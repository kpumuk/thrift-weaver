# Linux Managed `thriftls` Binary Compatibility Policy (Beta)

This document defines the Linux compatibility policy for the VS Code extension's planned managed `thriftls` install flow.

Status:
- Managed install is planned (not yet enabled in the extension)
- This policy governs release artifacts and user guidance in preparation for that feature

## Scope

This policy applies to Linux artifacts published for managed installation of `thriftls`.

It does not change support for the external-path workflow (`thrift.server.path`), which remains the fallback for all Linux environments.

## Beta Baseline Policy (Linux)

Managed Linux `thriftls` binaries are published for:

- `linux/amd64`
- `linux/arm64`

Beta compatibility target:

- glibc-based distributions with **glibc >= 2.28**

Practical examples (guidance, not an exhaustive list):

- generally expected to work on modern Debian/Ubuntu/RHEL-family distributions meeting the glibc floor
- may fail on older enterprise images and minimal/musl-based environments (for example Alpine)

## Non-Goals (Beta)

Not guaranteed in beta managed-install flow:

- musl/Alpine compatibility
- fully static Linux binaries
- distro-package integration

Users in those environments should use the external-path workflow.

## Fallback Guidance (Required UX / Docs)

If managed install download/verification/launch fails on Linux, the user guidance should always include:

1. Set `thrift.server.path` to a manually installed `thriftls` binary
2. Verify the downloaded artifact with `checksums.txt`
3. If needed, build `thriftls` from source for the local environment

Suggested fallback commands:

```bash
git clone https://github.com/kpumuk/thrift-weaver.git
cd thrift-weaver
mise install
go build -o thriftls ./cmd/thriftls
```

## Release Notes Guidance (Required for Beta Releases)

Each beta release should include a short Linux compatibility note covering:

- Linux managed binary target is glibc `>= 2.28`
- `linux/amd64` and `linux/arm64` artifacts are published
- Alpine/musl users should use external `thrift.server.path`
- Windows `arm64` remains best-effort (separate policy note)

Suggested release-note snippet:

> Linux managed-install binaries target glibc >= 2.28 (`linux/amd64`, `linux/arm64`).
> If your distro is older or musl-based (e.g. Alpine), use the external `thrift.server.path` workflow or build `thriftls` locally.

## Future Revisions (Post-Beta)

Possible future improvements:

- publish separate musl Linux artifacts
- runtime compatibility smoke tests across pinned distro images
- stronger policy tied to automated ABI verification
- managed-install preflight checks with clearer distro-specific diagnostics
