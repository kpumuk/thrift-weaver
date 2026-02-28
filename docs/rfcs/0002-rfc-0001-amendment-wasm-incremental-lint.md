# RFC 0002: Amendment to RFC 0001 (WASM Backend, Verified Incremental Parsing, Lint MVP)

- Status: Proposed
- Authors: Dmytro Shteflyuk, Codex
- Created: 2026-02-27
- Amends: `docs/rfcs/0001-thrift-tooling-platform.md`

## Summary

This amendment updates RFC 0001 to replace cgo-first parser policy and full-reparse-on-change policy with:

- WASM parser backend as the default runtime strategy
- Verified incremental reparsing on `textDocument/didChange`
- Full-file lint-on-change MVP (debounced), with changed-range lint explicitly experimental
- Single wasm parser runtime path; cgo parser backend is removed from target architecture

This amendment does not change formatter fail-closed behavior, lossless lexer strategy, or core product scope.

## Motivation

RFC 0001 intentionally accepted cgo and full reparse for v1 speed. Since then, implementation planning identified release and operability risks:

- cgo complicates cross-platform release and `CGO_ENABLED=0` goals
- full reparse on every edit does not exploit tree-sitter incremental strengths
- diagnostics pipeline needs explicit lint layering with cancellation/staleness guarantees
- runtime backend toggles introduce avoidable complexity once cgo is dropped

## Normative Changes to RFC 0001

## 1. Parser Runtime Policy

RFC 0001 section “cgo / Build Strategy” is amended as follows:

- Default parser backend is WASM executed by a pure-Go runtime (`wazero` or equivalent).
- Default release artifacts must build with `CGO_ENABLED=0`.
- cgo parser backend is not part of the target runtime architecture and is removed.

## 2. LSP Parse Mode

RFC 0001 section stating full reparse on change is amended as follows:

- `textDocument/didChange` must use incremental reparsing when eligibility checks pass.
- Incremental flow requires both:
  - applying edit metadata to prior syntax tree (`tree.Edit` equivalent)
  - parse invocation with prior tree (`parse(oldTree)` equivalent)
- If eligibility checks fail, server must do one full-parse fallback for that version.

## 3. Incremental Verification Requirement

Implementations must include explicit correctness verification:

- changed-range containment checks against declared edits
- periodic full-parse equivalence check (configurable cadence)
- degradation behavior when verification fails (diagnostics stay correct, no stale reuse)

## 4. Lint Diagnostics Policy (MVP)

- `didOpen`: full parse + full lint
- `didChange`: parse + debounced **full-file lint**
- `didSave`: full lint; expensive rules only when explicitly enabled
- changed-range lint is non-normative experimental mode until promoted by separate RFC amendment

## 5. Error and Staleness Semantics

- Diagnostics publication must be version- and generation-gated.
- Empty diagnostic sets must be published to clear stale results.
- Parser failure for current version must not reuse prior-version tree diagnostics as if current.

## 6. Runtime Configuration Policy

- Parser runtime configuration does not include backend-toggle settings.
- Implementations expose a single wasm parser backend in supported builds.
- Failure handling uses degraded-mode diagnostics and normal artifact rollback; no alternate runtime parser path is required.

## 7. Security and Resource Constraints

- WASM runtime must run with restricted host capabilities (no network/filesystem by default).
- Memory/time limits for parse and query execution are required.
- Input bounds are required for document size and edit volume.

## 8. Failure Policy by Surface

- Editor parse lifecycle (`didOpen`, `didChange`) is fail-open:
  - document version/text must advance even when parser runtime initialization/reparse fails
  - implementation must store a degraded current-version tree and publish parser internal diagnostics for that version
  - prior-version parser diagnostics must not be reused as current-version results
- Formatter remains fail-closed:
  - formatting requests must refuse output when parse state is unsafe (missing root or non-recoverable parser diagnostics)
- Linter policy (future) is fail-closed:
  - if parse state is unsafe, rules do not run and only parser/internal diagnostics are published

## Non-Changes

This amendment does not change:

- formatter unsafe/fail-closed policy
- lossless lexer as formatting token/trivia source of truth
- initial non-goals (semantic type checking, cross-file features)

## Acceptance Criteria for Amendment Adoption

The amendment is considered implemented when all are true:

1. WASM backend path exists and is default for supported builds.
2. `didChange` path has verified incremental behavior in tests.
3. Lint MVP is wired as full-file debounced lint-on-change.
4. CI validates WASM artifact drift and `CGO_ENABLED=0` builds.
5. No parser backend toggle exists in user-facing configuration.
6. Editor lifecycle is fail-open while formatter remains fail-closed under parser runtime failures.

## Compatibility and Migration Notes

- No user-visible protocol break is intended; LSP wire compatibility is maintained.
- Performance and reliability gates are enforced by CI benchmarks and release smoke tests.
- Legacy cgo code can be removed once wasm migration milestones are complete.

## References

- [RFC 0001](/Users/dmytro/work/github/thrift-weaver/docs/rfcs/0001-thrift-tooling-platform.md)
