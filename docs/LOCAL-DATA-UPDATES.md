# Local preparation and integrated updates

The Windows launcher opens its control panel before loading game data. Local
mode requires validated master data and images plus a connected rooted Android
device (emulator or physical phone). The selected device's game version and
library are checked before Local mode starts. Accounts and settings remain
under the original installation directory.

## First launch

1. Install the official game and finish its resource download.
2. Enable ADB and root on the Android device, connect it, then open `start-server.cmd`.
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

The launcher checks the release manifest on opening and every six hours while
open. Downloads are automatic. Ed25519 verifies the manifest, and SHA-256 plus
exact sizes verify each component. Files already matching the new release are
reused. Unsigned, damaged, incompatible, or incomplete downloads are not run.
The latest release must support the installed game; automatic downgrades are
refused. If it does not, the current installation is preserved.

When a download is ready, a theme-colored **Update and restart** button (or **Mettre à jour
et redémarrer** in French) appears at the bottom of the sidebar. On mobile it
appears above navigation. Download progress uses the same control. Every update,
including a change to extraction profiles, waits for this action.

The launcher restores all managed Android devices, stops their game servers, and starts
a detached temporary installer. After the old panel exits, the installer backs
up the accounts database and affected files, then replaces `bin/`, `profiles/`, `VERSION`
and compatibility metadata in `server.json` in the original installation.
Local runtime paths, Android settings, accounts and certificates are preserved.
The installer starts the panel again automatically; the browser reloads only
after a different process reports the expected installed version. Startup failure
restores the previous files and database and restarts the previous panel.

`data/updates/versions/` holds verified downloads, and `data/updates/backup-*`
holds original files for recovery. Active program files live at the root; the
root `VERSION` now reflects the installed release. A legacy `current.json`
selector is removed during installation and restored if rollback is needed.
Users keep the same shortcut. The Android game is never updated by this system.

After the replacement panel confirms its health and the installer exits, cleanup
removes the downloaded releases and temporary installers present before restart.
Only the most recent program backup and its accounts database backup are retained.
New downloads started by the replacement panel are preserved. The installer log
keeps its latest 1 MiB, trimmed before installation and during successful cleanup.
The launcher startup log is also trimmed to its latest 1 MiB before each update
restart, including rollback restarts.
Failed installations retain their recovery files. Interrupted cleanup is retried
on the next root launcher startup; cleanup failure does not roll back a healthy panel.

When an older installer places this launcher in a version directory, the new
launcher offers the already verified local release through **Update and restart**
to finish copying it to the root. This migration works even when that release is
already the latest one or the network is unavailable; no new download is needed.

Older distributions need one manual move to this launcher. Keep a backup before
that first migration. Subsequent updates operate in the same installation.
The previous data generation and program backup are retained; no Git history is rewritten.

## Building and publishing

Development adds Rust to the existing Go/Node requirements:

In a source checkout, `start-server.cmd` checks the native sources, Cargo manifests,
lockfiles and build script alongside the existing frontend and Go build checks.
It recompiles changed or missing native tools before launching the panel. Unchanged
tools are reused. Packaged releases continue to use precompiled executables.

```powershell
rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl
./native/build.ps1
npm --prefix web run build
go test ./...
cargo test --locked --manifest-path native/importer/Cargo.toml
```

The release workflow packages the Go executables, Rust importer, both readers,
and `profiles/`. It excludes `game-data/`; those files are now ignored locally.
Tests use synthetic master-data fixtures and do not require the game assets.

Before the first signed release, configure these repository settings:

- variable `PTCGP_UPDATE_PUBLIC_KEY`: base64 Ed25519 public key, 32 bytes;
- secret `PTCGP_UPDATE_SIGNING_SEED`: matching base64 Ed25519 seed, 32 bytes.

The workflow embeds only the public key in the launcher. `ptcgp-release` reads
the seed from its environment to sign `update.json`; it does not print the seed.
The workflow refuses publication without matching signing configuration.
Source builds without a public key keep local preparation functional and report
that automatic signed updates are not configured. No production signing secret
is generated or committed by the implementation.

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
Unit tests cover unknown game versions, architecture detection, invalid update
signatures and paths, modified downloads, generation identity and damaged files.

A forced importer termination preserved 515 completed images and the master data.
Retry imported the remaining 4,486 images, with no output differences. Damaging
one PNG then triggered extraction of exactly one image and reused every master
table. The optimized repair took 7.80 seconds including validation and publication.
These checks are recorded under `.tmp/bootstrap-resume`. Installer tests start
real isolated Windows subprocesses and verify both activation and database rollback
after a simulated failed migration.
