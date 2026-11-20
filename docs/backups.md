# Backups and restore

`lite` backs up three things: your **databases**, the **settings and secrets** of the projects, and sends them to storage **outside the server**. The settings and secrets are always encrypted with a key that only you have, so losing the server does not lose the key, and a leaked bucket does not reveal your secrets.

Set this up while you have no data yet. It takes a few minutes.

## 1. Make a backup key

## The short way: in the interface

Open `lite-ui`, then **Backups**, and press **Create my key and turn on backups**. The page makes the key on your computer and downloads the file `lite-backup.key`: **keep it safe and off the server**, because without it backups cannot be opened and nobody can recover it for you. It then gives the server the public part, turns on encrypted backups with a daily schedule, and **Back up now** takes the first one. Below, **Copies outside this server** has forms for Backblaze, S3 and another server (SFTP). **Databases** lists your databases with *Back up now* and *Restore*.

The rest of this page does the same with commands, and explains what happens.

On **your computer** (not the server):

```sh
age-keygen -o lite-backup.key
```

The output includes a public key that starts with `age1…`. Keep `lite-backup.key` **off the server**: a password manager and a second medium. Without it the backups cannot be decrypted, and nobody can recover it for you.

## 2. Give the server the public key

```sh
lite backup setup age1yourPublicKey... --encrypt-dumps --yes
```

The server now holds only the public part. `--encrypt-dumps` also encrypts database dumps (recommended).

## 3. Take a backup and schedule it

```sh
lite backup run
lite backup list
lite backup schedule on --yes       # every day around 03:00
```

`backup run` creates:

- a dump of every database of `lite` projects (`/var/backups/lite-deploy/pg`, the latest seven are kept per database);
- one archive with the project descriptions and secrets, encrypted (`/var/backups/lite-deploy/config`, the latest fourteen are kept);
- and uploads all of it to every storage you configured (next step).

`lite verify` warns if a database has no backup and reports an error if the newest is older than 36 hours.

## 4. A storage outside the server

A backup on the same disk is not a backup. Configure at least one location. Keys are given on standard input, never as arguments.

**Backblaze B2:**

```sh
lite backblaze install --yes
printf '%s\n%s\n' KEY_ID APPLICATION_KEY | lite backblaze setup my-bucket lite/shop --yes
lite backblaze schedule on --yes
```

**S3-compatible (AWS, Wasabi, MinIO, Cloudflare R2…):**

```sh
printf '%s\n%s\n' ACCESS_KEY SECRET_KEY | lite s3 setup https://s3.example.com my-bucket lite/shop --yes
```

**SFTP** (the host key is mandatory):

```sh
{ cat ~/.ssh/id_ed25519; echo ---; ssh-keyscan -t ed25519 backup.example.com; } | lite sftp setup backup.example.com backup /srv/backups/shop --yes
```

Each location supports `status`, `upload`, `list`, `fetch` and `schedule` (for example `lite s3 status`). Uploads only add files: the bucket's own lifecycle rules decide how long they live.

## 5. Test a restore (do this once)

A plain `lite pg restore shop FILE` writes into the database `shop` and **replaces its current data**, so do not test that way on a live database. Restore into a separate, empty database with `--as` instead, and look at the data:

```sh
lite pg backups                                                    # pick a file for shop
lite pg create-db shop_restore_test --yes
cat lite-backup.key | lite pg restore shop shop-20261119T030000Z.dump.age --as shop_restore_test --identity-stdin --yes
sudo -u postgres psql -d shop_restore_test -c '\dt'                 # look at it
lite pg drop-db shop_restore_test --yes                            # clean up
```

(Leave out `--identity-stdin` and the `cat …|` when dumps are not encrypted.) A backup that was never restored is a hope, not a backup. For a full rehearsal of losing the server, use a spare virtual server and follow "Lost the server" below.

## Lost the server: restore on a new one

1. Install `lite` and prepare the new server (the [Quickstart](quickstart.md), including the plugins you used).
2. Configure the same storage (new keys are fine): for example `lite backblaze setup …`.
3. Download and unpack the settings and secrets:

   ```sh
   lite backblaze fetch config
   cat lite-backup.key | lite backup restore config-DATE.tar.gz.age --identity-stdin --yes
   ```

4. Download and restore the databases:

   ```sh
   lite backblaze fetch pg
   lite pg create-db shop --yes
   cat lite-backup.key | lite pg restore shop shop-DATE.dump.age --identity-stdin --yes
   ```

5. Deploy each project again with `lite deploy` (the settings are back, so the project exists).

The executor key and the storage keys are not part of the backup: they are issued again on the new server.
