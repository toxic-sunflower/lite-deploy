package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const fallbackAuthUsage = "usage: lite-deploy fallback-auth <project> <site> [set --user NAME --password-stdin [--path /admin*]... | remove-user NAME | off] [--yes]"

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// cmdFallbackAuth shows or changes the password (HTTP Basic) that Caddy asks for when a site is served directly. It is the
// safe way in when the Cloudflare Tunnel, and Cloudflare Access with it, is not in front of an admin area. In tunnel mode
// nothing changes: the password is applied only to the direct block of the site.
func cmdFallbackAuth(args []string, env Env) result {
	const command = "fallback-auth"
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	user := fs.String("user", "", "")
	stdin := fs.Bool("password-stdin", false, "")
	var paths stringList
	fs.Var(&paths, "path", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) < 2 || len(pos) > 4 {
		return fail(command, CodeUsage, fallbackAuthUsage)
	}
	name, siteName := pos[0], pos[1]
	doc, sites, idx, bad := loadSite(command, env, name, siteName)
	if bad != nil {
		return *bad
	}
	var fa struct {
		Paths []string                     `json:"paths,omitempty"`
		Users []map[string]json.RawMessage `json:"users"`
	}
	if v, ok := sites[idx]["fallback_auth"]; ok {
		_ = json.Unmarshal(v, &fa)
	}
	names := func() []string {
		var out []string
		for _, u := range fa.Users {
			var n string
			_ = json.Unmarshal(u["name"], &n)
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	if len(pos) == 2 {
		where := "the whole site"
		if len(fa.Paths) > 0 {
			where = strings.Join(fa.Paths, " ")
		}
		text := fmt.Sprintf("Site %s of %s has no password for direct mode.\n", siteName, name)
		if len(fa.Users) > 0 {
			text = fmt.Sprintf("Site %s of %s asks for a password on %s when served directly. Logins: %s.\n", siteName, name, where, strings.Join(names(), ", "))
		}
		return result{command: command, data: map[string]any{"project": name, "site": siteName, "paths": fa.Paths, "users": names()}, text: text}
	}
	switch pos[2] {
	case "off":
		if len(pos) != 3 {
			return fail(command, CodeUsage, fallbackAuthUsage)
		}
		delete(sites[idx], "fallback_auth")
	case "remove-user":
		if len(pos) != 4 {
			return fail(command, CodeUsage, fallbackAuthUsage)
		}
		var keep []map[string]json.RawMessage
		for _, u := range fa.Users {
			var n string
			_ = json.Unmarshal(u["name"], &n)
			if n != pos[3] {
				keep = append(keep, u)
			}
		}
		if len(keep) == len(fa.Users) {
			return fail(command, CodeUsage, "there is no login "+pos[3])
		}
		if len(keep) == 0 {
			delete(sites[idx], "fallback_auth")
		} else {
			fa.Users = keep
			sites[idx]["fallback_auth"], _ = json.Marshal(fa)
		}
	case "set":
		if len(pos) != 3 || *user == "" || !*stdin {
			return fail(command, CodeUsage, fallbackAuthUsage)
		}
		line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
		pw := strings.TrimRight(line, "\r\n")
		if len(pw) < 12 || len(pw) > 72 {
			return fail(command, CodeUsage, "the password must be 12 to 72 characters long")
		}
		hash, herr := bcrypt.GenerateFromPassword([]byte(pw), 12)
		if herr != nil {
			return fail(command, CodeInternal, herr.Error())
		}
		h, _ := json.Marshal(string(hash))
		n, _ := json.Marshal(*user)
		entry := map[string]json.RawMessage{"name": n, "password_hash": h}
		replaced := false
		for i, u := range fa.Users {
			var un string
			_ = json.Unmarshal(u["name"], &un)
			if un == *user {
				fa.Users[i], replaced = entry, true
			}
		}
		if !replaced {
			fa.Users = append(fa.Users, entry)
		}
		if len(paths) > 0 {
			fa.Paths = paths
		}
		sites[idx]["fallback_auth"], _ = json.Marshal(fa)
	default:
		return fail(command, CodeUsage, fallbackAuthUsage)
	}
	r := applySiteChange(command, env, doc, sites, *yes)
	if r.err == nil {
		r.text += "The password is asked for only while the site is served directly (lite publish " + name + " " + siteName + " direct); in tunnel mode nothing changes.\n"
	}
	return r
}
