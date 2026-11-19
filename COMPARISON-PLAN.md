# Plan for comparing with alternatives (from primary sources)

(written 2026-10-29; the comparison was postponed: the documentation sites were blocked from the cloud session)

## Why

To understand, from the documentation and the code of the projects themselves, whether to turn this tool into a product or whether a set of scripts is enough. Reviews by third-party authors and vendors are not used as the basis of conclusions.

## Context of the niche (from the discussion)

- The target user: bought a VDS, wrote or vibe-coded some code, wants to deploy without a terminal.
- A possible interface: the user's AI agent (a skill or MCP) manages the server through the agent API; a panel is secondary.
- State and secrets are kept only on the server, not in GitHub or GitLab; databases and connection strings are managed too.
- Cloudflare Tunnel as the basis of publishing; the agent itself listens on nothing outward.
- Plugins: Caddy, Postgres, Podman and Docker. Multi-server later.

## Whom we compare and where to look

| Project | Documentation | Repository |
|---|---|---|
| Coolify | https://coolify.io/docs | coollabsio/coolify |
| Dokploy | https://docs.dokploy.com | Dokploy/dokploy |
| Kamal | https://kamal-deploy.org | basecamp/kamal |
| Dokku (if the budget allows) | https://dokku.com/docs | dokku/dokku |
| CapRover (if the budget allows) | https://caprover.com/docs | caprover/caprover |

## Criteria

1. Which user the panel or agent runs as on the host and what it can do to the host (root, the docker group, access to the socket).
2. Support for Podman and rootless mode.
3. Secrets: where they live, whether they are encrypted, whether they reach logs and builds.
4. Management of databases and connection strings, or containers only.
5. What listens outward; working with Cloudflare Tunnel.
6. Whether there is an API or MCP for an AI agent and what its permission limits are.
7. Resource use on an empty server.
8. Where the state is kept, whether there is backup and restore.

## Order of work (so as not to waste effort)

1. First Coolify, Dokploy, Kamal, documentation only, against the eight criteria.
2. Read code selectively, only where the documentation does not answer (above all criterion 1).
3. After the first pair of projects show an interim result and decide whether to continue.
4. The result: a table "what exists, what does not, where the niche is" and a conclusion: scripts or a product.

## What got in the way

The network policy of the cloud environment blocked `coolify.io`, `docs.dokploy.com`, `caprover.com`, `dokku.com`, `kamal-deploy.org`. To continue, add these domains to Allowed domains in the environment settings (Network access) or raise the access level, then start a new session, because the changes apparently do not apply to a running one.
