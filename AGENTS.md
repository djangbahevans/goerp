# GoERP agent instructions

## Project skills

Shared project skills live in `.agents/skills/`. Read `.agents/skills/run-goerp/SKILL.md` when running the platform, provisioning a dev tenant, checking the app in a browser or running its test suites. Read `.agents/skills/review-goerp/SKILL.md` when reviewing Go changes. Follow their linked resources and run helpers from the paths specified in those skills.

## Design and scope

Canonical intended design lives in `../nexus-docs/docs/`. Read the canonical type/API reference rather than an illustrative example or stale issue text. Check related SDK wrappers and shipped consumers for actual defaults and API shapes. If an implemented consumer contradicts a documented API shape, reconcile the documentation and implementation rather than adding a duplicate endpoint. Verify claims about sibling components against their code. Unfiled backlog entries do not make documented behavior unspecified.

GoERP is recorded as unreleased, pre-v1. While that remains true, change schemas, ABI and wire interfaces in place; do not add compatibility fallbacks or database upgrade migrations. Update bootstrap CREATE statements. A shared dev database reset needs separate authorization and coordination. Verify release status before applying this assumption.

## Implementation

Before writing, modifying, fixing or refactoring Go code, read and follow `~/.agents/skills/use-modern-go/SKILL.md`. Run its `list` command for the relevant file or target Go version and read the complete output before editing. Apply the applicable guidelines to the implementation, including tests; use `explain` when evaluating a guideline and before skipping one that appears relevant. Follow this procedure from the start of each Go change, including fixes made during review.

Existing code is not a style reference for Go idioms. When nearby code uses an older form than a returned guideline, write the modern form anyway, and modernize the block being edited. Typical cases: `t.Context()` in tests instead of `context.Background()` (except inside `t.Cleanup`, where the test context is already cancelled), `errors.AsType`/`errors.Is` instead of type assertions and `==`, `wg.Go`, `slices`/`maps`/`cmp` instead of `sort` and manual loops, `cmp.Or` for fallbacks, `new(value)` instead of a temporary taken by address, and generic methods instead of package-level helpers that belong to one receiver type. `golangci-lint` enforces the `modernize` and `errorlint` subsets; the rest depends on following the guidelines.

## Comments

Write a comment only for what the code cannot say: a non-obvious constraint, trade-off, hazard or workaround. Do not restate the code, narrate the change or its history, or describe the writing and review process. Keep a comment to a line or two; longer rationale belongs in the design docs or the PR description. Never cite issue or PR numbers in code; a design-doc reference may supplement an explanation but not replace it. Surrounding comments are not a style reference either: when editing a block, remove redundant comments in it.

SDK comments (`sdk/go/`) are read by module authors through godoc, without access to `../nexus-docs`, the issue tracker or engine source. State the behavior a caller needs (what it does, its errors and constraints) directly. Do not cite design-doc sections, issue numbers or `internal/` paths, and do not explain the SDK in terms of engine internals. `scripts/check-sdk-comments.sh`, run by `make check-go` and CI, rejects the citations.

## Review and verification

Review every change with `~/.agents/skills/review-changes/SKILL.md`, regardless of size. For Go changes, also read and follow `.agents/skills/review-goerp/SKILL.md` each time; verify the complete branch diff, including tests, against the applicable modern Go guidelines. The Go skill does not cover `shell/` TypeScript. Prefer actual build/test output over stale IDE diagnostics.

For shell changes run format, lint, typecheck and tests from the `shell/` root, across the workspace. Read complete error output. Verify layout, scrolling, animation and focus timing in a real browser; jsdom cannot establish these behaviors. Use `.agents/skills/run-goerp/SKILL.md` for the local stack and browser driver. Shared Postgres and River workers can interfere with integration tests; investigate full-suite timeouts by rerunning failed packages alone and report skips honestly.
