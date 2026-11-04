package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

// CodeInvalidSecret недопустимое имя или значение секрета.
const CodeInvalidSecret = "invalid_secret"

func (e Env) keyPath() string {
	if e.KeyPath != "" {
		return e.KeyPath
	}
	return secrets.DefaultKeyPath
}

func (e Env) pubPath() string {
	if e.PubPath != "" {
		return e.PubPath
	}
	return secrets.DefaultPubPath
}

func (e Env) secretsDir() string {
	if e.SecretsDir != "" {
		return e.SecretsDir
	}
	return secrets.DefaultStoreDir
}

func (e Env) root() bool { return e.IsRoot != nil && e.IsRoot() }

// readSecretValue читает значение секрета: скрыто с терминала или со стандартного ввода.
// Значение никогда не берётся из аргументов или переменных окружения (SPEC-v0.md, 6.1).
func readSecretValue(env Env) ([]byte, error) {
	if f, ok := env.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) { //nolint:gosec // дескриптор терминала помещается в int
		_, _ = fmt.Fprint(env.Stderr, "Значение секрета (ввод скрыт): ")
		b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // см. выше
		_, _ = fmt.Fprintln(env.Stderr)
		return b, err
	}
	b, err := io.ReadAll(io.LimitReader(env.Stdin, secrets.MaxValueBytes+3))
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
		if len(b) > 0 && b[len(b)-1] == '\r' {
			b = b[:len(b)-1]
		}
	}
	return b, nil
}

func cmdInit(env Env) result {
	if !env.root() {
		return fail("init", "permission_denied", "init выполняется от root: sudo lite-deploy init")
	}
	pub, created, err := secrets.EnsureKey(env.FS, env.keyPath(), env.pubPath())
	if err != nil {
		return fail("init", plan.CodePrecondition, err.Error())
	}
	for _, d := range []struct {
		path string
		perm uint32
	}{
		{"/etc/lite-deploy/projects", 0o755},
		{env.Paths.StateDir + "/projects", 0o755},
		{env.secretsDir(), 0o700},
	} {
		if err := env.FS.MkdirAll(d.path, os.FileMode(d.perm)); err != nil {
			return fail("init", CodeIO, "не удалось создать "+d.path+": "+err.Error())
		}
	}
	text := "Ключ исполнителя уже был создан, он не изменён.\n"
	if created {
		text = "Ключ исполнителя создан: " + env.keyPath() + " (права 600).\n"
	}
	text += "Открытый ключ (его можно показывать): " + pub + "\n"
	return result{command: "init", data: map[string]any{"public_key": pub, "created": created}, text: text}
}

func cmdSecret(args []string, env Env) result {
	if len(args) == 0 {
		return fail("secret", CodeUsage, "использование: lite-deploy secret pubkey|encrypt|set|list")
	}
	switch args[0] {
	case "pubkey":
		return secretPubkey(env)
	case "encrypt":
		return secretEncrypt(args[1:], env)
	case "set":
		return secretSet(args[1:], env)
	case "list":
		return secretList(args[1:], env)
	}
	return fail("secret", CodeUsage, "неизвестная подкоманда secret: "+args[0])
}

func secretPubkey(env Env) result {
	b, err := env.FS.ReadFile(env.pubPath())
	if err != nil {
		return fail("secret.pubkey", plan.CodePrecondition, "открытый ключ не найден в "+env.pubPath()+": на сервере выполните sudo lite-deploy init")
	}
	pub := strings.TrimSpace(string(b))
	return result{command: "secret.pubkey", data: map[string]any{"public_key": pub}, text: pub + "\n"}
}

func secretErr(command string, err error) result {
	switch {
	case errors.Is(err, secrets.ErrInvalid):
		return fail(command, CodeInvalidSecret, err.Error())
	default:
		return fail(command, CodeIO, err.Error())
	}
}

func secretEncrypt(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.encrypt", flag.ContinueOnError)
	pubFlag := fs.String("pubkey", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail("secret.encrypt", CodeUsage, "использование: lite-deploy secret encrypt <проект> <имя> [--pubkey age1…]; значение читается со стандартного ввода или скрыто с терминала")
	}
	pub := strings.TrimSpace(*pubFlag)
	if pub == "" {
		b, err := env.FS.ReadFile(env.pubPath())
		if err != nil {
			return fail("secret.encrypt", plan.CodePrecondition, "нет открытого ключа исполнителя: задайте --pubkey или выполните на сервере sudo lite-deploy init")
		}
		pub = string(b)
	}
	val, err := readSecretValue(env)
	if err != nil {
		return fail("secret.encrypt", CodeIO, "не удалось прочитать значение: "+err.Error())
	}
	ct, err := secrets.Encrypt(pub, pos[0], pos[1], val, env.Now())
	if err != nil {
		return secretErr("secret.encrypt", err)
	}
	return result{command: "secret.encrypt", data: map[string]any{"project": pos[0], "name": pos[1], "ciphertext": ct}, text: ct + "\n"}
}

func secretSet(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.set", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail("secret.set", CodeUsage, "использование: lite-deploy secret set <проект> <имя>; значение читается со стандартного ввода или скрыто с терминала")
	}
	if !env.root() {
		return fail("secret.set", "permission_denied", "secret set выполняется от root: хранилище секретов доступно только root")
	}
	val, err := readSecretValue(env)
	if err != nil {
		return fail("secret.set", CodeIO, "не удалось прочитать значение: "+err.Error())
	}
	changed, err := secrets.NewStoreAt(env.FS, env.secretsDir()).Put(pos[0], pos[1], val)
	if err != nil {
		return secretErr("secret.set", err)
	}
	word := "не изменился"
	if changed {
		word = "записан"
	}
	// значение и шифртекст не печатаются
	return result{command: "secret.set", data: map[string]any{"project": pos[0], "name": pos[1], "changed": changed},
		text: fmt.Sprintf("Секрет %s проекта %s %s.\n", pos[1], pos[0], word)}
}

func secretList(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.list", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("secret.list", CodeUsage, "использование: lite-deploy secret list <проект>")
	}
	type item struct {
		Name      string  `json:"name"`
		UpdatedAt *string `json:"updated_at"`
	}
	items := []item{}
	if entries, err := secrets.NewStoreAt(env.FS, env.secretsDir()).List(pos[0]); err == nil {
		for _, e := range entries {
			ts := e.UpdatedAt.Format(time.RFC3339)
			items = append(items, item{e.Name, &ts})
		}
	} else if errors.Is(err, secrets.ErrInvalid) {
		return fail("secret.list", CodeInvalidSecret, err.Error())
	} else {
		// без прав root каталог секретов недоступен: имена берём из несекретного состояния проекта (D-49), времени там нет
		st, serr := plan.LoadState(env.FS, env.Paths)
		if serr != nil {
			return fail("secret.list", plan.CodePrecondition, serr.Error())
		}
		ps, ok := st.Projects[pos[0]]
		if !ok {
			return fail("secret.list", CodeProjectMissing, "проект "+pos[0]+" не найден")
		}
		for _, n := range ps.Secrets {
			items = append(items, item{n, nil})
		}
	}
	var sb strings.Builder
	for _, it := range items {
		when := "время неизвестно"
		if it.UpdatedAt != nil {
			when = *it.UpdatedAt
		}
		fmt.Fprintf(&sb, "%s\t%s\n", it.Name, when)
	}
	if len(items) == 0 {
		sb.WriteString("Секретов нет.\n")
	}
	return result{command: "secret.list", data: map[string]any{"secrets": items}, text: sb.String()}
}
