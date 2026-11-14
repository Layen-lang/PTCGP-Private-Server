# Local preparation and integrated updates

The Windows launcher opens its control panel before loading game data. Local
mode requires validated master data and images plus a connected rooted Android
device (emulator or physical phone). The selected device's game version and
library are checked before Local mode starts. Accounts and settings remain
in the installation folder, outside the program files.

## First launch

1. Install the official game and finish its resource download.
2. Enable ADB and root on the Android device, connect it, then open `ptcgp-private-server.exe`.
3. If several devices are connected, select the one containing the game.
4. Wait for preparation to import all nine locales and the images in the
   published compatibility profile. The Accounts page opens automatically
   after validation. Then select **Local** to start the private server.

On later launches, a validated generation opens Accounts immediately, even
when the device is offline. Reconnect the device before selecting **Local**.
The imported generation is shared by all devices. Switching devices or
accounts does not repeat preparation; the preparation page is needed only when
the imported data is missing or invalid.
If preparation fails, finish any missing resource download in the official
game and select **Retry preparation** (or **Réessayer la préparation** in French)
in the panel. A retry reuses completed work. An unsupported game version needs
a compatible project release.

## How local preparation works

The importer is a precompiled Rust program. Users do not install Rust, Python,
AssetRipper, or the datamining project. ADB must be available on PATH, as in
previous releases. It reads installed APKs individually and only transfers the
required bundles. It does not download another copy of the APK or all assets.

The reader is selected from the Android system ELF architecture, rather than
`uname` or the translated game's ABI. Both x86-64 and ARM64 readers are built.
The implementation uses Android/ADB capabilities, not a MuMu-specific protocol.
MuMu has been exercised end to end. A rooted physical Android phone has also
been tested successfully, including over Wi-Fi ADB; see
[phone connection steps](INSTALLATION.md#physical-android-phone-usb-or-wi-fi).
BlueStacks, LDPlayer, Nox and MEmu require
real-device validation before their particular versions can be listed as tested.
ADB, root, a supported game build, and the existing routing/mount requirements
remain mandatory. A 64-bit game running through native translation is distinct
from a 64-bit emulator system.

Prepared data lives under `data/generations/<profile-hash>/`. An incomplete
generation uses a `.pending` directory and is never exposed to the server.
Verified images are checkpointed during import; a retry reuses them. Completed
generations can share unchanged files through hard links, with copying as a
fallback. Reuse depends on source identities and converter versions, not just
filenames. File size/modification checks trigger hash validation when necessary.
Once a generation is published, the importer removes its checkpoint, work plan,
temporary stream manifests and diagnostic reports. Incomplete `.pending`
generations retain their checkpoint for recovery. Once a new generation is
published and validated, older completed generations are removed. If cleanup
is interrupted, it is retried on the next successful startup.

The launcher checks game compatibility again before local mode, and the server
also checks it when started directly. An unsupported version or library hash is
rejected before starting the TLS server or applying the Android patch. Missing
resources require finishing the download in the official game. Unknown Unity
schemas require an updated importer; the parser does not guess their layout.
The preparation page appears only for missing or invalid data.

## Program updates

Updates are handled by [Velopack](https://velopack.io). The desktop app checks
this project's GitHub releases when it opens and every six hours, downloads a
newer version in the background (a small delta when possible) and verifies its
checksum against the release feed. Nothing is installed during the download.

When the download is ready, a theme-colored **Update and restart** button (or
**Mettre à jour et redémarrer** in French) appears at the bottom of the
sidebar, with the download progress shown in the same place before that.

Selecting it restores every managed Android device to the official game and
stops the backend. Velopack's `Update.exe` then replaces the whole `current`
folder with the new version and starts the app again. Because the folder is
replaced as a whole, a release can add, rename or remove any program file
without a change to the updater.

User data is outside `current` and is never touched: `data/` (accounts,
imported game data, logs), `certs/` and the optional `server.local.json`.
The new version's `server.json` replaces the old one, so new settings and
compatibility metadata always come from the release; local changes belong in
[`server.local.json`](CONFIGURATION.md#local-settings).

The first time a new version starts, the backend copies the accounts database
to `data/backups/accounts-before-<version>.db` before opening it. Only the
latest copy is kept. The Android game is never updated by this system.

A build run from the source tree is not an installation and does not update
itself.

### Moving from 1.8.1.0 or an older ZIP

Older ZIP installations cannot update themselves to this layout. Once:

1. Restore **Official** mode, select **Stop all** and close the old app.
2. Install the new release.
3. Close it, then copy the old `data` and `certs` folders into the new
   installation folder (`%LocalAppData%\PTCGPPrivateServer`), next to
   `Update.exe`. `data\updates` belongs to the old updater and can be deleted.
4. If you had edited the old `server.json`, put only those values in
   `server.local.json`. Do not copy the old file.

## Building and publishing

Development needs Go and Rust:

`native/build.ps1` builds the two Android readers into `native/target/readers`.
The desktop app compiles them in, together with the importer, so a release ships
no native tool as a separate file.

```powershell
rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl
./native/build.ps1
go test ./...
cargo test --locked --manifest-path native/importer/Cargo.toml
```

The release workflow packages the Go executables, Rust importer, both readers,
and `profiles/`. It excludes `game-data/`; those files are now ignored locally.
Tests use synthetic master-data fixtures and do not require the game assets.

`desktop/package.ps1` stages the program folder and runs `vpk pack`. The
package version has three numbers, so the game patch and the project revision
share the last one: `1.8.1.3` is packaged as `1.8.103`. `VERSION` and the app
keep showing four numbers. Keep the `vpk` version in the workflow equal to the
`velopack` crate version in `desktop/Cargo.toml`.

Everything in the staged folder is shipped. To ship a new file or folder, add
it to the staged folder in `desktop/package.ps1`; the updater needs no change.

To try an update before publishing, package two versions into one folder,
install the first from its portable archive and point the app at that folder:

```powershell
dotnet tool install -g vpk --version 1.2.161
./desktop/build.ps1
./desktop/package.ps1 -Version 1.8.1.0 -Output C:\tmp\feed
./desktop/package.ps1 -Version 1.8.1.1 -Output C:\tmp\feed
$env:PTCGP_UPDATE_SOURCE = 'C:\tmp\feed'
```

Releases are not code-signed; Windows SmartScreen may warn on the installer.

Each supported game revision needs a validated extraction plan and image index
in `profiles/`, plus matching release metadata in `server.json`. Plans contain
asset addresses/hashes and conversion instructions, not image pixels or table
contents. Preserve 64-bit JSON integers exactly when generating them. Updating
only these files can avoid rebuilding executables; a converter change must also
bump its cache identity in `internal/provision`.

## Verification performed during integration

On the available MuMu installation, the integrated import produced 5,001 PNGs
and 1,930 master-data JSON files byte-identical to the optimized reference.
Image acquisition/conversion took 18.09 seconds. Rechecking an already prepared
installation took 1.52 seconds including HTTP polling. These are warm-system
measurements on the development machine, not universal startup guarantees.

The test installation and detailed comparison remain in `.tmp/bootstrap-install`.
Unit tests cover unknown game versions, architecture detection, generation
identity and damaged files.

A forced importer termination preserved 515 completed images and the master data.
Retry imported the remaining 4,486 images, with no output differences. Damaging
one PNG then triggered extraction of exactly one image and reused every master
table. The optimized repair took 7.80 seconds including validation and publication.
These checks are recorded under `.tmp/bootstrap-resume`.
