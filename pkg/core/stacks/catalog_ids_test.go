package stacks

import "testing"

// InstallStack records the stack only when the ID resolves as a dev stack
// (GetStack wins over GetBaseScript). A base script that shared an ID with a
