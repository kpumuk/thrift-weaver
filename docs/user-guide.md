# User Guide (Beta)

This document covers how to use:

- `thriftfmt` (formatter CLI)
- `thriftls` (LSP server)
- the VS Code extension (`thrift-weaver-vscode`)

Beta status:
- `thriftfmt` and `thriftls` are usable now
- VS Code extension supports both managed `thriftls` install and external `thriftls` path fallback

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

Marketplace installs:

- Visual Studio Marketplace: https://marketplace.visualstudio.com/items?itemName=kpumuk.thrift-weaver-vscode
- Open VSX Registry: https://open-vsx.org/extension/kpumuk/thrift-weaver-vscode

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
- managed install is enabled by default and attempts to download/install `thriftls` using the configured manifest
- if managed install fails and `thrift.server.path` is configured, the extension falls back to that external binary
- if both managed install and external path are unavailable, the extension shows an actionable warning/error

Useful settings:

- `thrift.server.path`: path to `thriftls`
- `thrift.server.args`: extra args for `thriftls`
- `thrift.format.lineWidth`: preferred formatter width (forwarded by extension; server support may evolve)
- `thrift.trace.server`: LSP trace (`off`, `messages`, `verbose`)
- `thrift.managedInstall.enabled`: enable/disable managed `thriftls` install
- `thrift.managedInstall.manifestUrl`: manifest URL used by managed install
- `thrift.managedInstall.allowInsecureHttp`: allow non-HTTPS URLs for local/testing use only

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

### External path

Use `thrift.server.path` and manage the binary yourself.

Best for:

- immediate use today
- offline environments
- enterprise/internal mirrors
- controlled rollout/pinning outside the extension

### Managed install

Managed behavior:

- extension downloads a matching `thriftls` binary on demand
- selects artifact via `thriftls-manifest.json`
- verifies SHA-256 checksum before install
- preserves last-known-good binary on failed update
- falls back to `thrift.server.path` when configured and managed install fails

Release pipeline support already exists:

- per-platform `thriftls` artifacts
- `checksums.txt`
- `thriftls-manifest.json`
- artifact attestations (GitHub)

Implementation status in extension:

- shipped in current beta build
- external path remains fully supported for pinned/offline/internal workflows

## Linux Compatibility (Managed Binary Policy)

See `docs/linux-managed-binary-compatibility.md` for the explicit beta policy (glibc baseline, fallback guidance, and release-note requirements).
