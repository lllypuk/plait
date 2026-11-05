package agent_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/lllypuk/plait/internal/agent"
)

// TestNoAgentNamesInCode keeps agents data: Go code outside tests names no built-in agent, writer names aside.
func TestNoAgentNamesInCode(t *testing.T) {
	r, err := agent.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		for _, hit := range agentLiterals(fset, f, r.Names()) {
			t.Errorf("%s; move it to a descriptor", hit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func agentLiterals(fset *token.FileSet, f *ast.File, names []string) []string {
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || slices.Contains(agent.Writers(), s) {
			return true
		}
		words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, w := range words {
			if slices.Contains(names, w) {
				hits = append(hits, fset.Position(lit.Pos()).String()+": literal "+lit.Value+" names agent "+w)
			}
		}
		return true
	})
	return hits
}
