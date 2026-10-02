# Verification

Read the current `Makefile`, `.github/workflows/go.yml`, `.golangci.yml` and relevant test helpers before selecting checks. These determine package lists, toolchain requirements and CI commands; do not copy remembered version numbers or assume every Go package needs Docker.

## Static checks

For Go changes, run `make check-go`. It builds and vets the root module, checks tracked and untracked Go files using the selected Go toolchain's gofmt, and runs golangci-lint. Standalone staticcheck does not replace the configured golangci-lint run. Avoid an older system gofmt when the module requires newer syntax.

Nested module fixtures have their own `go.mod`; root-module build and tests do not cover every nested module. Check affected fixtures using their module's workflow. For contract changes, include CI's `GOOS=wasip1 GOARCH=wasm go build ./contract/...`. For schema/codegen changes, use applicable `goerp module generate --check` and module-test steps in CI; inspect required Node dependencies before relying on tests that can skip without them.

## Tests and infrastructure

Run focused `go test -race -timeout 30m <affected packages>` commands while investigating. Include dependent consumers when a shared API changes. Record package scope explicitly; targeted results do not establish that a full CI lane passed.

For complete delivery validation, run applicable `make test-cli` and/or `make test-engine` targets. Their package lists come from the Makefile and they default to `-race -timeout 30m`. Shared module or dependency changes can require both lanes. Consult CI for additional flags and checks rather than claiming these targets reproduce every CI step.

- CLI CI runs without the dev stack. Some module build tests require Node/npm and network or a warm dependency cache.
- Engine integration tests use `compose.dev.yml`. Use `make infra` when the task requires the live stack. Read current connection settings in the Makefile, compose file, README and test helpers: runtime traffic uses PgBouncer where specified, while schema work and some tests use direct Postgres connections.
- Use real services for integration coverage; focused unit tests and existing mocks remain appropriate for their own scope. Do not replace integration behavior with mocks merely to avoid unavailable infrastructure.
- Inspect verbose or structured test output for skips. A passing command with skipped integration tests does not verify those integrations.

Shared database contention can produce full-suite timeouts. Rerun failing packages alone to distinguish contention from logic defects, retaining both results in the report. An isolated pass does not erase a failed full-suite result. Do not reset shared databases, stop another session's engine or copy CI's volume-deleting cleanup into local review.

## Evidence and stopping conditions

Report selected checks as passed, failed, skipped or not run, with scope and reason. If a required tool or service is unavailable, identify the remaining gap; do not call the change fully verified. Resolve findings and rerun affected checks after fixes. Broaden testing when changed scope or unresolved failures justify it, rather than repeating passing checks without new evidence.
