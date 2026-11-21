# pr-preview

pr-preview gives every open pull request you write a live preview on one server.

Open `https://BASE_DOMAIN/<repo>/<pr>`. The page shows the build while it runs, then sends you to
the PR's own origin, `https://<repo>-<pr>.BASE_DOMAIN/`. Each PR gets its own subdomain, so apps
work at `/` unchanged and their cookies and storage stay apart.

## How it works

```
GitHub GraphQL ──poll──▶ pr-preview ──docker compose──▶ pv-<slug> project
                              ▲                              │
browser ─▶ Caddy (TLS) ───────┘◀──── reverse proxy ──────────┘
            BASE_DOMAIN, *.BASE_DOMAIN       network "pr-preview", alias pv-<slug>
```

- Every `POLL_SECONDS` the service lists the token owner's open PRs in all repos.
- The first run records each PR's head commit and builds nothing. After that, a new PR or a new
  commit starts a build. A visit to a preview that is not running builds it or wakes it; with
  `ACCESS_PASSWORD` set, only a signed-in visit does.
- One build runs at a time (`BUILD_CONCURRENCY`). Visits go before builds started by a push.
  While a new commit builds, the old containers keep serving.
- At most `MAX_RUNNING` previews run. The service stops the least recently used one to make room,
  and stops previews idle for `IDLE_MINUTES`. Stopped previews keep their volumes.
- When a PR closes, the service removes its containers, volumes, images and files.
- Every 6 hours it prunes the build cache down to 15 GB and removes dangling images.
- State lives in `DATA_DIR/state.json`. On start the service checks it against what Docker runs.

## Install

1. Point two DNS-only A records at the server: `BASE_DOMAIN` and `*.BASE_DOMAIN`.
2. Clone this repo on the server and copy `.env.example` to `.env`. Set `BASE_DOMAIN` and
   `GITHUB_TOKEN`.
3. Run `docker compose up -d`.
4. Open `https://BASE_DOMAIN/`. Caddy gets a certificate for each host on its first visit.

You can keep the token out of `.env` and pass it from the shell:
`GITHUB_TOKEN="$(gh auth token)" docker compose up -d`.

## How a PR is built

The service fetches `refs/pull/<n>/head` into `DATA_DIR/previews/<slug>/src` and picks the first
recipe that exists:

1. `.preview/compose.yml` (or `.yaml`) on the PR's base branch. The PR's own copy is never used, so a
   PR cannot change how it is contained.
2. `recipes/<repo>.yml` on the server, for repos that carry no recipe.
3. `Dockerfile` in the repo root. The app gets `PORT` and listens on the last `EXPOSE`d port, or 3000.
4. [Railpack](https://railpack.com) detects the stack and builds an image. The app gets `PORT=3000`.

[recipes/README.md](recipes/README.md) describes the recipe format. The preview is ready when its
entry service answers HTTP on its port.

## URLs

| URL | What it does |
| --- | --- |
| `/` | Dashboard of open PRs by repo |
| `/<repo>/<pr>[/path]` | Entry: status page, then a redirect to the PR's subdomain |
| `/<owner>/<repo>/<pr>[/path]` | Entry when two owners have the same repo name |
| `https://<slug>.BASE_DOMAIN/` | The preview itself |
| `/_/api/previews` | Previews as JSON |
| `/_/api/previews/<slug>?offset=N` | One preview with its build log from byte `N` |
| `POST /_/api/previews/<slug>/rebuild` | Build the current commit again |
| `POST /_/api/previews/<slug>/stop` | Stop the containers, keep volumes |
| `POST /_/api/previews/<slug>/reset` | Remove containers and volumes, then build |
| `/_/health` | Health check |

The slug is `<repo>-<pr>`, lowercased. If two owners have an open PR with the same repo name and
number, the non-fork repo gets the short slug and the other gets `<owner>-<repo>-<pr>`.

## Settings

| Variable | Default | What it does |
| --- | --- | --- |
| `BASE_DOMAIN` | | Domain that serves the dashboard; previews use its subdomains |
| `GITHUB_TOKEN` | | Token that lists your PRs and fetches their code |
| `ACME_EMAIL` | empty | Email for Let's Encrypt notices |
| `ACCESS_PASSWORD` | empty | Password for private-repo previews and all actions; empty means open |
| `REPORT_OWNERS` | empty | Repo owners to post GitHub deployments to, comma separated; `*` for all |
| `MAX_RUNNING` | `4` | Previews that may run at once |
| `IDLE_MINUTES` | `240` | Stop a preview after this long without requests |
| `POLL_SECONDS` | `60` | How often to list open PRs |
| `BUILD_CONCURRENCY` | `1` | Builds that may run at once |
| `BUILD_TIMEOUT_MINUTES` | `30` | Stop a build that takes longer |
| `DATA_DIR` | `/var/lib/pr-preview` | Sources, logs and state; mounted at the same path in the container |
| `PREVIEW_RUNTIME` | empty | Docker runtime for preview containers; set `runsc` to run them in gVisor |
| `PREVIEW_MEMORY` | `2g` | Memory limit of each preview container |
| `PREVIEW_CPUS` | `2` | CPU limit of each preview container |

With `REPORT_OWNERS` set, the service creates a transient `pr-preview` deployment for each build and
sets it to in progress, success (with the entry URL) or failure, and to inactive when the PR closes.
It also keeps one comment on the PR with the preview link and the recipe's `x-preview.note`, such as
the sign-in of a seeded account.

## Security

- Give the token the `repo` scope. It reads your private repos and, with `REPORT_OWNERS`, writes
  deployments. Builds never see it.
- The service holds the Docker socket, so it has root on the server. Run it on a server meant for
  previews only.
- Only the token owner's PRs are built. PRs from other people never run.
- PR code is treated as untrusted. Install [gVisor](https://gvisor.dev) and set
  `PREVIEW_RUNTIME=runsc`: every preview container then runs on gVisor's own kernel, not the
  host's. Each container also gets no new privileges, no added capabilities or devices, and memory,
  CPU and process limits.
- Register gVisor in `/etc/docker/daemon.json` as
  `"runsc": { "path": "/usr/bin/runsc", "runtimeArgs": ["--network=host"] }`. The flag passes
  network calls to the container's own network namespace; without it, service names do not resolve
  on Docker hosts that use nftables.
- A recipe comes from the server or the base branch, never from the PR. Every host path it names
  (bind mounts, env files, secrets, build contexts) must resolve inside that preview's own
  checkout, so a symlink in a PR cannot reach the host. Host namespaces are refused.
- Each preview has its own network. It reaches the internet and this service, not other previews.
- Image builds run in BuildKit's standard sandbox, not in gVisor.
- With `ACCESS_PASSWORD` set, private-repo previews, their logs and every action need a session.
  The session cookie covers the subdomains and never reaches a preview app. Actions also need an
  `Origin` of `https://BASE_DOMAIN`, because preview apps on the subdomains are same-site.
- Previews are public unless the repo is private and `ACCESS_PASSWORD` is set.
- Set `ACCESS_PASSWORD`. Every preview host gets a public certificate, so crawlers find it within
  minutes; without a password each crawler visit can start a build.
- [examples/erxes.yml](examples/erxes.yml) is a server recipe for a Postgres, API and UI stack.

## License

MIT
