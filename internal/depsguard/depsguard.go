// Package depsguard is the Go half of the dependency-change gate (#2794,
// story 3 of #2649). It compares a PR's go.mod with its base and fails on:
//
//   - a new or changed `replace` directive (an existing, unchanged one passes,
//     so a deliberate pin that predates the PR is not re-litigated on every PR);
//   - a new or version-changed module that sum.golang.org has no record of, or
//     whose go.sum hash differs from the checksum database's;
//   - (CheckEnv) a Go environment that would bypass the checksum database.
//
// The go command consults the checksum database only for modules missing
// from go.sum, so a PR that adds a module *and* its go.sum lines is never
// checked by `go mod download` or `go mod verify`. That is the gap this
// closes: every new or changed requirement is looked up explicitly.
//
// It also lists each new module with its publication age (from the module
// proxy's .info timestamp) and source, for a reviewer to read. The policy is a
// hard fail with no label; the only override is a code-owner approval of the
// PR (decision 4 on #2649).
//
// Standard library only, by design: the gate must not widen the dependency
// graph it guards.
package depsguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Default endpoints. The checksum database is named explicitly rather than
// taken from GOSUMDB so a misconfigured environment cannot point the gate at
// a database it controls; CheckEnv fails that environment separately.
const (
	DefaultSumDB = "https://sum.golang.org"
	DefaultProxy = "https://proxy.golang.org"
	// SumDBName is the only GOSUMDB value CheckEnv accepts.
	SumDBName = "sum.golang.org"
	// YoungAge: modules published more recently than this are bold in the
	// report (#2649 story 3).
	YoungAge = 14 * 24 * time.Hour
	// Marker identifies the sticky PR comment so it is updated, not re-posted.
	Marker = "<!-- deps-guard -->"
)

// ModuleVersion is a module path with an optional version, as in
// `go mod edit -json`. A replacement with an empty Version is a local path.
type ModuleVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version,omitempty"`
}

// Require is one `require` line.
type Require struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Indirect bool   `json:"Indirect,omitempty"`
}

// Replace is one `replace` directive.
type Replace struct {
	Old ModuleVersion `json:"Old"`
	New ModuleVersion `json:"New"`
}

func (r Replace) String() string {
	s := r.Old.Path
	if r.Old.Version != "" {
		s += " " + r.Old.Version
	}
	s += " => " + r.New.Path
	if r.New.Version != "" {
		s += " " + r.New.Version
	}
	return s
}

// GoMod is the subset of `go mod edit -json` output the guard reads.
type GoMod struct {
	Require []Require `json:"Require"`
	Replace []Replace `json:"Replace"`
}

// ParseGoModJSON decodes `go mod edit -json` output.
func ParseGoModJSON(b []byte) (GoMod, error) {
	var m GoMod
	if err := json.Unmarshal(b, &m); err != nil {
		return GoMod{}, fmt.Errorf("decode go mod edit -json: %w", err)
	}
	return m, nil
}

// LoadGoMod parses a go.mod file with the go command's own parser. A missing
// file is an empty module (the base of a PR that adds go.mod).
func LoadGoMod(ctx context.Context, path string) (GoMod, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return GoMod{}, nil
	}
	var stderr bytes.Buffer
	// #nosec G204 -- fixed binary and subcommand, no shell; path is a go.mod file
	// the CI script names, passed as one argument.
	cmd := exec.CommandContext(ctx, "go", "mod", "edit", "-json", path)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return GoMod{}, fmt.Errorf("go mod edit -json %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return ParseGoModJSON(out)
}

// GoSum maps "path version" and "path version/go.mod" to their h1: hash.
// Conflicts lists the keys that appear more than once with different hashes;
// the go command refuses such a file, and the guard must not let its verdict
// depend on which of the lines it happened to read last.
type GoSum struct {
	Hashes    map[string]string
	Conflicts []string
}

// ParseGoSum parses a go.sum file.
func ParseGoSum(b []byte) (GoSum, error) {
	sum := GoSum{Hashes: map[string]string{}}
	for i, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return GoSum{}, fmt.Errorf("go.sum line %d: want 3 fields, got %d", i+1, len(f))
		}
		key := f[0] + " " + f[1]
		if prev, ok := sum.Hashes[key]; ok {
			if prev != f[2] && !slices.Contains(sum.Conflicts, key) {
				sum.Conflicts = append(sum.Conflicts, key)
			}
			continue
		}
		sum.Hashes[key] = f[2]
	}
	sort.Strings(sum.Conflicts)
	return sum, nil
}

// FindingKind names the rule a finding broke. The CLI prints it, and the
// self-test matches each failing fixture on it, so a fixture that fails for
// the wrong reason does not count.
type FindingKind string

const (
	FindingReplaceAdded   FindingKind = "replace-added"
	FindingReplaceChanged FindingKind = "replace-changed"
	FindingNotInSumDB     FindingKind = "not-in-sumdb"
	FindingSumMismatch    FindingKind = "sum-mismatch"
	FindingUnsafeEnv      FindingKind = "unsafe-env"
	FindingSumConflict    FindingKind = "gosum-conflict"
	FindingLayout         FindingKind = "layout"
)

// Finding is one policy failure.
//
// Module and Evidence carry text from the PR (go.mod, go.sum, file names) or
// from the network (the checksum database). A quoted local replace target in
// go.mod can hold any character, newlines and backticks included, so neither
// field is ever printed raw: String escapes them for logs, and RenderMarkdown
// puts them in code spans. Detail is fixed prose written here.
type Finding struct {
	Kind     FindingKind
	Module   string
	Detail   string
	Evidence string
}

// String is the finding as one log line, safe to print in a CI log.
func (f Finding) String() string {
	s := fmt.Sprintf("%s %s: %s", f.Kind, LogSafe(f.Module), f.Detail)
	if f.Evidence != "" {
		s += ": " + LogSafe(f.Evidence)
	}
	return s
}

// LogSafe escapes every control or non-printing character (newline, carriage
// return, Unicode line separators, ...) as a Go escape such as \n, so the
// result is always one line. A CI runner executes a workflow command only at
// the start of a line, so PR text that passes through LogSafe cannot issue
// one.
func LogSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || !unicode.IsPrint(r) {
			b.WriteString(strings.Trim(strconv.QuoteRune(r), "'"))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CheckLayout fails a tracked file that would put Go modules outside what
// deps-guard reads, which is the root go.mod and go.sum only: a second
// go.mod, a workspace (go.work, go.work.sum) or a vendor directory
// (vendor/modules.txt, which switches the go command to vendor mode). Without
// this the gate would silently stop covering part of the build.
func CheckLayout(files []string) []Finding {
	var out []Finding
	for _, f := range files {
		base := path.Base(f)
		var why string
		switch {
		case base == "go.mod" && f != "go.mod":
			why = "a second go.mod: deps-guard checks the root module only"
		case base == "go.work" || base == "go.work.sum":
			why = "a Go workspace file: deps-guard does not read workspaces"
		case base == "modules.txt" && path.Base(path.Dir(f)) == "vendor":
			why = "a vendor directory: deps-guard does not check vendored code"
		default:
			continue
		}
		out = append(out, Finding{Kind: FindingLayout, Module: f, Detail: why + "; extend the gate before adding it"})
	}
	return out
}

// GoEnv is the subset of `go env -json` that decides whether the checksum
// database is in force. Field names match the variables so the JSON decodes
// straight in.
type GoEnv struct {
	GOFLAGS    string
	GOSUMDB    string
	GONOSUMDB  string
	GOPRIVATE  string
	GONOPROXY  string
	GOINSECURE string
}

// GoEnvVars lists the variables to request from `go env -json`.
var GoEnvVars = []string{"GOFLAGS", "GOSUMDB", "GONOSUMDB", "GOPRIVATE", "GONOPROXY", "GOINSECURE"}

// CheckEnv fails an environment that lets a module skip the checksum
// database or lets a build rewrite go.mod. GOPRIVATE and GONOPROXY count
// because the go command derives GONOSUMDB from GOPRIVATE, and both mark
// modules as private, which this public repo has none of.
func CheckEnv(env GoEnv) []Finding {
	var out []Finding
	bad := func(name, detail, value string) {
		out = append(out, Finding{Kind: FindingUnsafeEnv, Module: name, Detail: detail, Evidence: value})
	}
	readonly := false
	for _, f := range strings.Fields(env.GOFLAGS) {
		if f == "-mod=readonly" {
			readonly = true
		}
	}
	if !readonly {
		bad("GOFLAGS", "does not contain -mod=readonly", fmt.Sprintf("%q", env.GOFLAGS))
	}
	if env.GOSUMDB != SumDBName {
		bad("GOSUMDB", "must be "+SumDBName, fmt.Sprintf("%q", env.GOSUMDB))
	}
	for name, v := range map[string]string{
		"GONOSUMDB": env.GONOSUMDB, "GOPRIVATE": env.GOPRIVATE,
		"GONOPROXY": env.GONOPROXY, "GOINSECURE": env.GOINSECURE,
	} {
		if v != "" {
			bad(name, "is set; it exempts modules from the checksum database or TLS", fmt.Sprintf("%q", v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out
}

// CheckReplaces fails every replace directive in head that is not present,
// identically, in base. A directive is identified by its left-hand side
// (path and optional version); a changed right-hand side is "changed", a new
// left-hand side is "added". Removing a replace passes.
func CheckReplaces(base, head GoMod) []Finding {
	baseBy := map[ModuleVersion]ModuleVersion{}
	for _, r := range base.Replace {
		baseBy[r.Old] = r.New
	}
	var out []Finding
	for _, r := range head.Replace {
		prev, ok := baseBy[r.Old]
		switch {
		case !ok:
			out = append(out, Finding{Kind: FindingReplaceAdded, Module: r.Old.Path,
				Detail: "new replace directive", Evidence: r.String()})
		case prev != r.New:
			was := Replace{Old: r.Old, New: prev}
			out = append(out, Finding{Kind: FindingReplaceChanged, Module: r.Old.Path,
				Detail: "replace directive changed (was, now)", Evidence: was.String() + "  ->  " + r.String()})
		}
	}
	return out
}

// ModuleChange is a requirement that is new in head, or whose version moved.
type ModuleChange struct {
	Path       string
	OldVersion string // empty: the module is new
	NewVersion string
	Indirect   bool
}

// Added reports whether the module is new rather than re-versioned.
func (c ModuleChange) Added() bool { return c.OldVersion == "" }

// DiffRequires lists added and re-versioned requirements, sorted by path.
func DiffRequires(base, head GoMod) []ModuleChange {
	was := map[string]string{}
	for _, r := range base.Require {
		was[r.Path] = r.Version
	}
	var out []ModuleChange
	for _, r := range head.Require {
		old, ok := was[r.Path]
		if ok && old == r.Version {
			continue
		}
		out = append(out, ModuleChange{Path: r.Path, OldVersion: old, NewVersion: r.Version, Indirect: r.Indirect})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// EscapePath applies the module proxy / checksum database case encoding:
// every upper-case letter becomes '!' plus its lower-case form. It is used
// for both paths and versions, which share the rule.
func EscapePath(s string) string {
	var b strings.Builder
