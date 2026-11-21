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
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestUnitAbsoluteProgram(t *testing.T) {
	got := Unit(actions.InstallUnit{Project: "shop", Service: "api", Slot: "green", HostPort: 18002, Exec: []string{"/usr/bin/node", "server.js"}, Memory: "1g", Pids: 64})
	if !strings.Contains(got, `ExecStart="/usr/bin/node" "server.js"`) || !strings.Contains(got, "MemoryMax=1G") {
		t.Fatal(got)
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
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestQuadletDataVolumes(t *testing.T) {
	got := Quadlet(actions.InstallQuadlet{Project: "shop", Service: "api", Slot: "blue", HostPort: 18001, ContainerPort: 8080, Memory: "512m", Pids: 64, Data: []string{"uploads"}})
	if !strings.Contains(got, "Volume=%h/.local/share/lite-deploy/data/uploads:/data/uploads:U") {
		t.Fatal(got)
	}
}
