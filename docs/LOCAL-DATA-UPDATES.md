# Local preparation and integrated updates

The Windows launcher opens its control panel before loading game data. Local
mode stays disabled until the selected emulator, game profile, master data and
image generation have passed validation. Accounts and settings remain under
the original installation directory.

## First launch

1. Install the official game and finish its resource download.
2. Enable ADB and root in the emulator, then open `start-server.cmd`.
3. If several emulators are connected, select the one containing the game.
4. Wait for preparation to import all nine locales and the images in the
   published compatibility profile. The Accounts page opens automatically
   after validation. Then select **Local** to start the private server.

On later launches, a validated generation opens Accounts immediately, even
when the emulator is offline. Reconnect the emulator before selecting **Local**.
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
MuMu has been exercised end to end; BlueStacks, LDPlayer, Nox and MEmu require
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

Changes to executable code require **Update and restart** (or **Mettre à jour et
redémarrer** in French). The launcher restores official mode, stops the game
server, and starts a temporary installer.
After the old panel exits, the installer backs up the accounts database, selects
the prepared release, and starts its panel. Health must come from that exact
new process. Startup failure restores the previous program selection and database.

A profile-only release may activate automatically while the server is stopped,
provided every executable remains byte-identical and the compiled protocol
accepts the new profile. Extraction then prepares the matching data. A newer
game protocol can still require changes to the server; automatic downloading
does not make unknown protocols compatible.

Program versions live under `data/updates/versions/`. `current.json` selects the
active version. The original entry point follows this selection, so users keep
the same shortcut. Published client/contract/patch metadata is loaded from that
version; local runtime paths, serial selection, accounts and certificates stay
in the original installation. The Android game is never updated by this system.

Older distributions need one manual move to this launcher. Keep a backup before
that first migration. Subsequent updates operate in the same installation.
The previous generation and program are retained; no Git history is rewritten.

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
