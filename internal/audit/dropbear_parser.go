package audit

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Parsing dropbear's login lines (#1189).
//
// K8s boxes run dropbear, not OpenSSH, and the two log successful
// authentication in completely different words. The existing sshd parser
// matches "Accepted publickey for alice from 10.0.0.1 port 54321"; dropbear
// never emits that string, so an sshd-shaped parser pointed at a K8s box
// silently yields zero login records — indistinguishable from a box nobody
// logged into.
//
// The formats below are taken from the dropbear binary's own format strings
// (2022.83), not from documentation or memory:
//
//	Password auth succeeded for '%s' from %s
//	Pubkey auth succeeded for '%s' with %s key %s from %s
//	Auth succeeded with blank password for '%s' from %s
//
// Note the shape differences that matter for a regex: the username is
// single-quoted, the source is host:port rather than "from IP port N", and
// the method word comes FIRST rather than after "Accepted".

var (
	// dropbearPubkeyPattern: Pubkey auth succeeded for 'alice' with ssh-ed25519 key SHA256:... from 10.0.0.1:54321
	dropbearPubkeyPattern = regexp.MustCompile(
		`Pubkey auth succeeded for '([^']+)' with \S+ key \S+ from (\S+)`,
	)
	// dropbearPasswordPattern: Password auth succeeded for 'alice' from 10.0.0.1:54321
	dropbearPasswordPattern = regexp.MustCompile(
		`Password auth succeeded for '([^']+)' from (\S+)`,
	)
	// dropbearBlankPattern: Auth succeeded with blank password for 'alice' from 10.0.0.1:54321
	//
