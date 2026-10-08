---
name: review-goerp
description: Review GoERP Go changes for documented behavior, dependency safety, lifecycle correctness, test isolation and repository conventions. Applies to Go files, diffs and PRs; excludes shell-only TypeScript changes.
---

# Review GoERP Go changes

Review the Go side of the repository: `internal/`, `cmd/`, `sdk/` and `contract/`. Read root `AGENTS.md` and instructions applicable to the changed files. All source paths below are relative to the repository root.

## Establish scope and requirements

Identify the requested base and diff, including tests and untracked files when reviewing working changes. Read the originating issue or spec and the canonical references in `../nexus-docs/docs/`. Trace affected consumers and SDK wrappers to establish actual wire shapes and defaults. Distinguish intended behavior, current implementation and discrepancies between them; do not silently treat one as proof of the other.

Use separate local passes for requirements, runtime behavior and conventions. Independent reviewers are optional when delegation is available and authorized; no particular model or agent API is required.

## Review applicable risks

Read only references relevant to the changes:

- [Runtime and health](references/runtime.md) for startup wiring, optional dependencies, health and readiness endpoints.
- [Test isolation and lifecycle](references/test-isolation.md) for WASM pools, tenant synchronization and running River workers.
- [Verification](references/verification.md) when choosing commands, interpreting skips or reporting readiness for delivery.

Check changed code against these conventions:

- Preserve initialism casing such as `DB`, `URL` and `API`.
- Error wrapping describes the failed operation, such as `"create connection pool: %w"`, rather than just naming the library call. Preserve underlying errors where callers depend on them.
- Use `New` for constructors, consistent with neighboring subsystem packages.
- Keep backend-specific environment parsing in the selected backend package; central `internal/engine/config.Config` owns shared configuration and backend selectors.
- Flag an older Go idiom that a returned modern-go guideline replaces, even when it matches surrounding code; consistency with old code is not a reason to keep it.
- New packages need a concise package comment explaining their purpose. Other comments explain non-obvious constraints rather than narrate the implementation, restate the code or cite issue numbers.
- SDK comments (`sdk/go/`) must be self-contained for a module author reading godoc: no design-doc sections, issue numbers or `internal/` paths. Run `scripts/check-sdk-comments.sh`, and also judge whether each changed SDK doc comment states the caller-visible behavior.

## Verify and report

Follow [Verification](references/verification.md) for required checks and the distinction between targeted testing and complete delivery validation. Confirm suspected failures with the real toolchain, relevant source or a focused reproduction; stale IDE diagnostics alone are not a finding. Derive regression-test expectations from requirements rather than the implementation being reviewed. An intentionally panicking scratch test is not the only acceptable evidence.

Report supported findings with severity, file/line, concrete trigger, impact and the violated requirement or invariant. Separate existing defects from changes introduced by the diff. State commands actually run and their outcomes, integration skips, unavailable tools, unresolved doc/code differences and remaining verification gaps. A review with no findings is not a claim that unrun checks passed.

Keep results in the conversation unless posting to an external PR is requested. Apply fixes only within the authorized task; follow root implementation instructions before editing code.
