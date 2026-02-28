<h3 align="center">
	<img src="https://raw.githubusercontent.com/kpumuk/thrift-weaver/main/editors/vscode/media/icon.png" width="100" alt="Logo"/><br/>
	Thrift Weaver for <a href="https://code.visualstudio.com">VSCode</a>
</h3>

<p align="center">
    <a href="https://github.com/kpumuk/thrift-weaver/stargazers"><img src="https://img.shields.io/github/stars/kpumuk/thrift-weaver?colorA=363a4f&colorB=b7bdf8&style=for-the-badge"></a>
    <a href="https://github.com/kpumuk/thrift-weaver/issues"><img src="https://img.shields.io/github/issues/kpumuk/thrift-weaver?colorA=363a4f&colorB=f5a97f&style=for-the-badge"></a>
    <a href="https://github.com/kpumuk/thrift-weaver/contributors"><img src="https://img.shields.io/github/contributors/kpumuk/thrift-weaver?colorA=363a4f&colorB=a6da95&style=for-the-badge"></a>
</p>

`thrift-weaver` helps you write and format Thrift IDL files.

It has three parts:

- `thriftfmt`: a safe formatter for `.thrift` files
- `thriftls`: an LSP server for editors.
- a VS Code extension with syntax highlighting and LSP support.

It is made for daily editor use. It keeps comments. It stays stable. It can handle broken or half-written files while you type.

> [!IMPORTANT]
> Read [RFC 0001](docs/rfcs/0001-thrift-tooling-platform.md) before code changes. If behavior or policy changes, update the RFC in the same PR.

## Why Use `thrift-weaver`

- One stack for CLI and editor use.
- Safe mode: if format is not safe, the tool returns an error.

## Usage

### Preferred method of installation

Install the extension from a Marketplace:

- [Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=kpumuk.thrift-weaver-vscode)
- [Open VSX Registry](https://open-vsx.org/extension/kpumuk/thrift-weaver-vscode)

### Manual method for installation

Download the VSIX from
[the latest GitHub release](https://github.com/kpumuk/thrift-weaver/releases/latest).
Open the Command Palette and select "Extensions: Install from VSIX...", then open the file you just downloaded.

## Local Installation

### Needs

- Go (pinned in [`mise.toml`](mise.toml)).
- Node.js and npm (for the VS Code extension).

### Setup From Source (recommended)

```bash
git clone https://github.com/kpumuk/thrift-weaver.git
cd thrift-weaver
mise trust
mise install
mise exec lefthook -- lefthook install
```

Build the CLIs:

```bash
go build -o thriftfmt ./cmd/thriftfmt
go build -o thriftls ./cmd/thriftls
```

### Quick Install With `go install`

```bash
go install github.com/kpumuk/thrift-weaver/cmd/thriftfmt@latest
go install github.com/kpumuk/thrift-weaver/cmd/thriftls@latest
```

For stable installs, use a version tag, not `@latest`.

## Use `thriftfmt`

Common commands:

```bash
thriftfmt path/to/file.thrift
thriftfmt --write path/to/file.thrift
thriftfmt --check path/to/file.thrift
thriftfmt --stdin --assume-filename foo.thrift < input.thrift
thriftfmt --range 120:240 path/to/file.thrift
```

Main flags:

- `--write`, `-w`: write in place.
- `--check`: non-zero exit when the file would change.
- `--stdin`: read source from stdin.
- `--stdout`: force stdout output.
- `--assume-filename`: file name used in parser context and errors.
- `--line-width`: preferred max line width (default `100`).
- `--range start:end`: byte range, half-open.
- `--debug-tokens`, `--debug-cst`: debug dumps.

Exit codes:

- `0`: success.
- `1`: `--check` found changes.
- `2`: unsafe to format.
- `3`: internal or usage error.

> [!WARNING]
> `thriftfmt` refuses unsafe formatting and reports errors. It does not guess.

## Use `thriftls`

`thriftls` is a stdio LSP server. Your editor starts it.

Current features:

- Live text sync.
- Errors as you type.
- Document formatting and range formatting.
- Document symbols.
- Folding ranges.
- Selection ranges.
- Semantic tokens (`textDocument/semanticTokens/full`).

## VS Code Extension

The extension lives in `editors/vscode`.

Build for local development:

```bash
npm --prefix editors/vscode ci
npm --prefix editors/vscode run compile
```

Package a `.vsix`:

```bash
npm --prefix editors/vscode run package
```

Key settings:

- `thrift.server.path`.
- `thrift.server.args`.
- `thrift.format.lineWidth`.
- `thrift.trace.server`.
- `thrift.managedInstall.enabled`.
- `thrift.managedInstall.manifestUrl`.
- `thrift.managedInstall.allowInsecureHttp`.

Managed install is on by default. If it fails and `thrift.server.path` is set, the extension uses that path.

## Config Notes

- Formatter defaults: line width `100`, indent `2` spaces, max blank lines `2`.
- Mixed newline files are normalized to the main style (`LF` or `CRLF`) with an info message.
- Invalid UTF-8 input is refused for formatting.

## Contributing

Start with the RFC, then code.

Daily commands:

```bash
mise run fmt
mise run lint
mise run test
mise run ci
```

If you change behavior or policy, update [RFC 0001](docs/rfcs/0001-thrift-tooling-platform.md) in the same PR.

## Repository Map

- `cmd/`: CLI entry points (`thriftfmt`, `thriftls`).
- `internal/`: core engine packages (`text`, `lexer`, `syntax`, `format`, `lsp`).
- `grammar/tree-sitter-thrift/`: Thrift grammar and generated parser assets.
- `editors/vscode/`: VS Code extension client.
- `testdata/`: formatter fixtures, LSP scenarios, and corpus files.
- `docs/`: RFCs, architecture notes, user guide, and release policy.

## More Docs

- [RFC 0001](docs/rfcs/0001-thrift-tooling-platform.md)
- [User Guide](docs/user-guide.md)
- [Architecture Overview](docs/architecture.md)
- [WASM Grammar Build Pipeline](docs/wasm-grammar-build.md)
- [Performance Benchmarks](docs/performance.md)
- [Robustness (fuzzing and race tests)](docs/robustness.md)
- [Release Process](docs/release.md)
- [Linux managed-binary compatibility](docs/linux-managed-binary-compatibility.md)
