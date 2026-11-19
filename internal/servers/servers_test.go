package servers

import (
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func TestRoundTripAndValidation(t *testing.T) {
	fs := host.NewMemFS()
	if m, err := Load(fs); err != nil || len(m) != 0 {
		t.Fatalf("пустой реестр: %v %v", m, err)
	}
	if err := Save(fs, map[string]string{"db1": "10.0.0.2", "app2": "app2.internal"}); err != nil {
		t.Fatal(err)
	}
	m, err := Load(fs)
	if err != nil || m["db1"] != "10.0.0.2" || len(Names(m)) != 2 || Names(m)[0] != "app2" {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"0.0.0.0", "127.0.0.1", "::1", "a b", "", "x;y", "-bad.example"} {
		if ValidAddress(bad) == nil {
			t.Errorf("адрес %q должен быть отклонён", bad)
		}
	}
	for _, good := range []string{"10.0.0.2", "db1.internal", "fd00::2"} {
		if err := ValidAddress(good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	if ValidName("local") || ValidName("Db") || !ValidName("db1") {
		t.Error("проверка имён")
	}
}

func TestCorruptRegistry(t *testing.T) {
	fs := host.NewMemFS()
	fs.Files[Path] = []byte("{")
	if _, err := Load(fs); err == nil {
		t.Fatal("повреждённый файл должен давать ошибку")
	}
}
