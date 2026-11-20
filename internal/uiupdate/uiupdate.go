// Package uiupdate checks whether a newer lite-ui exists and replaces the running program with it. It is used by the
// interface (the page offers the update and does it on a button), never by the server side.
package uiupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/minisign"
)

// Info is what the page shows.
type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
}

// Updater talks to the release host. The zero value is not usable: use New.
type Updater struct {
	// Current is the running version (a release tag such as v0.12.1; a development build is never updated).
	Current string
	// APIURL is the address of the "latest release" document; DownloadBase is the folder that holds the files of a tag.
	APIURL, DownloadBase string
	GOOS, GOARCH         string
	// Exe is the program to replace.
	Exe    string
	Client *http.Client
	// PubKey is the minisign public key the releases are signed with (the line of minisign.pub). The signature of
	// SHA256SUMS is always required: a release without a valid one is never installed.
	PubKey string

	mu     sync.Mutex
	cached Info
	at     time.Time
}

const repo = "toxic-sunflower/lite-deploy"

// ReleaseKey is minisign.pub of the repository: the key the releases are signed with.
const ReleaseKey = "RWQXkl8IozvEWfo230NRtIvfuO4KI+SiT2s0p+Qynna/9WSATC/JDcyJ"

// New makes an Updater for the running program.
func New(current string) *Updater {
	exe, _ := os.Executable()
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return &Updater{
		Current: current, APIURL: "https://api.github.com/repos/" + repo + "/releases/latest",
		DownloadBase: "https://github.com/" + repo + "/releases/download",
		GOOS:         runtime.GOOS, GOARCH: runtime.GOARCH, Exe: exe, PubKey: ReleaseKey,
		Client: &http.Client{Timeout: 5 * time.Minute},
	}
}

var verRe = regexp.MustCompile(`\Av?(\d+)\.(\d+)\.(\d+)\z`)

// Parse reads a release version such as v0.12.1 (a development build does not parse).
func Parse(v string) ([3]int, bool) { return parse(v) }

func parse(v string) ([3]int, bool) {
	m := verRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// Newer reports whether latest is a higher release than current. A version that is not a plain release (a development
// build) is never offered an update.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Check asks for the newest release; the answer is kept for ten minutes so that opening pages does not hit the host often.
func (u *Updater) Check(ctx context.Context) (Info, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.at.IsZero() && time.Since(u.at) < 10*time.Minute {
		return u.cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.APIURL, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lite-ui")
	resp, err := u.Client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("the release host answered %d", resp.StatusCode)
	}
	var doc struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return Info{}, err
	}
	u.cached = Info{Current: u.Current, Latest: doc.Tag, Available: Newer(u.Current, doc.Tag)}
	u.at = time.Now()
	return u.cached, nil
}

var tagRe = regexp.MustCompile(`\Av\d+\.\d+\.\d+\z`)

func (u *Updater) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lite-ui")
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: the release host answered %d", filepath.Base(url), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("the downloaded file is too large")
	}
	return b, nil
}

// Apply downloads the newest release for this system, checks its SHA256 against the SHA256SUMS of the same release
// and replaces the program. The new version runs after a restart.
func (u *Updater) Apply(ctx context.Context) (string, error) {
	info, err := u.Check(ctx)
	if err != nil {
		return "", err
	}
	if !info.Available {
		return "", errors.New("lite-ui is already up to date")
	}
	if !tagRe.MatchString(info.Latest) {
		return "", errors.New("unexpected version name from the release host")
	}
	name := fmt.Sprintf("lite-ui-%s-%s", u.GOOS, u.GOARCH)
	if u.GOOS == "windows" {
		name += ".exe"
	}
	base := u.DownloadBase + "/" + info.Latest + "/"
	sums, err := u.get(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return "", err
	}
	pk, err := minisign.ParsePublicKey(u.PubKey)
	if err != nil {
		return "", errors.New("this lite-ui has no valid key to check releases with")
	}
	sig, err := u.get(ctx, base+"SHA256SUMS.minisig", 64<<10)
	if err != nil {
		return "", errors.New("the release has no signature, so it was not installed: " + err.Error())
	}
	if err := pk.Verify(sums, sig); err != nil {
		return "", errors.New("the signature of the release is not valid, nothing was changed (" + err.Error() + ")")
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("the release %s has no program for this system (%s)", info.Latest, name)
	}
	bin, err := u.get(ctx, base+name, 200<<20)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != want {
		return "", errors.New("the downloaded program does not match the checksum of the release; nothing was changed")
	}
	if err := replace(u.Exe, bin, u.GOOS == "windows"); err != nil {
		return "", err
	}
	return info.Latest, nil
}

// replace puts data in place of the program at path. A running program can be renamed on every system, but only on
// Unix can it be overwritten by a rename, so Windows moves the old file aside first.
func replace(path string, data []byte, windows bool) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil { //nolint:gosec // a program must be executable
		return fmt.Errorf("cannot write next to %s: %w", path, err)
	}
	if windows {
		old := path + ".old"
		_ = os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Cleanup removes the leftover of a previous update (Windows keeps the old program until the next start).
func (u *Updater) Cleanup() {
	if u.Exe != "" {
		_ = os.Remove(u.Exe + ".old")
	}
}
