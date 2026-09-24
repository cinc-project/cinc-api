package cinc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Error strings reach users verbatim through callers such as cinc-cli, whose
// house style keeps the em dash (U+2014) out of user-facing text. Comments may
// use it; string literals in the shipped (non-test) code may not.
func TestNoEmDashInStringLiterals(t *testing.T) {
	var files []string
	for _, dir := range []string{".", "internal/signing"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				files = append(files, m)
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("found no source files to check")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "—") {
				t.Errorf("%s: string literal contains an em dash: %s", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}
