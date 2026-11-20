package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/deploy"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// peerFromStdin is the user id of whoever is connected to the socket that systemd gave this process as standard input.
func peerFromStdin(in io.Reader) (int, error) {
	f, ok := in.(*os.File)
	if !ok {
		return -1, errors.New("no socket on standard input")
	}
	c, err := net.FileConn(f)
	if err != nil {
		return -1, err
	}
	defer func() { _ = c.Close() }()
	return host.PeerUID(c)
}

// userName finds the name of a user id in /etc/passwd.
func userName(env Env, uid int) string {
	b, err := env.FS.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) >= 3 && f[2] == strconv.Itoa(uid) {
			return f[0]
		}
	}
	return ""
}

// cmdSwitchServe is the root helper that moves the traffic of a project to a slot. A project user that deploys (for example
// CI over ssh) cannot be allowed to write Caddy configuration or to reload Caddy: so it asks this helper. The helper
// identifies the caller by the kernel, serves only users of managed projects, and only for their own sites. The caller says
// only the slot; the files are written from a fixed template with the ports of the project description.
func cmdSwitchServe(env Env) result {
	const command = "switch-serve"
	if !env.root() {
		return fail(command, "permission_denied", "the helper is started by systemd as root")
	}
	reply := func(ok bool, msg string) result {
		b, _ := json.Marshal(deploy.SwitchReply{OK: ok, Error: msg})
		return result{command: command, raw: b}
	}
	peer := env.PeerUID
	if peer == nil {
		peer = func() (int, error) { return peerFromStdin(env.Stdin) }
	}
	uid, err := peer()
	if err != nil || uid <= 0 {
		return reply(false, "could not tell who is asking")
	}
	name := userName(env, uid)
	if name == "" {
		return reply(false, "unknown user")
	}
	if _, err := env.FS.Stat("/etc/lite-deploy/projects/" + name + ".json"); err != nil {
		return reply(false, "this user is not the user of a project managed by lite-deploy")
	}
	line, err := bufio.NewReader(io.LimitReader(env.Stdin, 1024)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return reply(false, "no request")
	}
	var req deploy.SwitchRequest
	if json.Unmarshal(line, &req) != nil || (req.Slot != "blue" && req.Slot != "green") {
		return reply(false, "the slot must be blue or green")
	}
	m, e := deploy.Open(deployEnv(env), name)
	if e != nil {
		return reply(false, e.Message)
	}
	if e := m.SwitchSlot(context.Background(), req.Slot); e != nil {
		return reply(false, e.Message)
	}
	return reply(true, "")
}
