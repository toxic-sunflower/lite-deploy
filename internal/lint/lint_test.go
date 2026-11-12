// Package lint содержит проверки правил репозитория, которые удобнее держать тестами.
package lint

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Любая внешняя команда запускается только через host.Runner (SPEC-v0.md, раздел 1):
// os/exec и syscall можно импортировать только в internal/host; unsafe и cgo нельзя нигде.
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
				t.Errorf("%s импортирует %s: unsafe и cgo запрещены везде (D-23)", rel, imp.Path.Value)
			case `"os/exec"`, `"syscall"`:
				if !inHost {
					t.Errorf("%s импортирует %s: разрешено только в internal/host", rel, imp.Path.Value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
