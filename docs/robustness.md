# Robustness: Fuzzing and Race Testing

This document captures the M5 Track B robustness plan and commands.

## Fuzz Targets

Implemented fuzz targets:

- `internal/lexer` — `FuzzLex`
- `internal/syntax` — `FuzzParse`
- `internal/format` — `FuzzDocumentAndRange`

Each target seeds from:

- repository formatter fixtures (`testdata/format/input/*.thrift`)
- hand-written malformed examples (unterminated strings/comments, invalid UTF-8, comment-heavy snippets)

The fuzz targets intentionally cap oversized inputs to keep exploration focused and runtime stable.

## Local Fuzz Commands

Run short local fuzz sessions:

```bash
go test ./internal/lexer  -run='^$' -fuzz=FuzzLex               -fuzztime=10s
go test ./internal/syntax -run='^$' -fuzz=FuzzParse             -fuzztime=10s
go test ./internal/format -run='^$' -fuzz=FuzzDocumentAndRange  -fuzztime=10s
```

Longer pre-beta fuzz soak (example):

```bash
go test ./internal/lexer  -run='^$' -fuzz=FuzzLex              -fuzztime=2m
go test ./internal/syntax -run='^$' -fuzz=FuzzParse            -fuzztime=2m
go test ./internal/format -run='^$' -fuzz=FuzzDocumentAndRange -fuzztime=2m
```

## Nightly Fuzzing (CI)

Implemented scheduled workflow:

- `.github/workflows/nightly-fuzz.yml`
- `workflow_dispatch` for manual runs

Schedule policy:

- daily run: `2m` fuzz time per target
- weekly run: `10m` fuzz time per target

The workflow runs all three fuzz targets (`lexer`, `syntax`, `format`) independently and:

- uploads fuzz logs on every run
- uploads discovered crashers (`testdata/fuzz/...`) when present
- fails on new crashers or test failures

This is intentionally separated from normal PR CI to keep feedback latency reasonable.

## Race Detector Coverage

Race detector coverage is required for beta sign-off for the LSP/document-store packages.

Local command:

```bash
mise run test-race-lsp
```

Current scope:

- `./internal/lsp`
- `./cmd/thriftls`

CI:

- `.github/workflows/ci.yml` includes a dedicated `race-lsp` job running the same command.
