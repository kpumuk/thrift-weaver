# Release Process (Beta)

This project currently publishes **unsigned beta artifacts** with mandatory SHA-256 checksums and GitHub artifact attestations (build provenance).

## Published artifacts

- `thriftfmt` archives (per platform)
- `thriftls` archives (per platform)
- `checksums.txt` (SHA-256 for all release artifacts)
- `thriftls-manifest.json` (managed-install manifest for the VS Code extension)
- `thrift-weaver-vscode-<version>.vsix`
- `vscode-release-metadata.json` (extension compatibility metadata)
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
- The release workflow uses `goreleaser-cross` to build `cgo`/tree-sitter targets in a single Linux job.
- Beta release notes should include the Linux compatibility snippet/policy callout from `docs/linux-managed-binary-compatibility.md`.
- Beta release notes should include a performance summary (parse/format p50/p95 + LSP memory loop) generated per `docs/performance.md`.
