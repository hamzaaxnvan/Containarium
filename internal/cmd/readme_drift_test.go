//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
)

// README drift guard (#2244). The README's shell blocks are the first thing a
// new user copies; a renamed verb or a dropped flag there is a broken first
// run that no other test sees. These tests parse every fenced bash/sh/shell
// block in README.md and assert each `containarium <verb> [--flag]` resolves
// in the real cobra command tree, and that every in-page `(#anchor)` link
// points at a heading that exists.
//
// Untagged-build only (!containarium_client): on a host the installer links
// `containarium` -> `containariumd`, so a README invocation is valid if
// containariumd has it. The client tree is a strict subset of that one
// (TestContainariumdCommandTree_SupersetOfClientGolden), so checking the
// superset is the right bound for a document that covers both.

const readmePath = "../../README.md"

func TestREADMEQuickstartUsesAutomaticMCP(t *testing.T) {
	md := readDriftFile(t, readmePath)
	_, section, ok := strings.Cut(md, "## Quick start\n")
	if !ok {
		t.Fatal("Quick start section missing")
	}
	section, _, _ = strings.Cut(section, "## After your first box\n")
	found := false
	for _, invocation := range extractCLIInvocations(section) {
		if len(invocation.Args) == 0 || invocation.Args[0] != "quickstart" {
			continue
		}
		found = true
		for _, arg := range invocation.Args {
			if arg == "--no-mcp" {
				t.Fatal("Quick start still disables automatic MCP wiring")
			}
		}
	}
	if !found {
		t.Fatal("Quick start does not invoke quickstart")
	}
}

// cliInvocation is one `containarium …` command found in a fenced shell
// block: the words after the binary name, and where it was found.
type cliInvocation struct {
	Line int      // 1-based line of the command's first line in the file
	Args []string // shell words after `containarium`
}

// driftProblem is one invocation (or link) that doesn't resolve.
type driftProblem struct {
	Line   int
	Detail string
}

// shellFenceLangs are the fence info strings whose contents are treated as
// commands. Unlabelled fences (illustrative output, the verb table) and
// jsonc/ssh-config blocks are deliberately not checked.
var shellFenceLangs = map[string]bool{"bash": true, "sh": true, "shell": true}

// fenceInfo reports whether line opens/closes a code fence, and the info
// string's first word (lowercased) when it does.
func fenceInfo(line string) (bool, string) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") && !strings.HasPrefix(t, "~~~") {
		return false, ""
	}
	info := strings.Fields(strings.TrimLeft(t, "`~"))
	if len(info) == 0 {
		return true, ""
	}
	return true, strings.ToLower(info[0])
}

// extractCLIInvocations returns every `containarium …` command in the
// document's shell fences, with backslash continuations joined, comments
// dropped, and pipelines / `&&` / `;` / `$(…)` split into separate commands.
func extractCLIInvocations(markdown string) []cliInvocation {
	return extractInvocationsFor(markdown, readmeBinaries)
}

// readmeBinaries are the binary names whose invocations the README check
// reads. The upgrade runbook (runbook_drift_test.go) also reads containariumd.
var readmeBinaries = map[string]bool{"containarium": true}

// extractInvocationsFor is extractCLIInvocations for any set of binary names
// that resolve against the same cobra tree.
func extractInvocationsFor(markdown string, binaries map[string]bool) []cliInvocation {
	var out []cliInvocation
	inFence, shell := false, false
	var logical strings.Builder
	start := 0

	for i, line := range strings.Split(markdown, "\n") {
		if isFence, lang := fenceInfo(line); isFence {
			if !inFence {
				inFence, shell = true, shellFenceLangs[lang]
			} else {
				inFence, shell = false, false
			}
			continue
		}
		if !inFence || !shell {
			continue
		}
		if logical.Len() == 0 {
			start = i + 1
		}
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmed, "\\") {
			logical.WriteString(strings.TrimSuffix(trimmed, "\\"))
			logical.WriteByte(' ')
			continue
		}
		logical.WriteString(trimmed)
		for _, words := range splitShell(logical.String()) {
			if args, ok := containariumArgs(words, binaries); ok {
				out = append(out, cliInvocation{Line: start, Args: args})
			}
		}
		logical.Reset()
	}
	return out
}

// containariumArgs strips the prefixes a README puts before the binary
// (`sudo`, `VAR=value`, `env`) and returns the words after `containarium`.
func containariumArgs(words []string, binaries map[string]bool) ([]string, bool) {
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case w == "sudo" || w == "env" || w == "exec" || w == "time":
			i++
		case i > 0 && words[i-1] == "sudo" && strings.HasPrefix(w, "-"):
			i++
		case isEnvAssignment(w):
			i++
		default:
			if binaries[w] {
				return words[i+1:], true
			}
			return nil, false
		}
	}
	return nil, false
}

func isEnvAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for j, r := range w[:eq] {
		valid := r == '_' || unicode.IsLetter(r) || (j > 0 && unicode.IsDigit(r))
		if !valid {
			return false
		}
	}
	return true
}

// splitShell is a deliberately small shell word splitter: enough for README
// snippets, not a shell. It honours quotes and backslashes, drops `#`
// comments, ends a command at | ; & ( ), and lifts each `$(…)` out as a
// command of its own (the substitution stays in the outer word verbatim).
func splitShell(s string) [][]string {
	var segs [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
