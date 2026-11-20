# Describe the server in a file

Everything `lite init` asks you can also come from a file. The same file can be **saved from a running server** or **written by hand**, so a new server is set up the same way every time.

## Save it from a server you already have

In the interface: **Server**, “Save configuration”. Or on the server:

```sh
lite config export > server.lite-config.json
```

The file holds only public things: the administrators and their **public** SSH keys, the firewall mode, the installed programs, whether protection is on, and the **public** backup key. There is no password and no secret in it, so it is safe to keep in git.

## Use it on a new server

Copy the file to the new server, then, as root:

```sh
lite init --config server.lite-config.json
```

`init` shows what it will do and asks for confirmation as usual. It creates the users with the keys from the file (it does not ask for them again), sets the firewall, installs the programs, turns protection on, and sets up backups with the public key. A flag on the command line wins over the file: `--ufw http` overrides `"firewall"`.

The projects are not in this file: add them from their own descriptions ([Your first project](first-project.md)), and bring their secrets with the interface or from a backup.

## Write it by hand

```json
{
  "schema": "lite-deploy/server/v1",
  "users": [
    { "name": "alice", "ssh_keys": ["ssh-ed25519 AAAA… alice@laptop"] }
  ],
  "firewall": "max",
  "plugins": ["caddy", "podman", "pg"],
  "security": true,
  "backup": { "recipient": "age1…", "encrypt_dumps": true, "schedule": true }
}
```

| Field | Meaning |
| --- | --- |
| `users` | Administrators. Each has a name and the public SSH keys that may log in |
| `firewall` | `max` (only SSH), `http` (SSH, 80 and 443), `off`, or `keep` (leave it as it is) |
| `plugins` | `caddy`, `podman`, `pg`, `redis`, `cloudflare`, `backblaze`, `s3`, `sftp` |
| `security` | Protection (SSH by key only, protection against password guessing, automatic updates). On unless you write `false` |
| `backup` | The **public** age key (`age1…`), whether database copies are encrypted, whether the daily schedule is on |

A mistake is reported with a reason before anything changes, for example “backup.recipient must be a public age key”.
