package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "перезаписать schemas/*.json и golden-файлы")

// Закоммиченные схемы обязаны совпадать с генерируемыми из Go-типов (SPEC-v0.md, раздел 0).
func TestCommittedSchemasMatch(t *testing.T) {
	for _, name := range SchemaNames {
		want := Schemas()[name]
		path := filepath.Join("..", "..", "schemas", name+".schema.json")
		if *update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (запустите go test ./internal/cli -update)", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s устарел: запустите go test ./internal/cli -update", path)
		}
	}
}
