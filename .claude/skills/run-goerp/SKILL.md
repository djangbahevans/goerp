---
name: run-goerp
description: Start, run and drive the goerp platform locally — dev infrastructure, the engine, the shell app — provision a dev tenant with an admin login, and drive the shell in a headless browser to click through views and take screenshots. Use when asked to run goerp, start the engine or shell, check a change in the real app or a real browser, screenshot a page, create a test tenant, or run the Go/shell test suites.
---

The platform runs as the compose dev stack (`make infra`), the engine on the host (`make engine`, `:8080`/`:8081`) and the shell app's Vite dev server (`make shell`, `:5173`, proxying API calls to the engine). Drive the shell with `.claude/skills/run-goerp/driver.mjs`, a headless-Chromium REPL that reads commands from stdin and writes screenshots to `/tmp/goerp-shots/`.

All paths are relative to the repo root.

## Prerequisites

Docker (with the compose plugin), Go (the `go` command downloads the toolchain `go.mod` asks for), and Node/npm.

The driver needs `playwright-core` pinned to the Chromium build in `~/.cache/ms-playwright/`. 1.63.0 uses Chromium 1243:

```bash
npm install --prefix ~/.cache/goerp-run-driver --no-audit --no-fund playwright-core@1.63.0
(cd ~/.cache/goerp-run-driver && npx playwright-core install chromium)
```

## Setup

```bash
(cd shell && npm install && npm run build -w @goerp/sdk)
make infra
make module M=.claude/skills/run-goerp/sample-crm
git checkout -- .claude/skills/run-goerp/sample-crm/manifest.json
```

The app imports `@goerp/sdk` from its `dist/`, so rebuild it (`npm run build -w @goerp/sdk` in `shell/`) after any SDK change. `make module` builds into `.dev/modules/`, where `make engine` loads it from. It also writes the build checksum into the module's own `manifest.json` (goerp#1231), which is what the `git checkout` undoes.

## Run (agent path)

Start the engine and the shell, each in the background, and poll until both answer:

```bash
make engine > /tmp/goerp-engine.log 2>&1 &
make shell > /tmp/goerp-shell.log 2>&1 &
timeout 180 bash -c 'until curl -sf localhost:8080/_ready >/dev/null && curl -sf -o /dev/null -H "Accept: text/html" localhost:5173/; do sleep 1; done'
curl -s localhost:8080/_ready
```

`/_ready` should report every module `ready`. Then provision a tenant whose admin can sign in, with the sample module enabled:

```bash
.claude/skills/run-goerp/bootstrap-tenant.sh demo admin@demo.test Demo-Pass-2026! crm
```

Drive it. Each line is one command, and output and errors print per command:

```bash
node .claude/skills/run-goerp/driver.mjs <<'EOF'
open demo
login admin@demo.test Demo-Pass-2026!
h1
ss contacts-list
click text=New Contact
fill role=textbox[name="Name"] Kofi Mensah
fill role=textbox[name="Email"] kofi@example.test
click role=button[name="Save"]
eval new Promise(r => setTimeout(() => r(location.pathname), 1500))
click role=button[name="CRM"]
click role=link[name="Contacts"]
wait text=Kofi Mensah
ss contacts-list-after
errors
EOF
```

Screenshots land in `/tmp/goerp-shots/NN-<name>.png` (override with `SHOTS_DIR`). **Look at them**: a page can render its chrome while its content is empty.

| command | what it does |
|---|---|
| `open <tenant>` | base URL becomes `http://<tenant>.localhost:5173` |
| `login <email> <password>` | signs in through the form; fails with the page's alert text if the form is still up |
| `nav <path>` | loads a path (e.g. `/_m/crm/contacts`, `/activities`) and prints its heading |
| `h1` | prints the page heading (times out on a page without one, such as the home page of a tenant with no modules) |
| `click <selector>` / `fill <selector> <text>` / `text <selector>` / `wait <selector>` | Playwright selectors: `text=…`, `role=button[name="Save"]`, CSS |
| `viewport <w> <h>` | resizes, e.g. `viewport 390 844` for phone width |
| `ss [name]` / `ssfull [name]` | viewport / full-page screenshot |
| `errors` | page errors and failed requests so far (known-harmless ones filtered) |
| `eval <js>` | evaluates in the page and prints the JSON result |
| `quit` | closes the browser (EOF does too) |

For step-by-step debugging, run the same driver under tmux and `send-keys` one command at a time.

Stop everything this session started:

```bash
for p in $(ss -ltnp | grep -E ':(8080|8081|5173) ' | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u); do kill $p; done
timeout 30 bash -c 'while ss -ltn | grep -qE ":(8080|8081|5173) "; do sleep 1; done'
```

The engine shuts down gracefully, so its ports can take up to about 15 seconds to free. Leave the containers running: other sessions share them.

## Run (human path)

`make infra`, then `make engine` and `make shell` in two terminals, then the bootstrap script, then open `http://<tenant>.localhost:5173` in a browser. Ctrl-C stops each. `make` lists every target.

## Test

```bash
make check-go
make test-cli
make test-engine
make check-shell
(cd shell && npm run test-storybook)
```

`test-cli`/`test-engine` are CI's exact Go test lines and need `make infra`. `test-engine` takes about 20 minutes. The Temporal-backed `tenant/offboard` and `tenant/provision` tests can time out under full-suite load; rerun those two packages alone before treating a failure as real. Stop a running `make engine` first: its River workers would claim the tests' jobs from the shared database.

## Gotchas

- **Poll the shell with `Accept: text/html`.** The dev server proxies every non-Vite path to the engine, so a plain `curl localhost:5173/` gets the engine's 404 even when Vite is up.
- **Use `<tenant>.localhost`, not `localhost`.** The engine resolves the tenant from the host, and the `__Host-`/`Secure` auth cookies only work on `localhost` hosts. `make engine` sets `GOERP_PLATFORM_DOMAIN=localhost`, so provisioning registers `<slug>.localhost` itself.
- **`goerp tenant create --wait` exits `not_found` after a successful create** (goerp#1216). The bootstrap script creates with `--wait=false` and polls `system.tenants` instead.
- **Enabling a module needs no engine restart** (the script drops the Redis entitlement cache), but loading a newly built `.erp` does: restart `make engine` after `make module`.
- **Sidebar groups start collapsed.** Click the group (`role=button[name="CRM"]`) before its item link.
- **`wait text=…` doesn't match an input's value**; read values with `eval`.
- **A cold load of a module URL shows "Page not found" for up to a few seconds** (goerp#1230). `nav` and `h1` wait it out; a heading still reading "Page not found" after that is real.
- **Full page loads clear the client cache.** To reproduce in-app state bugs, navigate with `click`, not `nav`.
- **`/_notif/*` 404s are expected** (no backend yet, goerp#1112); `errors` filters them.
- **Repeated logins hit the rate limiter.** Clear it with `docker compose -f compose.dev.yml exec -T redis sh -c 'redis-cli --scan --pattern "ratelimit:*" | xargs -r redis-cli del'`.
- **The dev database is shared** with every other session and test run on this machine. Check `system.tenants` before assuming a tenant exists, and never reuse another session's tenant. Users are global across tenants, so give each new tenant its own admin email.
- **An engine that can't bind its ports keeps running** (goerp#1220), serving nothing. Check `ss -ltnp | grep -E ':(8080|8081|5173) '` before `make engine`.

## Troubleshooting

- **`listen tcp :8080: bind: address already in use` in the engine log, but the process stays up**: another engine holds the port. Kill it (the stop command above) and restart.
- **`relation "system.river_job" does not exist` / `column "unique_key" does not exist` from a running engine**: the database was reset under it (e.g. `make infra-down` with `-v`, or another session). Restart the engine; it re-bootstraps the schema.
- **`Cannot find module '@storybook/addon-vitest/vitest-plugin'` from `make check-shell`**: `shell/node_modules` predates a dependency change. Run `npm install` in `shell/`.
- **`you are using a configuration file for golangci-lint v2 with golangci-lint v1`**: install v2 with `GOTOOLCHAIN=go1.27.1 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
- **`method must have no type parameters` from `gofmt`**: that `gofmt` predates Go 1.27. `make check-go` uses the toolchain's own (`$(go env GOROOT)/bin/gofmt`).
- **`login` fails with the form still up and no alert**: the password is wrong or the invite was never accepted. Re-running the bootstrap script with the same arguments retries an unaccepted invite. If the script printed `already has an account`, the email belongs to an existing user whose password wins; use a new email.
