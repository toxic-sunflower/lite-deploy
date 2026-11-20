# Deploy from CI

Your CI builds the image (or archive) and tells the server to deploy it. The server end is protected by a **restricted SSH key**: a key that can do exactly three things for one project, and nothing else.

| Command | What it does |
|---|---|
| `deploy <40-character hash>` | deploy that version |
| `rollback` | switch back to the previous version |
| `status` | show the state |

The key gives no shell, no file access and no other commands, even if it leaks. The rest of the system never runs code that CI sends: CI only names a hash.

## 1. Make a key for CI

On your workstation:

```sh
ssh-keygen -t ed25519 -N '' -C 'shop-ci' -f shop-ci
```

`shop-ci` is the private key (a CI secret), `shop-ci.pub` the public one. Only **ed25519** keys are accepted.

## 2. Put the public key into the project

In `shop.json`:

```json
"deploy_key": { "public_key": "ssh-ed25519 AAAAC3Nza… shop-ci" }
```

Run `lite project create shop.json …` again. `lite` adds a line to the project user's `~/.ssh/authorized_keys` that forces the gatekeeper and drops everything else (`restrict`). Other lines in that file are not touched.

The project user can now log in over SSH only to run those three commands. If you use `lite security allow-users`, project users are added to the allow list automatically.

## 3. Test it from your workstation

```sh
ssh -i shop-ci shop@YOUR_SERVER status
ssh -i shop-ci shop@YOUR_SERVER deploy 3f2c0d1e9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
ssh -i shop-ci shop@YOUR_SERVER 'rm -rf /'
```

The first two work. The third is refused with "the command is not allowed".

## 4. A GitHub Actions workflow

Store these as repository secrets: `DEPLOY_KEY` (the private key), `DEPLOY_HOST` (the server address) and `DEPLOY_KNOWN_HOSTS` (the output of `ssh-keyscan -t ed25519 YOUR_SERVER`, so the server is verified).

```yaml
name: deploy
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-24.04
    environment: production
    steps:
      - uses: actions/checkout@v4

      - name: Build and push the image
        run: |
          echo "${{ secrets.GITHUB_TOKEN }}" | docker login ghcr.io -u "${{ github.actor }}" --password-stdin
          docker build -t ghcr.io/you/shop-api:${{ github.sha }} .
          docker push ghcr.io/you/shop-api:${{ github.sha }}

      - name: Deploy
        env:
          DEPLOY_KEY: ${{ secrets.DEPLOY_KEY }}
          DEPLOY_HOST: ${{ secrets.DEPLOY_HOST }}
          DEPLOY_KNOWN_HOSTS: ${{ secrets.DEPLOY_KNOWN_HOSTS }}
        run: |
          install -m 700 -d ~/.ssh
          printf '%s\n' "$DEPLOY_KEY" > ~/.ssh/shop-ci && chmod 600 ~/.ssh/shop-ci
          printf '%s\n' "$DEPLOY_KNOWN_HOSTS" > ~/.ssh/known_hosts
          ssh -i ~/.ssh/shop-ci shop@"$DEPLOY_HOST" deploy "${{ github.sha }}"
```

The image tag is the commit hash, which is exactly what `lite` pulls. The job fails if the deploy fails (the new version did not become healthy), and traffic stays on the old one.

For a [project without containers](process-project.md) replace the build step with building, signing and uploading the archive.

## Good habits

- Restrict the `production` environment to the `main` branch, and turn on two-factor authentication on the account that can push to it: whoever can push to `main` can deploy.
- Use one key per project. Rotating a key is: generate a new one, put it into `deploy_key`, run `lite project create` again.
- Only one deploy of a project runs at a time: a second one is refused with `deploy_in_progress` until the first finishes.
