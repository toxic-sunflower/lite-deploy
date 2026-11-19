// Package lint holds checks of repository rules that are easier to keep as tests.
package lint

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Any external command is run only through host.Runner (SPEC-v0.md, section 1):
// os/exec and syscall may be imported only in internal/host; unsafe and cgo nowhere.
func TestExecOnlyInHostPackage(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		inHost := strings.HasPrefix(filepath.ToSlash(rel), "internal/host/")
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			switch imp.Path.Value {
			case `"unsafe"`, `"C"`:
				t.Errorf("%s imports %s: unsafe and cgo are forbidden everywhere (D-23)", rel, imp.Path.Value)
			case `"os/exec"`, `"syscall"`:
				if !inHost {
					t.Errorf("%s imports %s: only allowed in internal/host", rel, imp.Path.Value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
