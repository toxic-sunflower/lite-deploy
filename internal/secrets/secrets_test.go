package secrets

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func keys(t *testing.T) (sec, pub string) {
	t.Helper()
	s, p, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestRoundTrip(t *testing.T) {
	sec, pub := keys(t)
	for _, v := range []string{"p@ss w0rd", "значение с юникодом", `quote"back\slash`, strings.Repeat("x", MaxValueBytes), "a\nb"} {
		ct, err := Encrypt(pub, "shop", "api_key", []byte(v), now)
		if err != nil {
			t.Fatalf("%.20q: %v", v, err)
		}
		got, err := Decrypt(sec, ct, "shop", "api_key")
		if err != nil || string(got) != v {
			t.Fatalf("%.20q: %q %v", v, got, err)
		}
	}
}

func TestCiphertextShapeAndNoPlaintext(t *testing.T) {
	sec, pub := keys(t)
	ct, _ := Encrypt(pub, "shop", "api_key", []byte("SUPER-SECRET-VALUE"), now)
	if !regexp.MustCompile(ArmorPattern).MatchString(ct) {
		t.Fatalf("форма шифртекста:\n%s", ct)
	}
	for _, leak := range []string{"SUPER-SECRET-VALUE", "shop", "api_key"} {
		if strings.Contains(ct, leak) {
			t.Errorf("шифртекст содержит %q", leak)
		}
	}
	ct2, _ := Encrypt(pub, "shop", "api_key", []byte("SUPER-SECRET-VALUE"), now)
	if ct == ct2 {
		t.Error("два шифрования одного значения должны различаться")
	}
	_ = sec
}

func TestBindingRejected(t *testing.T) {
	sec, pub := keys(t)
	ct, _ := Encrypt(pub, "shop", "api_key", []byte("v1"), now)
	if _, err := Decrypt(sec, ct, "other", "api_key"); !errors.Is(err, ErrBinding) {
		t.Errorf("другой проект: %v", err)
	}
	if _, err := Decrypt(sec, ct, "shop", "other_key"); !errors.Is(err, ErrBinding) {
		t.Errorf("другое имя: %v", err)
	}
}

func TestDecryptFailures(t *testing.T) {
	sec, pub := keys(t)
	otherSec, _ := keys(t)
	ct, _ := Encrypt(pub, "shop", "api_key", []byte("v1"), now)
	lines := strings.Split(ct, "\n")
	flipped := strings.Join(append(append([]string{}, lines[:2]...), append([]string{swapFirst(lines[2])}, lines[3:]...)...), "\n")
	cases := map[string]struct{ key, ct string }{
		"чужой ключ":        {otherSec, ct},
		"усечённый":         {sec, ct[:len(ct)/2]},
		"повреждённый байт": {sec, flipped},
		"пустой":            {sec, ""},
		"не age":            {sec, "-----BEGIN AGE ENCRYPTED FILE-----\nAAAAAAAAAAAAAAAAAAAAAAAA\n-----END AGE ENCRYPTED FILE-----"},
		"мусор":             {sec, "hello"},
		"плохой закрытый":   {"AGE-SECRET-KEY-1BAD", ct},
		"слишком длинный":   {sec, strings.Repeat("A", MaxCiphertextSize+1)},
	}
	for name, c := range cases {
		if _, err := Decrypt(c.key, c.ct, "shop", "api_key"); err == nil {
			t.Errorf("%s: ожидали ошибку", name)
		}
	}
}

func swapFirst(s string) string {
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

func TestEncryptValidation(t *testing.T) {
	_, pub := keys(t)
	bad := []struct{ project, name, value, pub string }{
		{"Shop", "ok_name", "v", pub},
		{"shop", "Bad", "v", pub},
		{"shop", "ok_name", "", pub},
		{"shop", "ok_name", "a\x00b", pub},
		{"shop", "ok_name", strings.Repeat("x", MaxValueBytes+1), pub},
		{"shop", "ok_name", "\xff\xfe", pub},
		{"shop", "ok_name", "v", "age1notakey"},
		{"shop", "ok_name", "v", ""},
		{"../x", "ok_name", "v", pub},
	}
	for _, c := range bad {
		if _, err := Encrypt(c.pub, c.project, c.name, []byte(c.value), now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: ожидали ErrInvalid, получили %v", c, err)
		}
	}
	if _, err := Encrypt(pub, "shop", "_db_password", []byte("v"), now); err != nil {
		t.Errorf("служебное имя разрешено: %v", err)
	}
}

func TestMaxValueFitsCiphertextLimit(t *testing.T) {
	_, pub := keys(t)
	// худший случай по размеру: 8 КиБ значения из символов, которые JSON экранирует шестью байтами
	v := strings.Repeat("\x01", MaxValueBytes)
	if ct, err := Encrypt(pub, "shop", "api_key", []byte(v), now); err == nil && len(ct) > MaxCiphertextSize {
		t.Fatalf("шифртекст %d байт больше предела", len(ct))
	}
}

func TestStore(t *testing.T) {
	m := host.NewMemFS()
	tick := now
	m.Now = func() time.Time { tick = tick.Add(time.Minute); return tick }
	s := &Store{FS: m, Dir: "/var/lib/lite-deploy/secrets"}
	if changed, err := s.Put("shop", "api_key", []byte("v1")); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if m.Perms["/var/lib/lite-deploy/secrets/shop/api_key"] != 0o600 || m.Dirs["/var/lib/lite-deploy/secrets"] != 0o700 || m.Dirs["/var/lib/lite-deploy/secrets/shop"] != 0o700 {
		t.Errorf("права: %v %v", m.Perms, m.Dirs)
	}
	if changed, _ := s.Put("shop", "api_key", []byte("v1")); changed {
		t.Error("то же значение: unchanged")
	}
	if changed, _ := s.Put("shop", "api_key", []byte("v2")); !changed {
		t.Error("новое значение: changed")
	}
	_, _ = s.Put("shop", "_db_password", []byte("x"))
	got, err := s.Get("shop", "api_key")
	if err != nil || string(got) != "v2" {
		t.Fatal(string(got), err)
	}
	if _, err := s.Get("shop", "none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	list, err := s.List("shop")
	if err != nil || len(list) != 2 || list[0].Name != "_db_password" || list[1].Name != "api_key" || !list[0].UpdatedAt.After(list[1].UpdatedAt) {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := s.List("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	for _, bad := range [][2]string{{"../x", "a_b"}, {"shop", "../x"}, {"shop", "A"}, {"Shop", "ok_name"}} {
		if _, err := s.Put(bad[0], bad[1], []byte("v")); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if _, err := s.Put("shop", "ok_name", nil); !errors.Is(err, ErrInvalid) {
		t.Error("пустое значение")
	}
}

func TestEnsureKey(t *testing.T) {
	m := host.NewMemFS()
	pub, created, err := EnsureKey(m, DefaultKeyPath, DefaultPubPath)
	if err != nil || !created || !strings.HasPrefix(pub, "age1") {
		t.Fatal(pub, created, err)
	}
	if m.Perms[DefaultKeyPath] != 0o600 || m.Perms[DefaultPubPath] != 0o644 || m.Dirs["/etc/lite-deploy"] != 0o755 {
		t.Errorf("права: %v %v", m.Perms, m.Dirs)
	}
	before := string(m.Files[DefaultKeyPath])
	pub2, created2, err := EnsureKey(m, DefaultKeyPath, DefaultPubPath)
	if err != nil || created2 || pub2 != pub || string(m.Files[DefaultKeyPath]) != before {
		t.Fatal("повторный вызов не должен менять ключ")
	}
	// открытый ключ восстанавливается из закрытого, закрытый не трогается
	delete(m.Files, DefaultPubPath)
	if p, c, err := EnsureKey(m, DefaultKeyPath, DefaultPubPath); err != nil || c || p != pub || strings.TrimSpace(string(m.Files[DefaultPubPath])) != pub {
		t.Fatal("восстановление открытого ключа")
	}
	// повреждённый закрытый ключ не перезаписывается
	m.Files[DefaultKeyPath] = []byte("garbage")
	if _, _, err := EnsureKey(m, DefaultKeyPath, DefaultPubPath); err == nil || string(m.Files[DefaultKeyPath]) != "garbage" {
		t.Fatal("повреждённый ключ должен давать ошибку и не затираться")
	}
	// шифрование под созданным ключом работает
	m2 := host.NewMemFS()
	pub3, _, _ := EnsureKey(m2, DefaultKeyPath, DefaultPubPath)
	ct, _ := Encrypt(pub3, "shop", "api_key", []byte("v"), now)
	if v, err := Decrypt(string(m2.Files[DefaultKeyPath]), ct, "shop", "api_key"); err != nil || string(v) != "v" {
		t.Fatal(v, err)
	}
}
