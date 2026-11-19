package render

import (
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

func TestUnitText(t *testing.T) {
	got := Unit(actions.InstallUnit{Project: "shop", Service: "api", Slot: "blue", HostPort: 18001,
		Exec: []string{"./bin/app", "--name", `a "b" 50%$HOME`}, Memory: "768m", Pids: 256})
	for _, want := range []string{
		"ConditionPathExists=%h/.local/share/lite-deploy/app/api/blue/.release",
		"WorkingDirectory=%h/.local/share/lite-deploy/app/api/blue",
		"Environment=PORT=18001",
		`ExecStart="%h/.local/share/lite-deploy/app/api/blue/bin/app" "--name" "a \"b\" 50%%$$HOME"`,
		"MemoryMax=768M", "TasksMax=256", "NoNewPrivileges=true", "WantedBy=default.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в:\n%s", want, got)
		}
	}
}

func TestUnitAbsoluteProgram(t *testing.T) {
	got := Unit(actions.InstallUnit{Project: "shop", Service: "api", Slot: "green", HostPort: 18002, Exec: []string{"/usr/bin/node", "server.js"}, Memory: "1g", Pids: 64})
	if !strings.Contains(got, `ExecStart="/usr/bin/node" "server.js"`) || !strings.Contains(got, "MemoryMax=1G") {
		t.Fatal(got)
	}
}

func TestRemoteDatabaseURL(t *testing.T) {
	for format, want := range map[string]string{
		"url":    "postgresql://shop:p%40ss%2Fw@10.0.0.2:5432/shop?sslmode=require",
		"npgsql": `Host=10.0.0.2;Port=5432;Database=shop;Username=shop;Password="p@ss/w;x";SSL Mode=Require`,
		"libpq":  `host=10.0.0.2 port=5432 dbname=shop user=shop password='p@ss/w;x' sslmode=require`,
	} {
		pw := "p@ss/w;x"
		if format == "url" {
			pw = "p@ss/w"
		}
		got, err := RemoteDatabaseURL(format, "10.0.0.2", 0, "shop", "shop", pw)
		if err != nil || got != want {
			t.Errorf("%s:\n got %s\nwant %s (%v)", format, got, want, err)
		}
	}
	if got, _ := RemoteDatabaseURL("url", "fd00::2", 6432, "a", "b", "c"); got != "postgresql://a:c@[fd00::2]:6432/b?sslmode=require" {
		t.Errorf("ipv6: %s", got)
	}
	if _, err := RemoteDatabaseURL("x", "h", 0, "a", "b", "c"); err == nil {
		t.Error("неизвестный формат")
	}
}

func TestUnitStopAndData(t *testing.T) {
	got := Unit(actions.InstallUnit{Project: "shop", Service: "api", Slot: "blue", HostPort: 18001,
		Exec: []string{"/usr/bin/podman", "compose", "up"}, Stop: []string{"/usr/bin/podman", "compose", "down"},
		Data: []string{"uploads", "tmp-files"}, Memory: "512m", Pids: 64})
	for _, want := range []string{
		`ExecStop="/usr/bin/podman" "compose" "down"`,
		"Environment=LITE_DATA_UPLOADS=%h/.local/share/lite-deploy/data/uploads",
		"Environment=LITE_DATA_TMP_FILES=%h/.local/share/lite-deploy/data/tmp-files",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в:\n%s", want, got)
		}
	}
}

func TestQuadletDataVolumes(t *testing.T) {
	got := Quadlet(actions.InstallQuadlet{Project: "shop", Service: "api", Slot: "blue", HostPort: 18001, ContainerPort: 8080, Memory: "512m", Pids: 64, Data: []string{"uploads"}})
	if !strings.Contains(got, "Volume=%h/.local/share/lite-deploy/data/uploads:/data/uploads:U") {
		t.Fatal(got)
	}
}
