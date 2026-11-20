package serverconfig

import (
	"strings"
	"testing"
)

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJi5Jedy5KNPnCRATLPXN1w/JstcLrxCXM8y3z6qQZ8W me@laptop"

func TestAGoodConfigIsAccepted(t *testing.T) {
	c, bad := Parse([]byte(`{"schema":"lite-deploy/server/v1","users":[{"name":"serafim","ssh_keys":["` + key + `"]}],"firewall":"max","plugins":["caddy","pg"],
	  "backup":{"recipient":"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p","encrypt_dumps":true,"schedule":true}}`))
	if c == nil {
		t.Fatalf("%v", bad)
	}
	if !c.SecurityOn() || len(c.Users) != 1 || c.Plugins[1] != "pg" {
		t.Errorf("%+v", c)
	}
}

func TestBadConfigsAreExplained(t *testing.T) {
	for name, text := range map[string]string{
		"wrong schema":      `{"schema":"x"}`,
		"a bad user":        `{"schema":"lite-deploy/server/v1","users":[{"name":"Root User"}]}`,
		"a bad key":         `{"schema":"lite-deploy/server/v1","users":[{"name":"a","ssh_keys":["not a key"]}]}`,
		"a bad firewall":    `{"schema":"lite-deploy/server/v1","firewall":"open"}`,
		"an unknown plugin": `{"schema":"lite-deploy/server/v1","plugins":["docker"]}`,
		"a private key":     `{"schema":"lite-deploy/server/v1","backup":{"recipient":"AGE-SECRET-KEY-1QQQ"}}`,
		"an unknown field":  `{"schema":"lite-deploy/server/v1","password":"x"}`,
	} {
		if c, bad := Parse([]byte(text)); c != nil || len(bad) == 0 || strings.TrimSpace(bad[0]) == "" {
			t.Errorf("%s must be refused with a reason", name)
		}
	}
}
