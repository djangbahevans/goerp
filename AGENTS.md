# GoERP agent instructions

## Project skills

Shared project skills live in `.agents/skills/`. Read `.agents/skills/run-goerp/SKILL.md` when running the platform, provisioning a dev tenant, checking the app in a browser or running its test suites. Read `.agents/skills/review-goerp/SKILL.md` when reviewing Go changes. Follow their linked resources and run helpers from the paths specified in those skills.

## Design and scope

Canonical intended design lives in `../nexus-docs/docs/`. Read the canonical type/API reference rather than an illustrative example or stale issue text. Check related SDK wrappers and shipped consumers for actual defaults and API shapes. If an implemented consumer contradicts a documented API shape, reconcile the documentation and implementation rather than adding a duplicate endpoint. Verify claims about sibling components against their code. Unfiled backlog entries do not make documented behavior unspecified.

GoERP is recorded as unreleased, pre-v1. While that remains true, change schemas, ABI and wire interfaces in place; do not add compatibility fallbacks or database upgrade migrations. Update bootstrap CREATE statements. A shared dev database reset needs separate authorization and coordination. Verify release status before applying this assumption.

## Implementation

Before writing, modifying, fixing or refactoring Go code, read and follow `~/.agents/skills/use-modern-go/SKILL.md`. Run its `list` command for the relevant file or target Go version and read the complete output before editing. Apply the applicable guidelines to the implementation, including tests; use `explain` when evaluating a guideline and before skipping one that appears relevant. Follow this procedure from the start of each Go change, including fixes made during review.

## Review and verification

Review every change with `~/.agents/skills/review-changes/SKILL.md`, regardless of size. For Go changes, also read and follow `.agents/skills/review-goerp/SKILL.md` each time; verify the complete branch diff, including tests, against the applicable modern Go guidelines. The Go skill does not cover `shell/` TypeScript. Prefer actual build/test output over stale IDE diagnostics.

For shell changes run format, lint, typecheck and tests from the `shell/` root, across the workspace. Read complete error output. Verify layout, scrolling, animation and focus timing in a real browser; jsdom cannot establish these behaviors. Use `.agents/skills/run-goerp/SKILL.md` for the local stack and browser driver. Shared Postgres and River workers can interfere with integration tests; investigate full-suite timeouts by rerunning failed packages alone and report skips honestly.
