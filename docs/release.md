# Release Process (Beta)

This project publishes **unsigned beta artifacts** with mandatory SHA-256 checksums and GitHub artifact attestations (build provenance).

## Release flow

1. Merged pull requests on `main` are classified by `release-please` from their squash-merge title.
2. `release-please` keeps a release PR open with the next version, the extension package version bump, and the current release summary.
3. Merging the release PR creates the `vX.Y.Z` tag and the matching GitHub release shell.
4. The tag-triggered release workflow builds artifacts with GoReleaser, packages the VS Code extension, publishes the extension registries, and rewrites the GitHub release body with the final release notes.

The release workflow still runs on `push.tags: v*`. The difference is that tags are created by the release PR merge instead of being pushed manually.

## Release inputs

- Release-driving metadata comes from Conventional Commit-style **PR titles**, not from individual local commits.
- Pull requests are expected to be squash-merged, with the squash commit title set to the PR title.
- Breaking changes must use `!` in the PR title or a `BREAKING CHANGE:` footer.
- VS Code user-facing release notes are authored in `editors/vscode/CHANGELOG.md` under `## [Unreleased]` and rolled forward automatically when a release PR is prepared.

## Published artifacts

- `thriftfmt` archives (per platform)
- `thriftlint` archives (per platform)
- `thriftls` archives (per platform)
- `checksums.txt` (SHA-256 for all release artifacts)
- `thriftls-manifest.json` (managed-install manifest for the VS Code extension)
- `thrift-weaver-vscode-<version>.vsix`
- `vscode-release-metadata.json` (extension compatibility metadata)
- `sbom.cdx.json` (CycloneDX SBOM for the release)
- VS Code extension publication to:
  - Visual Studio Marketplace
  - Open VSX Registry
- GitHub artifact attestations for published release artifacts (provenance)

## Release notes

The final GitHub release body is composed from:

- the release-please release summary
- the Linux compatibility snippet required by `docs/linux-managed-binary-compatibility.md`
- a performance summary rendered from `scripts/perf-report`

After publication, the release workflow also comments on the merged pull requests included in the release with the published version and release link.

## Supply-chain policy (beta)

- Artifacts are published over GitHub Releases (HTTPS).
- `thriftls` managed-install clients must verify SHA-256 checksums against `checksums.txt` / manifest entries.
- GitHub Actions release workflow verifies release checksums before publishing release metadata.
- GitHub Actions release workflow generates a CycloneDX SBOM for the release payload.
- GitHub Actions release workflow emits artifact attestations for release archives, checksums, `.vsix`, generated metadata/manifest files, and the SBOM.
- The VS Code extension release metadata includes the `.vsix` checksum and the referenced `thriftls` manifest schema version.
- Platform code signing / notarization is **not yet required** for beta. This remains an explicit temporary policy tracked in the RFC and execution plan.
- Linux managed-binary compatibility policy is documented in `docs/linux-managed-binary-compatibility.md`.
- Release artifacts are pure-Go (`CGO_ENABLED=0`) binaries with the parser wasm embedded in the executable.

## Required GitHub secrets

Configure these secrets before enabling the automated flow:

- `RELEASE_PLEASE_TOKEN`: personal access token used by the release-please workflow so release PRs and tags can trigger downstream workflows
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

Generate a local SBOM from the current module graph:

```bash
mise run generate-sbom
```

## Notes

- Windows `arm64` binary publication is best-effort.
- The release workflow runs GoReleaser on a standard Ubuntu runner and packages pure-Go (`CGO_ENABLED=0`) binaries.
- The release workflow verifies generated checksums and emits `dist/sbom.cdx.json` before uploading release metadata.
- Beta release notes must include the Linux compatibility snippet from `docs/linux-managed-binary-compatibility.md`.
- Beta release notes must include a performance summary (parse/format p50/p95 + LSP memory loop) generated per `docs/performance.md`.
