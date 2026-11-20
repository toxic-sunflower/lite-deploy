# A database on another server

By default a project's PostgreSQL runs on the same server and is reached over a unix socket. When the database should live on a different server, you describe it in the project and open access on the database server. This tutorial uses two servers:

- `app1` runs the project `shop` (the address on your internal network is `10.0.0.5`);
- `db1` runs PostgreSQL (`10.0.0.2`).

Both have `lite` installed (see the [Quickstart](quickstart.md)). `db1` needs the `pg` plugin (`sudo lite init --user alice --plugins pg`).

Use a private network between the servers. The database port is opened only for the one address you name, but it should not cross the public internet.

![The Databases page.](img/databases.png)

## 1. Register the database server on `app1`

```sh
lite server add db1 10.0.0.2 --yes
lite server list
```

The registry maps a name to an address on the internal network.

## 2. Create the database and the role on `db1`

Pick a password for the role (16 to 128 characters from `A-Za-z0-9_.~-`) and give it on standard input, so it does not land in shell history:

```sh
echo 'a-long-random-password' | lite pg create-db shop shop --password-stdin --yes
```

The role and the database are named after the project; hyphens in the project name become underscores.

## 3. Open access on `db1`

```sh
lite pg listen 10.0.0.2 --yes                      # also listen on this server's internal address
lite pg allow shop shop 10.0.0.5 --yes             # database shop, role shop, only from app1
lite pg access                                     # who has access now
```

`allow` edits a separate block of `pg_hba.conf` (an error in the file restores the previous content), requires `ssl` and a password, and opens port `5432` in the firewall **only for `10.0.0.5`**. Addresses like `0.0.0.0/0` are refused. `PostgreSQL` is restarted by `listen`.

## 4. Point the project at it, on `app1`

```json
"database": {
  "engine": "postgres",
  "server": "db1",
  "port": 5432,
  "password_secret": "secret://db_password",
  "connection_format": "url"
}
```

Encrypt the same password as a secret and create the project:

```sh
echo 'a-long-random-password' | lite secret encrypt shop db_password > ct_db_password
lite project create shop.json --ciphertext db_password=ct_db_password
```

The service receives `DATABASE_URL` pointing at `db1`. The connection is always encrypted (`sslmode=require`). A project with a remote database does not need PostgreSQL installed on `app1`.

## Change the password later

On `db1`: `echo 'new-password' | lite pg password shop --password-stdin --yes`. On `app1` encrypt the new value again and run `lite project create` again, then deploy so the service restarts with it.

## Remove access

```sh
lite pg deny shop shop 10.0.0.5 --yes
```

removes the rule, and closes the port again if nothing else is allowed from that address.
