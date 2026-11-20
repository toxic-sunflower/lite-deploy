# Testing on a real project

These scenarios are for a real server with a real project (no spare machine needed). Each one says what to do, what you must see, and what to send back if it fails. Work top to bottom; a failed step stops that scenario, not the whole run.

Where a command runs is written in front of it: **server** (as your administrator, no root) or **workstation** (your own machine, WSL or PowerShell).

If something fails, send: the exact text you saw, and from the **server** `sudo journalctl -u lite-deploy-agent -n 50 --no-pager`.

## 0. Start

**server**

```sh
lite version
lite verify
```

You see the version and `errors 0`.

**workstation**

```sh
lite-ui
```

The page opens, your server is in the list, **Overview** shows “All good” or “Mostly fine” with plain sentences (no commands).

## 1. Your project from the form

1. In the page: **Projects**, **New project**. Fill in the name, the image (without the version), the port the application listens on, the domain; if the registry is private tick the box and paste the read-only token.
2. Press **Show what will happen**. You see a table of steps and the text “Nothing has changed on the server yet”.
3. Press **Create project**. All steps finish; no step is “failed”.
4. On the project card paste the full commit hash of an image that exists in your registry and press **Deploy**.

Expected: “Deployed … Traffic switched”, the slot shows “running, version …”. Open your domain through the way you use today.

5. Press **Roll back**, then deploy the version again.

Expected: traffic goes to the previous version and back, with no moment when the site is down.

Repeat the creation once more with the same form and **Create project**: every step must say `unchanged`.

## 1b. Secrets, export and import

1. On the project card press **Secrets**. Expected: the names the project needs, each “set” or “missing”; no value is ever shown.
2. Type a new value for one secret and **Save**, then deploy the same version again. Expected: the application uses the new value.
3. **Export (no secrets)**. Expected: a file `NAME.lite-project.json` is downloaded; open it: it holds no password, token or key, only names like `secret://api_key`.
4. **New project**, “Start from an exported project file”, pick that file. Expected: the form is filled in; secret values and the registry token are empty and the page asks for them. (Do not create a second copy of your live project: close the form.)

## 2. Publishing (Ingress)

Do this on a domain you can afford to touch for a few minutes.

1. **Ingress**: your domain is listed; “How visitors reach it” shows the way you use now.
2. If you use a Cloudflare Tunnel: **Server**, add **cloudflare**, then in **Ingress** paste the tunnel token and press Connect. Expected: “is connected and running”; in the Cloudflare dashboard the tunnel is “Healthy”.
3. Switch the domain to “Directly”, open the web ports when the page offers it, point the DNS record at the server. Expected: within a minute `https://your-domain/` answers with a valid certificate.
4. Switch it back and restore the DNS record. Expected: the site works the way it did before.

## 3. Backups

1. **workstation**: `age-keygen -o lite-backup.key`. Keep the file off the server. Copy the `age1…` line.
2. In the page: **Backups**, “Create my key and turn on backups”. A file `lite-backup.key` is downloaded: keep it. Then “Back up now”. (On the **server** the same is `lite backup setup age1… --encrypt-dumps`, `lite backup schedule on`, `lite backup run`, `lite backup list`.)

Expected: a backup file is listed; the file ends with `.age`.

3. If you use Backblaze, S3 or SFTP: set the storage up (see the README), run `lite backup run` again. Expected: the last upload time appears in `lite verify` without warnings.
4. Restore into a throw-away database, never over the live one: `lite pg restore LIVE_DB FILE.dump --as LIVE_DB_test` (the target database must exist). Expected: “Database … restored”; then drop the test database.

4. In **Databases**: your database is listed, “Back up now” makes a copy that appears under Backups. Do not press Restore on the live database during a test.

## 3b. Confirm changes with your key (optional, try it last)

1. **Protection**, “Confirm with my key”, **Turn on**. Expected: “Done”.
2. Open a port from **Protection**: it works from this page.
3. **server**: `lite firewall allow 8080/tcp` is refused (owner_confirmation_required). Deploy and rollback from the page or CI still work.
4. Turn it off again from the page. If it does not turn off, on the **server** as root: `sudo rm /etc/lite-deploy/owner-keys`.

## 4. Updating lite (the Update button)

This needs a release newer than the one on the server. Tell me and I publish a throw-away patch release.

1. **Overview** shows “A new version of lite is available”.
2. Press **Update**. Expected: the page waits about a minute, then says “Updated to …”. (After the update the settings that lite manages are brought up to date by themselves.) `lite version` on the **server** shows the new version.
3. `lite verify` still has no errors, and the project still runs.

## 4b. Traffic switch (after the update to this version)

1. **server**: `systemctl is-active lite-deploy-switch.socket` says `active`; `ls -l /etc/caddy/upstreams/` shows the files owned by root; `ls /etc/sudoers.d/` has no `lite-deploy-*` files.
2. Deploy a version from CI (or `lite deploy`) as before. Expected: traffic switches, no errors. **server** `sudo journalctl -u 'lite-deploy-switch@*' -n 20` shows the switch.
3. Roll back. Expected: traffic goes back.
4. If a deploy from CI fails with a message about switching traffic, send it to me with the journal lines above.

## 5. Protection and access

1. **server**: `lite ssh-key list`, then `lite ssh-key add "ssh-ed25519 AAAA… test"` with a second key, log in with the second key from a new terminal, then `lite ssh-key remove FINGERPRINT` for it. Expected: login keeps working at every step; removing your only key is refused.
2. **Overview**: if it offers “Allow only …”, press it. Expected: you can still log in; another system user cannot.
3. Close the laptop lid (or drop the network) for five minutes, open the page again. Expected: it reconnects; no “Connection refused”.

## 6. Windows and macOS

**PowerShell**: `irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex`, open a new window, `lite-ui`. **macOS**: `curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh`, `lite-ui`. Expected: the same pages as above, no errors about `ssh`.

## 6b. Claude Desktop (optional)

Install `lite-deploy-windows-amd64.mcpb` from the Releases page (Settings, Extensions, Install Extension), type `user@address`. Ask “Is my server fine?”. Expected: Claude calls `verify` and answers in plain words. If it says it cannot connect, run `ssh user@address` in PowerShell: it must log in with no password question.

## 7. Clean-up (only if you want to start over)

**server**: `sudo liteup uninstall --everything`. Expected: the server is bare, `ssh` login still works, and `curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh` followed by `sudo liteup install` works again.
