package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Startup-wiring guards for the metrics-export path (#1077, finding 1).
//
// The behaviour of the resume path is already well covered:
// TestStartMetricsExportIfEnabled_ResumesFromPersistedConfig,
// TestMetricsExport_LateStoreWiring_NotCachedAsDisabled (#1070) and
// TestCurrentExportLabels / ..._ResumeCapturesLiveIdentity (#1080) all
// pin what ContainerServer does *given* the calls.
//
// None of them can catch the failure #1077 was actually filed about: a
// wiring call silently disappearing from dual_server.go. That is not
// hypothetical — #1075 shipped with a comment claiming a call existed at
// a call site where it did not, and no test noticed, because the tests
// hand-injected the dependency instead of exercising the real startup
// path.
//
// NewDualServer needs a database, an incus connection and a filled
// DualServerConfig, so constructing one in a unit test is not realistic.
// These tests assert over the parsed AST of dual_server.go instead. That
// is a narrow tool and deliberately so: it answers exactly one question
// — is the call still there, in the right function — which is the whole
// of the regression class. It reads the AST rather than matching text,
// so reformatting, renamed locals and moved comments do not disturb it.

// dualServerFuncCalls returns the method names called inside the named
// top-level function of dual_server.go, in source order.
func dualServerFuncCalls(t *testing.T, funcName string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dual_server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse dual_server.go: %v", err)
	}

	var target *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == funcName {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatalf("function %q not found in dual_server.go — it was renamed or removed, "+
			"and these wiring guards need updating with it", funcName)
	}

	var calls []string
	ast.Inspect(target, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			// x.Foo()
			calls = append(calls, fn.Sel.Name)
		case *ast.Ident:
			// Foo() — plain function calls, which constructors are. Without
			// this the guards could only see method calls, so a dropped
			// NewXxx(...) was invisible to them.
			calls = append(calls, fn.Name)
		}
		return true
	})
