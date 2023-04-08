package container

import "strings"

// shellQuote wraps s in single quotes for POSIX sh, escaping any
// embedded single quote. Inside single quotes the shell expands
// nothing, so the result is injection-safe for any input, including a
// username crafted to break out of the quoting.
func shellQuote(s string) string {
	// Each embedded quote becomes: close the quoting, emit an escaped
	// quote, reopen. (The literal is kept out of the doc comment above
	// because gofmt rewrites consecutive apostrophes there.)
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
