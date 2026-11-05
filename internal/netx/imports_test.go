package netx_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOnlyNetxAndDoctorImportHTTP keeps the network explicit: other packages reach it only through these two.
func TestOnlyNetxAndDoctorImportHTTP(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := []string{filepath.Join(root, "internal", "netx"), filepath.Join(root, "internal", "doctor")}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path != root && skipDir(d.Name()):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
			return nil
		case underDir(path, allowed):
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http" || strings.HasPrefix(p, "net/http/") {
				t.Errorf("%s imports %s: connect through internal/netx", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "testdata" || name == "private"
}

func underDir(path string, dirs []string) bool {
	for _, dir := range dirs {
		if strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
