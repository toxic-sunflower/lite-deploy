package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "overwrite schemas/*.json and the golden files")

// The committed schemas must match the ones generated from the Go types (SPEC-v0.md, section 0).
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
			t.Fatalf("%s: %v (run go test ./internal/cli -update)", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is outdated: run go test ./internal/cli -update", path)
		}
	}
}
