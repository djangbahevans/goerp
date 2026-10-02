# Test isolation and resource lifecycle

## WASM pool shutdown

A shared `wasm.Runtime` must outlive every pool and background operation using it. Inspect pools created during install/reload, including superseded pools whose asynchronous cleanup may still be running.

- Drain and close live pools before closing the shared runtime.
- Register pool cleanup after runtime cleanup so `t.Cleanup`'s last-in-first-out order closes pools first.
- Wait for superseded pools' cleanup where a test exercises replacement; draining the final registry snapshot alone does not prove old background work stopped.
- Confirm lifecycle changes with affected package tests under `-race`.

Use `newLeader` in `internal/engine/modulereload/leader_test.go` and `newWorker` in `internal/engine/moduleinstall/worker_test.go` as examples. Read their implementations before adopting the pattern; production and test ownership can differ.

## Shared tenant enumeration

`ActiveTenants()` enumerates active tenants across the database. Concurrent package test binaries can synchronize a test's tenant using another test's module with the same name.

For assertions that a module's sync did not run, use a tenant/module-scoped record in `system.module_schema_versions` rather than absence of a commonly named table. See `moduleSyncRecorded` in `internal/engine/moduleinstall/worker_test.go` and `internal/engine/tenant/sync/tenantsync_test.go`. Use distinct tenant and module identities when tests need isolation beyond that assertion.

## Running River clients

Clients sharing a schema and queue names can claim each other's jobs even when their worker registries support different kinds. A second client against the shared dev schema can make a test's jobs retry or appear never to run.

For tests that start workers:

- Allocate a private schema with `riverdbtest.TestSchema(ctx, t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})` directly at the test call site.
- Pass that schema to `jobqueuetest.New` from `internal/engine/jobqueue/jobqueuetest`.
- Keep `TestSchema` at the call site: caller-derived naming is part of its isolation mechanism; moving it into a shared helper can create collisions.
- Stop worker activity before schema and database cleanup.

Production wiring and insert-only clients that never start workers can use `jobqueue.New`. Check whether a running engine shares queues with tests before attributing missing jobs to application logic. Coordinate ownership rather than killing another session's process.
