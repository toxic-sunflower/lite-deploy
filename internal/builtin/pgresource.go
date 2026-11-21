package builtin

import (
	"regexp"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

// pgSocketDir is where PostgreSQL keeps its socket. A project shares it with its container to reach the database (the
// service setting `sockets`), so no network port is needed.
const pgSocketDir = "/var/run/postgresql"

// pgDefaultTemplate is the usual form of the address: a URL. Whoever needs another notation gives a template of their own,
// for example "Host={host};Database={db};Username={user};Password={password}"; nothing here lists notations.
const pgDefaultTemplate = "postgresql://{user}:{password}@/{db}?host={host}"

var pgTemplateRe = regexp.MustCompile(`^[ -~]{3,200}$`)

// pgConnection fills a template with the parts of the connection: {host} (the socket directory), {port}, {user},
// {password} and {db}. The password is hexadecimal and the names are plain, so no escaping is needed in any notation.
func pgConnection(template, role, db, password string) (string, error) {
	if template == "" {
		template = pgDefaultTemplate
	}
	if !pgTemplateRe.MatchString(template) || !strings.Contains(template, "{") {
		return "", plugin.Errorf("usage", "the template must be printable text with placeholders such as {user} {password} {db} {host} {port}")
	}
	out := strings.NewReplacer("{host}", pgSocketDir, "{port}", "5432", "{user}", role, "{password}", password, "{db}", db).Replace(template)
	if strings.Contains(out, "{") && strings.Contains(out, "}") && !strings.Contains(template, "{{") {
		// an unknown placeholder is a mistake, not text to keep
		if m := regexp.MustCompile(`\{[a-z_]+\}`).FindString(out); m != "" {
			return "", plugin.Errorf("usage", "unknown placeholder %s: the parts are {host} {port} {user} {password} {db}", m)
		}
	}
	return out, nil
}

var pgHbaLocalAll = regexp.MustCompile(`^\s*local\s+all\s+all\s+`)

// ensureHba keeps the line "local ROLE ROLE scram-sha-256" between markers, above the general rule "local all all", so that
// the role may log in over the socket with its password.
func (t tools) ensureHba(role string) error {
	path, err := t.psql("SHOW hba_file")
	if err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	raw, err := t.env.FS.ReadFile(path)
	if err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	begin, end := "# lite-deploy:begin "+role, "# lite-deploy:end "+role
	block := begin + "\nlocal   " + role + "   " + role + "   scram-sha-256\n" + end
	var out []string
	inBlock, found := false, false
	for _, l := range strings.Split(string(raw), "\n") {
		switch {
		case strings.TrimSpace(l) == begin:
			inBlock, found = true, true
			out = append(out, block)
		case strings.TrimSpace(l) == end:
			inBlock = false
		case inBlock:
		default:
			out = append(out, l)
		}
	}
	if !found {
		at := -1
		for i, l := range out {
			if pgHbaLocalAll.MatchString(l) {
				at = i
				break
			}
		}
		if at < 0 {
			return plugin.Errorf("step_failed", "pg_hba.conf has no general \"local all all\" rule: cannot tell where to insert the rule of the role")
		}
		out = append(out[:at], append([]string{block}, out[at:]...)...)
	}
	next := strings.Join(out, "\n")
	if next == string(raw) {
		return nil
	}
	info, err := t.env.FS.Stat(path)
	if err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := t.env.FS.WriteFile(path, []byte(next), info.Mode); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := t.env.FS.Chown(path, info.Owner, info.Group); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	return t.must("systemctl", "reload", "postgresql")
}

// storeConnection writes the ready connection string as a secret of a project: for the project it is an ordinary secret.
func (t tools) storeConnection(project, name, value string) error {
	if !secrets.ValidProject(project) || !secrets.ValidName(name) {
		return plugin.Errorf("usage", "a project name and a secret name (lowercase letters, digits, _) are needed")
	}
	if _, err := secrets.NewStore(t.env.FS).Put(project, name, []byte(value)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	return nil
}
