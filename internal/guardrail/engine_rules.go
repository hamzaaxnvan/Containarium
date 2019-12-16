package guardrail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// RulesEngineID names the reference engine in attestations.
const RulesEngineID = "containarium-reference-rules"

// rule is one regular-expression recognizer. The reference engine is
// exactly this table and nothing else: no context, no checksums, no
// models. It exists so `containarium guardrail` runs end to end on a
// fresh checkout; a production engine replaces it behind the same RPC.
type rule struct {
	kind    pb.GuardrailKind
	typ     string
	pattern *regexp.Regexp
	// validate rejects a regex match that fails a cheap structural check
	// (a Luhn checksum, say). nil accepts every match.
	validate func(string) bool
}

var referenceRules = []rule{
	{pb.GuardrailKind_GUARDRAIL_KIND_PII, "EMAIL",
		regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_PII, "SSN",
		regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_PII, "PHONE_US",
		regexp.MustCompile(`(?:\+1[ .-]?)?(?:\(\d{3}\)|\d{3})[ .-]?\d{3}[ .-]?\d{4}\b`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_PII, "IPV4",
		regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_PII, "CREDIT_CARD",
		regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`), luhnOK},
	{pb.GuardrailKind_GUARDRAIL_KIND_SECRET, "PRIVATE_KEY",
		regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_SECRET, "AWS_ACCESS_KEY_ID",
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), nil},

	// Inbound kinds: signatures over model output on its way into a box
	// (docs/architecture/guardrail-inbound-and-server-policy.md). Shapes,
	// not semantics: a starting point, not a detector to rely on.

	// A fetched script piped straight into an interpreter.
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DOWNLOAD_EXECUTE",
		regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\n|;&]*\|\s*(?:sudo\s+)?(?:(?:ba|z|da|k)?sh|python[0-9.]*|perl|ruby|node)\b`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DOWNLOAD_EXECUTE",
		regexp.MustCompile(`(?i)\b(?:(?:ba|z)?sh|source|\.)\s+<\(\s*(?:curl|wget)\b`), nil},
	// Recursive force-delete of the filesystem root or the home directory
	// itself (not a path under it).
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DESTRUCTIVE_SHELL",
		regexp.MustCompile(`\brm\s+(?:-[A-Za-z]*(?:rf|fr|r[A-Za-z]*f|f[A-Za-z]*r)[A-Za-z]*|-r\s+-f|-f\s+-r|--recursive\s+--force|--force\s+--recursive)\s+(?:--no-preserve-root\s+)?(?:/\*?|~/?|\$HOME/?|\$\{HOME\}/?)(?:[\s;&|]|$)`), nil},
	// Formatting or overwriting a block device.
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DESTRUCTIVE_SHELL",
		regexp.MustCompile(`\bmkfs(?:\.[a-z0-9]+)?\s+(?:-\S+\s+)*/dev/`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DESTRUCTIVE_SHELL",
		regexp.MustCompile(`\bdd\b[^\n]*\bof=/dev/(?:sd|hd|vd|xvd|nvme|disk|mmcblk)`), nil},
	// The classic shell fork bomb.
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "DESTRUCTIVE_SHELL",
		regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), nil},
	// A network client handed a credential file, either as an argument or
	// on its stdin.
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "CREDENTIAL_EXFIL",
		regexp.MustCompile(`(?i)\b(?:curl|wget|nc|ncat|scp)\b[^\n]*(?:` + exfilTargetFiles + `)`), nil},
	{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE, "CREDENTIAL_EXFIL",
		regexp.MustCompile(`(?i)(?:` + exfilTargetFiles + `)[^\n]*\|\s*(?:curl|wget|nc|ncat)\b`), nil},

	// "Ignore the previous instructions" and its near variants.
	{pb.GuardrailKind_GUARDRAIL_KIND_PROMPT_INJECTION, "INSTRUCTION_OVERRIDE",
		regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override)\s+(?:all\s+|any\s+)?(?:of\s+)?(?:the\s+|your\s+|my\s+)?(?:previous|prior|above|earlier|preceding|system|original)\s+(?:instructions|prompts?|rules|directions|guidelines)\b`), nil},
	// An attempt to switch the model into an unrestricted persona.
	{pb.GuardrailKind_GUARDRAIL_KIND_PROMPT_INJECTION, "ROLE_HIJACK",
		regexp.MustCompile(`(?i)\byou\s+are\s+now\s+(?:in\s+)?(?:DAN\b|developer\s+mode|jailbreak\s+mode|an?\s+unrestricted\b)`), nil},
	// An attempt to make the model disclose its own instructions.
	{pb.GuardrailKind_GUARDRAIL_KIND_PROMPT_INJECTION, "SYSTEM_PROMPT_EXFIL",
		regexp.MustCompile(`(?i)\b(?:reveal|print|output|repeat|show|leak)\s+(?:me\s+)?your\s+(?:system\s+prompt|hidden\s+instructions|initial\s+instructions)\b`), nil},
}

// exfilTargetFiles is the set of sensitive files whose appearance next to a
// network client reads as exfiltration. The public half of a key pair
// (id_*.pub) is excluded by requiring the match to end there.
const exfilTargetFiles = `(?:~|\$HOME|\$\{HOME\}|/root|/home/[^/\s]+)/\.(?:ssh/id_[a-z0-9_]+\b(?:[^.]|$)|aws/credentials|docker/config\.json|kube/config|netrc)|/etc/shadow\b`

