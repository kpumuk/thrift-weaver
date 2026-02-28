# Release Process (Beta)

This project currently publishes **unsigned beta artifacts** with mandatory SHA-256 checksums and GitHub artifact attestations (build provenance).

## Published artifacts

- `thriftfmt` archives (per platform)
- `thriftls` archives (per platform)
- `checksums.txt` (SHA-256 for all release artifacts)
- `thriftls-manifest.json` (managed-install manifest for the VS Code extension)
- `thrift-weaver-vscode-<version>.vsix`
- `vscode-release-metadata.json` (extension compatibility metadata)
- VS Code extension publication to:
  - Visual Studio Marketplace
  - Open VSX Registry
- GitHub artifact attestations for published release artifacts (provenance)

## Supply-chain policy (beta)

- Artifacts are published over GitHub Releases (HTTPS).
- `thriftls` managed-install clients must verify SHA-256 checksums against `checksums.txt` / manifest entries.
- GitHub Actions release workflow emits artifact attestations for release archives, checksums, `.vsix`, and generated metadata/manifest files.
- The VS Code extension release metadata includes the `.vsix` checksum and the referenced `thriftls` manifest schema version.
- Platform code signing / notarization is **not yet required** for beta. This remains an explicit temporary policy tracked in the RFC and execution plan.
- Linux managed-binary compatibility policy is documented in `docs/linux-managed-binary-compatibility.md`.

## Manual verification

Verify checksums locally after download:

```bash
sha256sum -c checksums.txt
```

Inspect manifest entries:

```bash
jq '.platforms[] | {os, arch, filename, sha256, size_bytes}' thriftls-manifest.json
```

Verify GitHub attestation (example with GitHub CLI):

```bash
gh attestation verify thriftls_0.1.0_darwin_arm64.tar.gz --repo kpumuk/thrift-weaver
```

## Notes

- Windows `arm64` binary publication is best-effort.
- The release workflow runs GoReleaser on a standard Ubuntu runner and packages pure-Go (`CGO_ENABLED=0`) binaries.
- Beta release notes should include the Linux compatibility snippet/policy callout from `docs/linux-managed-binary-compatibility.md`.
- Beta release notes should include a performance summary (parse/format p50/p95 + LSP memory loop) generated per `docs/performance.md`.

## Marketplace publishing setup (one-time)

The release workflow publishes the same `.vsix` to both extension registries. Configure these secrets in the GitHub `release` environment:

- `VSCE_PAT`: Visual Studio Marketplace personal access token
- `OVSX_PAT`: Open VSX personal access token

### Visual Studio Marketplace (`VSCE_PAT`)

1. Create/update a publisher in the Visual Studio Marketplace (`kpumuk`).
2. Generate a publisher PAT with extension publish permissions.
3. Store the PAT as `VSCE_PAT` in the GitHub `release` environment.

### Open VSX (`OVSX_PAT`)

1. Sign in to [open-vsx.org](https://open-vsx.org/) (GitHub auth is supported).
2. Create/confirm the `kpumuk` namespace (must match `publisher` in `editors/vscode/package.json`).
3. Generate a PAT allowed to publish under that namespace.
4. Store the PAT as `OVSX_PAT` in the GitHub `release` environment.

Optional local verification:

```bash
npx --yes ovsx verify-pat kpumuk -p "$OVSX_PAT"
```
