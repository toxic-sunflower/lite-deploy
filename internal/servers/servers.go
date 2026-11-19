// Package servers реестр серверов, известных этому серверу: имя и адрес во внутренней сети (R-26).
// Проект живёт на одном сервере; чужие ресурсы (например, база) подключаются по адресу из реестра.
package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"regexp"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Path файл реестра; читает любой пользователь (адреса не секрет), пишет root.
const Path = "/etc/lite-deploy/servers.json"

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Local зарезервированное имя этого сервера.
const Local = "local"

// ValidName проверяет имя сервера.
func ValidName(n string) bool { return nameRe.MatchString(n) && n != Local }

// ValidAddress проверяет адрес: IP или имя хоста. Адреса 0.0.0.0, loopback и пустой не принимаются.
func ValidAddress(a string) error {
	if ip, err := netip.ParseAddr(a); err == nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return fmt.Errorf("address %s is not usable from another server", a)
		}
		return nil
	}
	if len(a) > 253 || !hostRe.MatchString(a) {
		return errors.New("address: an IP or a host name")
	}
	return nil
}

// Load читает реестр: имя → адрес. Нет файла: пустой реестр.
func Load(f host.FS) (map[string]string, error) {
	b, err := f.ReadFile(Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var j struct {
		Servers map[string]string `json:"servers"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("the server registry %s is corrupt: %w", Path, err)
	}
	if j.Servers == nil {
		j.Servers = map[string]string{}
	}
	return j.Servers, nil
}

// Save записывает реестр.
func Save(f host.FS, m map[string]string) error {
	b, _ := json.MarshalIndent(map[string]any{"servers": m}, "", "  ")
	if err := f.MkdirAll("/etc/lite-deploy", 0o755); err != nil {
		return err
	}
	return f.WriteFile(Path, append(b, '\n'), 0o644)
}

// Names имена по алфавиту.
func Names(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
