<h3 align="center">
	<img src="https://raw.githubusercontent.com/kpumuk/thrift-weaver/main/editors/vscode/media/icon.png" width="100" alt="Logo"/><br/>
	Thrift Weaver for <a href="https://code.visualstudio.com">VSCode</a>
</h3>

<p align="center">
    <a href="https://github.com/kpumuk/thrift-weaver/stargazers"><img src="https://img.shields.io/github/stars/kpumuk/thrift-weaver?colorA=363a4f&colorB=b7bdf8&style=for-the-badge"></a>
    <a href="https://github.com/kpumuk/thrift-weaver/issues"><img src="https://img.shields.io/github/issues/kpumuk/thrift-weaver?colorA=363a4f&colorB=f5a97f&style=for-the-badge"></a>
    <a href="https://github.com/kpumuk/thrift-weaver/contributors"><img src="https://img.shields.io/github/contributors/kpumuk/thrift-weaver?colorA=363a4f&colorB=a6da95&style=for-the-badge"></a>
</p>

Thrift Weaver adds Thrift support to VS Code.

## Why This Extension

- See errors while you type.
- Format whole files or a selected range.
- Use symbols, folds, and semantic tokens.
- Run with managed `thriftls` or your own `thriftls` path.

## Install

### Option 1: Visual Studio Marketplace

1. Open the Extensions view in VS Code.
2. Search for `kpumuk.thrift-weaver-vscode`.
3. Click **Install**.

Marketplace page: [Thrift Weaver](https://marketplace.visualstudio.com/items?itemName=kpumuk.thrift-weaver-vscode)

### Option 2: Open VSX Registry

Install from Open VSX (useful for VS Code-compatible editors like VSCodium):

[Thrift Weaver on Open VSX](https://open-vsx.org/extension/kpumuk/thrift-weaver-vscode)

### Option 3: Install `.vsix`

```bash
code --install-extension thrift-weaver-vscode-<version>.vsix --force
```

## Quick Start

1. Open any `.thrift` file.
2. Run **Format Document** or **Format Selection**.
3. If setup fails, set `thrift.server.path` to your local `thriftls` binary.

> [!IMPORTANT]
> Managed install is on by default. The extension downloads a matching `thriftls` binary and checks its SHA-256 hash.

## Features

- Syntax highlighting.
- Diagnostics.
- Document format and range format.
- Document symbols.
- Folding ranges.
- Selection ranges.
- Semantic tokens.
- Command: `Thrift: Restart Language Server`.

## Settings

All settings start with `thrift.`.

Main settings:

- Server path: use your own `thriftls` binary.
- Server args: pass extra args to `thriftls`.
- Line width: set your preferred wrap width.
- Trace level: choose `off`, `messages`, or `verbose`.
- Managed install: choose if auto install is on.
- Manifest URL: set where install info comes from.
- Insecure HTTP: allow only for local tests.

### Example `settings.json`

```json
{
  // Path to local thriftls (empty = managed install path)
  "thrift.server.path": "",
  // Extra args for thriftls
  "thrift.server.args": [],
  // Preferred line width for formatting
  "thrift.format.lineWidth": 100,
  // LSP trace level
  "thrift.trace.server": "off",
  // Enable managed thriftls install
  "thrift.managedInstall.enabled": true,
  // Managed install manifest URL
  "thrift.managedInstall.manifestUrl": "https://github.com/kpumuk/thrift-weaver/releases/latest/download/thriftls-manifest.json",
  // Allow HTTP only for local testing
  "thrift.managedInstall.allowInsecureHttp": false
}
```

## Managed Install Notes

Managed install flow:

1. Download manifest.
2. Pick your platform artifact.
3. Download and verify hash.
4. Install with rollback.

> [!WARNING]
> Non-HTTPS URLs are blocked unless `thrift.managedInstall.allowInsecureHttp` is set to `true`.

## Troubleshooting

### No errors or format

1. Open Output panel and check:
   - `Thrift Weaver`
   - `Thrift Weaver LSP Trace`
2. Run **Thrift: Restart Language Server**.
3. Confirm the mode is `Thrift`.
4. If needed, set `thrift.server.path`.

### Managed install fails

1. Check `thrift.managedInstall.manifestUrl`.
2. Use HTTPS unless this is local testing.
3. Check network access from VS Code.
4. Set `thrift.server.path` as fallback.

### Server starts, then stops

1. Set `thrift.trace.server` to `verbose`.
2. Restart the language server.
3. Share logs in a GitHub issue.

Issues: [github.com/kpumuk/thrift-weaver/issues](https://github.com/kpumuk/thrift-weaver/issues)
