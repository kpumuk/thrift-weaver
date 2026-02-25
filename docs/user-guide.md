# User Guide (Beta)

This document covers how to use:

- `thriftfmt` (formatter CLI)
- `thriftls` (LSP server)
- the VS Code extension (`thrift-weaver-vscode`)

Beta status:
- `thriftfmt` and `thriftls` are usable now
- VS Code extension supports external `thriftls` path today
- gopls-style managed `thriftls` install is planned (release artifacts/manifest/checksums are already produced)

## Install / Build

### Option 1: Install with `go install` (quick path)

```bash
go install github.com/kpumuk/thrift-weaver/cmd/thriftfmt@latest
go install github.com/kpumuk/thrift-weaver/cmd/thriftls@latest
```

Notes:
- use a pinned version (`@vX.Y.Z`) instead of `@latest` for reproducible installs
- this requires a working C toolchain because `thriftfmt` / `thriftls` use `tree-sitter` via `cgo`

### Option 2: Build from source (recommended for contributors)

```bash
git clone https://github.com/kpumuk/thrift-weaver.git
cd thrift-weaver
mise install

go build -o thriftfmt ./cmd/thriftfmt
go build -o thriftls ./cmd/thriftls
```

### Option 3: Download release artifacts

From GitHub Releases, download:

- `thriftfmt_<version>_<os>_<arch>.tar.gz|zip`
- `thriftls_<version>_<os>_<arch>.tar.gz|zip`

Verify integrity:

```bash
sha256sum -c checksums.txt
```

For provenance verification, see `docs/release.md` (artifact attestations).

## `thriftfmt` (CLI Formatter)

### Basic usage

```bash
thriftfmt path/to/file.thrift
thriftfmt --write path/to/file.thrift
thriftfmt --check path/to/file.thrift
thriftfmt --stdin --assume-filename foo.thrift < input.thrift
```

### Important flags

- `--write` / `-w`: write formatted output in-place
- `--check`: exit non-zero if the file would change
- `--stdin`: read input from stdin
- `--stdout`: force writing formatted output to stdout
- `--assume-filename`: parser context/diagnostic filename when using stdin
- `--line-width`: maximum line width (formatter target width)
- `--range start:end`: format a byte range (`start:end`, half-open)
- `--debug-tokens`: dump lexer tokens
- `--debug-cst`: dump CST nodes

### Exit codes

- `0`: success (including no-op formatting)
- `1`: `--check` found formatting changes
- `2`: unsafe to format (fail-closed; diagnostics printed)
- `3`: internal/usage error (invalid flags, read/write failure, etc.)

### Fail-closed behavior (important)

`thriftfmt` refuses to format when it cannot do so safely (for example, parser/lexer alignment problems or unsafe syntax diagnostics).

When that happens it prints:

- human-readable diagnostics with file/line/column
- source snippet + caret underline
- an `unsafe to format` error summary

## `thriftls` (LSP Server)

`thriftls` is a stdio LSP server intended to be launched by editors/clients.

- transport: stdio (JSON-RPC / LSP)
- primary use: VS Code extension (and other LSP-capable editors)

### Features currently implemented

- diagnostics (`didOpen` / `didChange` / `didClose`)
- document formatting
- range formatting
- document symbols
- folding ranges
- selection ranges
- cancellation handling (`$/cancelRequest`, best-effort in current sequential server loop)

### Not implemented yet (examples)

- go to definition
- rename
- semantic tokens
- code actions

## VS Code Extension

### Install the extension

Prepare the extension build:

```bash
cd editors/vscode
npm install --no-package-lock
npm run compile
```

Development host:

```bash
# Press F5 in VS Code to launch Extension Development Host
```

Package a `.vsix` manually (for regular VS Code):

```bash
cd editors/vscode
npm run package -- --no-yarn --allow-missing-repository
code --install-extension thrift-weaver-vscode-<version>.vsix --force
```

### Configure `thriftls`

Current beta behavior:
- set `thrift.server.path` to a local `thriftls` binary
- when empty, the extension warns (managed install is planned but not implemented yet)

Useful settings:

- `thrift.server.path`: path to `thriftls`
- `thrift.server.args`: extra args for `thriftls`
- `thrift.format.lineWidth`: preferred formatter width (forwarded by extension; server support may evolve)
- `thrift.trace.server`: LSP trace (`off`, `messages`, `verbose`)

Extension defaults for Thrift files:

- `editor.tabSize = 2`
- `editor.insertSpaces = true`
- `editor.detectIndentation = false`

### VS Code features currently expected to work

- syntax highlighting (TextMate)
- diagnostics
- format document / format selection
- document symbols (Outline)
- folding ranges
- selection ranges
- `Thrift: Restart Language Server` command

### Troubleshooting

If the extension command is missing or features are not working:

1. Check the extension output channels:
   - `Thrift Weaver`
   - `Thrift Weaver LSP Trace`
2. Confirm `thrift.server.path` points to an existing `thriftls` binary
3. Run `Developer: Reload Window`
4. Ensure the file language mode is `Thrift`

## Managed Install vs External Path (Beta Guidance)

### External path (supported today)

Use `thrift.server.path` and manage the binary yourself.

Best for:

- immediate use today
- offline environments
- enterprise/internal mirrors
- controlled rollout/pinning outside the extension

### Managed install (planned)

Planned behavior (gopls-style):

- extension downloads a matching `thriftls` binary on demand
- selects artifact via `thriftls-manifest.json`
- verifies SHA-256 checksum before install
- preserves last-known-good binary on failed update

Release pipeline support already exists:

- per-platform `thriftls` artifacts
- `checksums.txt`
- `thriftls-manifest.json`
- artifact attestations (GitHub)

Implementation status in extension:

- not yet shipped in current beta build
- use external path for now

## Linux Compatibility (Managed Binary Policy)

See `docs/linux-managed-binary-compatibility.md` for the explicit beta policy (glibc baseline, fallback guidance, and release-note requirements).
