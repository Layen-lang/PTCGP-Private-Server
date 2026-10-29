# Changelog

All notable project changes are documented here. The first three version
components identify the compatible game version; the fourth is the project
revision for that game version.

## Unreleased

## [1.8.1.0] - 2026-10-29

### Changed

- Adopt the Pokémon TCG Pocket 1.8.0 Player API protocol: drop the removed
  `Echo/EchoV1` and `ItemShop/GetPurchaseSummariesV1` handlers.

### Added

- Share private match rules between local players by room password, list
  friends' first decks as someone decks, and answer the new versus battle
  record lists (empty locally).
- Report the weekly special pack right as available; special pack openings are
  refused until they are modelled locally.

### Fixed

- Illegal mode no longer fails when its filters select a rarity absent from
  the opened pack, such as the special-pack-only rarity of 1.8.0; those cards
  are drawn uniformly.

## [1.7.5.2] - 2026-10-14

### Added

- Document successful testing on a rooted physical Android phone, including
  Wi-Fi ADB, with USB, wireless pairing, and USB-assisted TCP connection steps.
- Keep a device selector beside Refresh, remember the selection automatically,
  and discover LDPlayer 9 alongside MuMu when their ADB ports overlap.
- Run separate game servers and accounts on multiple emulators concurrently,
  with independent routing, traffic history, and Android restoration.
- Add a separate control-panel listener for home Wi-Fi access, with private
  IPv4 client checks and the panel's existing origin and CSRF protection.

### Changed

- Apply Local and Official modes only to the selected emulator; Stop all
  restores all managed emulators and stops their servers.
- Prevent a local account from running on two emulators at once and show which
  emulator is using it in the account list.
- Reuse prepared game data when switching emulators or accounts, while checking
  each installed game before enabling Local mode.

### Fixed

- Keep automatic updates from 1.7.5.1 compatible with the new Wi-Fi panel
  setting, and let replacement launchers validate future release options.
- Restart the previous panel when update preparation fails before replacement.
- Label Android hosts overlays correctly for SELinux before mounting them, so
  rooted physical phones can resolve the game's local endpoints.
- Keep process ownership, account credentials, and session invalidation scoped
  to each device, including aliases for the same Android instance.
- Resume pending Android restoration when an unselected device reconnects.
- Offer completion of legacy update installations without hiding the action
  during automatic update discovery.
- Clean obsolete update downloads, installers, and older backups after a
  successful update while retaining the latest recovery backups and limiting
  updater log size.

## [1.7.5.1] - 2026-10-01

### Changed

- Show update availability and download progress in the sidebar and mobile
  control panel, with installation actions disabled during restart.
- Require confirmation for every program update, including compatibility-only
  updates.

### Fixed

- Detach the Windows update installer so it survives the launcher shutdown.
- Replace installed program files at the installation root while preserving
  local settings, accounts, and compatibility with legacy update selections.
- Restore program files and accounts when the updated panel fails to start,
  and retain installer diagnostics for troubleshooting.
- Wait for a different launcher process serving the expected version before
  reloading the control panel after an update.

## [1.7.5.0] - 2026-09-30

### Changed

- Update the client compatibility profile, protocol schemas, image extraction
  plans, and patch metadata for Pokémon TCG Pocket 1.7.5.
- Resolve master data and images from a validated local generation instead of
  requiring paths in the bundled configuration.
- Remove older completed data generations after a new generation is validated.
- Keep image import receipts reusable across profile JSON field-order changes.

### Fixed

- Inspect native libraries inside installed APKs when Android has not extracted
  them yet, preserving compatibility hash validation before preparation.
- Accept event booster SKUs without an expansion in the 1.7.5 master data while
  continuing to reject unknown expansion and SKU references.

## [1.7.2.2] - 2026-09-26

### Added

- Import game images and master data from the installed game on first launch,
  with progress, resumable preparation, and validated local generations.
- Download signed program updates and install them after confirmation, with
  startup validation and rollback if the new version fails.
- Package precompiled Rust extraction tools and compatibility profiles instead
  of distributing the game's artwork and tables.

### Changed

- Open the control panel before data preparation and restore validated data
  without requiring an emulator connection.
- Support compatible 64-bit x86-64 and ARM64 Android systems, with device
  selection when several emulators are connected.
- Update installation, usage, troubleshooting, configuration, and development
  guides for local preparation and integrated updates.

### Fixed

- Preserve binary data streams through root shells on LDPlayer and make
  Android setup and deferred restoration more resilient.

## [1.7.2.1] - 2026-09-21

### Added

- Master-data-driven tutorial routes, including route-specific card packs,
  decks, feed cards, rewards, and persistent route selection.

### Fixed

- Install the local certificate in the Conscrypt APEX and Android app mount
  namespaces used by Android 14 and newer, while retaining the legacy trust
  store for older emulators.
- Deduplicate multiple ADB transports that identify the same running emulator,
  including recent MuMu releases without `ro.serialno`.

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
