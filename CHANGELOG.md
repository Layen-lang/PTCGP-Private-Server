# Changelog

All notable project changes are documented here. The first three version
components identify the compatible game version; the fourth is the project
revision for that game version.

## Unreleased

## [1.7.2.0] - 2026-09-14

This is the first public release of PTCGP Private Server.

### Added

- Local Player API emulation for the 1.7.2 client profile.
- Persistent multi-profile administration, inventory editing, pack rules,
  traffic inspection, and operation logs.
- Windows control panel with system-tray actions.
- English and French control-panel translations, with English as the
  default for new installations and localized catalog names in administration.
- Emulator-independent handling for native libraries stored inside split
  APKs and root-only Android certificate destinations.
- Curated runtime master data and artwork required by the local
  server.
- Synthetic test fixtures that do not contain player or account data.
- Automated CI and Windows release packaging.

### Changed

- Reduced routine emulator monitoring to `get-state` and `pidof` every ten
  seconds, while retaining complete checks at startup and after control actions.
- Generalized the fallback ADB tunnel for rooted Android emulators whose
  `iptables` build does not support owner-scoped rules.
- Refined the appearance settings and removed the named PTCGP theme presets.
- Prefer source execution from `start-server.cmd` when a development checkout
  is present, while published installations continue to use bundled binaries.
- Install frontend dependencies automatically when a source checkout needs to
  rebuild the embedded control panel.
- Updated gRPC and its transitive dependencies to patched versions.

### Fixed

- Reuse the most recently authorized local profile when a newly observed device
  logs in instead of creating an unnecessary account.
- Preserve Android setup and rollback across emulator-specific package layouts
  and unprivileged ADB daemon configurations.
- Prevent published Windows installations from trying to rebuild the embedded
  frontend with `npm` when source files are not included.

### Security

- Services bind to the loopback interface by default.
- Runtime databases, certificates, captures, binaries, and personal data are
  excluded from version control.
- Publication checks reject private files, oversized files, personal paths,
  and paths that are not portable to default Windows Git checkouts.
