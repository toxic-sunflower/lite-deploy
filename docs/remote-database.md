# A database on another server

By default a project's PostgreSQL runs on the same server and is reached over a unix socket. When the database should live on a different server, you give the project its connection as a secret and open access on the database server. This tutorial uses two servers:

- `app1` runs the project `shop` (the address on your internal network is `10.0.0.5`);
- `db1` runs PostgreSQL (`10.0.0.2`).

Both have `lite` installed (see the [Quickstart](quickstart.md)). `db1` needs the `pg` plugin (`sudo lite init --user alice --plugins pg`).

Use a private network between the servers. The database port is opened only for the one address you name, but it should not cross the public internet.

![The Databases page.](img/databases.png)

## 1. Create the database and the role on `db1`

Pick a password for the role (16 to 128 characters from `A-Za-z0-9_.~-`) and give it on standard input, so it does not land in shell history:

```sh
echo 'a-long-random-password' | lite pg create-db shop shop --password-stdin --yes
```

The role and the database are named after the project; hyphens in the project name become underscores.

## 2. Open access on `db1`

```sh
lite pg listen 10.0.0.2 --yes                      # also listen on this server's internal address
lite pg allow shop shop 10.0.0.5 --yes             # database shop, role shop, only from app1
lite pg access                                     # who has access now
```

`allow` edits a separate block of `pg_hba.conf` (an error in the file restores the previous content), requires `ssl` and a password, and opens port `5432` in the firewall **only for `10.0.0.5`**. Addresses like `0.0.0.0/0` are refused. `PostgreSQL` is restarted by `listen`.

## 3. Point the project at it, on `app1`

A database on another server is just a secret of the project: the connection string. Put it in the form of the project (a secret setting `DATABASE_URL=...`), or in the description:

```json
"secrets": { "DATABASE_URL": "secret://database_url" }
```

and encrypt the connection string as that secret (the password is the one you gave `lite pg create-db` on `db1`):

```sh
echo 'postgresql://shop:a-long-random-password@10.0.0.2:5432/shop?sslmode=require' | lite secret encrypt shop database_url > ct_database_url
lite project create shop.json --ciphertext database_url=ct_database_url
```

The component receives `DATABASE_URL` pointing at `db1` (write another notation if the application needs one). Keep `sslmode=require` so that the connection is encrypted. Nothing about this database is known to `lite` on `app1`: it is a secret like any other, so PostgreSQL does not have to be installed there.

## Change the password later

On `db1`: `echo 'new-password' | lite pg password shop --password-stdin --yes`. On `app1` encrypt the new value again and run `lite project create` again, then deploy so the service restarts with it.

## Remove access

```sh
lite pg deny shop shop 10.0.0.5 --yes
```

removes the rule, and closes the port again if nothing else is allowed from that address.
