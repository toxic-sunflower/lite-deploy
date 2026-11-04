package deploy

import (
	"strings"
	"testing"
)

const goodSHA = "0123456789abcdef0123456789abcdef01234567"

func TestParseGateAllowed(t *testing.T) {
	cases := map[string]GateCommand{
		"deploy " + goodSHA: {Verb: VerbDeploy, SHA: goodSHA},
		"rollback":          {Verb: VerbRollback},
		"status":            {Verb: VerbStatus},
		"status --json":     {Verb: VerbStatus, JSON: true},
	}
	for in, want := range cases {
		got, err := ParseGate(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v, %v", in, got, err)
		}
	}
}

func TestParseGateRejected(t *testing.T) {
	notAllowed := []string{
		"", " ", "ls", "bash", "sh -c id", "deploy", "rollback now", "status --json ", " status",
		"rollback\n", "status\n", "status; id", "status && id", "status | cat", "status `id`", "status $(id)",
		"rollback;id", "Deploy " + goodSHA, "deploy" + goodSHA, "sudo rollback", "scp -t /tmp", "cat /etc/passwd",
		"status --json --foo", "status\x00", "\nstatus",
	}
	for _, in := range notAllowed {
		if _, err := ParseGate(in); err == nil || err.Code != CodeNotAllowed {
			t.Errorf("%q: ожидали command_not_allowed, получили %v", in, err)
		}
	}
	badSHA := []string{
		"deploy ", "deploy abc", "deploy " + strings.ToUpper(goodSHA), "deploy " + goodSHA + "0", "deploy " + goodSHA[:39],
		"deploy " + goodSHA + " ", "deploy " + goodSHA + "\n", "deploy " + goodSHA + ";id", "deploy $(id)",
		"deploy " + goodSHA[:39] + "g", "deploy  " + goodSHA, "deploy -rf", "deploy " + goodSHA + " extra",
		"deploy " + goodSHA[:20] + " " + goodSHA[20:],
	}
	for _, in := range badSHA {
		if _, err := ParseGate(in); err == nil || err.Code != CodeInvalidSHA {
			t.Errorf("%q: ожидали invalid_sha, получили %v", in, err)
		}
	}
}
