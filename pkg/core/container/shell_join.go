package container

import "strings"

// shellQuote wraps s in single quotes for POSIX sh, escaping any
// embedded single quote. Inside single quotes the shell expands
// nothing, so the result is injection-safe for any input, including a
