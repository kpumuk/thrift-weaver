# Changelog

All notable changes to the VS Code extension are documented in this file.

The changelog covers:

- extension client changes
- managed-install behavior changes
- user-visible `thriftls` changes that ship through the extension
- marketplace and distribution changes relevant to extension users

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- Avoided a `thriftls` shutdown race that could interrupt workspace diagnostics during extension use.


## [v0.2.1] - 2026-03-14

- No extension-specific user-visible changes in this release.


## [v0.2.0] - 2026-03-13

- No extension-specific user-visible changes in this release.


## [v0.1.1] - 2026-03-12

### Fixed

- Corrected `Thift` to `Thrift` in extension-facing branding and copy.


## [v0.1.0] - 2026-03-11

### Added

- Shipped workspace-index-backed language-server improvements through the extension, including cross-file workspace behavior in `thriftls`.
- Added the `thrift.workspace.indexWorkers` setting for tuning workspace indexing parallelism.

### Changed

- Rebranded the extension to **Weaver for Apache Thrift**, including updated command text and extension copy.
- Refreshed the extension icon and branding assets.

### Fixed

- Surfaced duplicate field ID diagnostics through the shipped language server.


## [v0.0.5] - 2026-02-28

### Changed

- Managed-install downloads now use pure-Go `thriftls` release artifacts, simplifying runtime portability.
- No extension-specific user-visible changes in this release.


## [v0.0.4] - 2026-02-27

### Changed

- Published the extension to Open VSX in addition to the existing distribution channels.


## [v0.0.3] - 2026-02-27

### Changed

- Published the extension to the Visual Studio Marketplace.
- Updated installation and documentation for marketplace-based installs.


## [v0.0.2] - 2026-02-27

### Added

- Added managed `thriftls` install with automatic download and checksum verification.
- Added fallback to `thrift.server.path` when managed install is unavailable.
- Added managed-install settings for enable/disable, manifest URL, and local insecure-HTTP testing.


## [v0.0.1] - 2026-02-25

### Added

- Initial VS Code extension release for Thrift.
- Added syntax highlighting, language configuration, the restart command, and `thriftls` client wiring.
- Set two-space editor defaults for `.thrift` files.

### Changed

- Shipped extension packages as `.vsix` assets on GitHub releases.

[Unreleased]: https://github.com/kpumuk/thrift-weaver/compare/v0.2.1...HEAD
[v0.2.1]: https://github.com/kpumuk/thrift-weaver/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/kpumuk/thrift-weaver/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/kpumuk/thrift-weaver/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/kpumuk/thrift-weaver/compare/v0.0.5...v0.1.0
[v0.0.5]: https://github.com/kpumuk/thrift-weaver/compare/v0.0.4...v0.0.5
[v0.0.4]: https://github.com/kpumuk/thrift-weaver/compare/v0.0.3...v0.0.4
[v0.0.3]: https://github.com/kpumuk/thrift-weaver/compare/v0.0.2...v0.0.3
[v0.0.2]: https://github.com/kpumuk/thrift-weaver/compare/v0.0.1...v0.0.2
[v0.0.1]: https://github.com/kpumuk/thrift-weaver/releases/tag/v0.0.1
