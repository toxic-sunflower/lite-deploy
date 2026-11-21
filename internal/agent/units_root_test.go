package agent

import (
	"strings"
	"testing"
)

// An explicit User=root makes systemd 255 drop CAP_SETUID from the service, and runuser stops working.
func TestRootUnitsHaveNoExplicitUserRoot(t *testing.T) {
	for _, name := range []string{"lite-deploy-exec@.service", "lite-deploy-switch@.service"} {
		body := Units("/usr/local/bin/lite-deploy", "sudo")[name]
		if body == "" {
			t.Fatalf("%s is missing", name)
		}
		if strings.Contains(body, "\nUser=") {
			t.Errorf("%s sets User=: it must run as the default root", name)
		}
	}
}
