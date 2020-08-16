package sentinel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The tunnel server's OnConnect/OnDisconnect callbacks run on goroutines that
// OUTLIVE the test function, so they must never touch *testing.T (#1374).
//
// A t.Logf in one of them raced tRunner's write marking the test complete,
// and — because it fired only when a session happened to close after the test
// returned — presented as an intermittent failure in PRs that touched nothing
// nearby. Two unrelated PRs were investigated for it before the cause was
// found.
//
// Re-adding a t.Logf there would be an easy and natural thing to do while
// debugging, and the resulting flake would take days to attribute again. So
// the constraint is enforced rather than described.
//
// This asserts over the parsed AST rather than by running the test: the race
// is a scheduling accident that a passing run cannot rule out. What CAN be
// checked deterministically is that the callbacks do not reference the test
// at all, which is what makes the race impossible.
func TestTunnelCallbacksDoNotTouchTheTestingT(t *testing.T) {
	const file = "tunnel_integration_test.go"

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	checked := 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "OnConnect" && sel.Sel.Name != "OnDisconnect" {
			return true
		}
		lit, ok := assign.Rhs[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		checked++

		ast.Inspect(lit.Body, func(inner ast.Node) bool {
