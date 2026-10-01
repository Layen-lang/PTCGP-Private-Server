# Installation

This guide takes you from a clean Windows machine to a working local profile.
If the server is already installed, continue with the [user guide](USAGE.md).

## Choose how to install

For most people, use the published Windows release. It contains the launcher,
server, web interface, extraction profiles, and precompiled Rust tools. Game
data is prepared locally on first launch. Building from source is for
contributors and developers.

| Method | Requires Go, Node.js, and Rust | Recommended for |
| --- | --- | --- |
| [Windows release](#install-a-windows-release) | No | Almost everyone |
| [Source checkout](#run-from-source) | Yes | Development and contributions |

## Requirements

### Windows computer

- Windows 10 or later.
- Permission to run the extracted executables and bind local ports `443`,
  `8080`, and `8081`.
- Enough free space for the release, your local profiles, and emulator.

### Android emulator

The emulator must provide all of the following:

- a 64-bit Android system (x86-64 or ARM64) able to run the supported ARM64 game;
- ADB access;
- root through `su`;
- support for `adb reverse`;
- writable or bind-mountable system files;
- Pokémon TCG Pocket **1.7.5**, installed by you.

These requirements are stricter than simply enabling Android developer mode.
If an emulator cannot provide root or system mounts, it is not compatible.

### ADB

1. Download
   [Android SDK Platform-Tools](https://developer.android.com/tools/releases/platform-tools)
   from the official Android Developers website.
2. Extract it to a stable folder, for example
   `C:\Android\platform-tools`.
3. Open **Edit the system environment variables** from the Windows Start menu.
4. Select **Environment Variables**, edit your user **Path**, and add the folder
   containing `adb.exe`.
5. Close any existing terminals and open a new Command Prompt.

Verify the installation:

```bat
adb version
adb devices
```

Your emulator must appear with the state `device`, not `offline` or
`unauthorized`.

You can also verify root access:

```bat
adb shell su -c id
```

The result must identify user `root` or UID `0`.

## Install a Windows release

1. Open the
   [latest release](https://github.com/Layen-lang/PTCGP-Private-Server/releases/latest).
2. Download `PTCGP-Private-Server-<version>-windows-amd64.zip`.
3. Optionally compare the archive's SHA-256 hash with `SHA256SUMS.txt`.
4. Extract the **entire** ZIP into a folder your Windows user can modify, for
   example `C:\Games\PTCGP-Private-Server`.
5. Confirm that the extracted folder contains:

```text
PTCGP-Private-Server/
├── bin/
│   ├── ptcgp-launcher.exe
│   ├── ptcgp-server.exe
│   ├── ptcgp-importer.exe
│   ├── ptcgp-reader-x86_64
│   └── ptcgp-reader-aarch64
├── docs/
├── profiles/
├── server.json
└── start-server.cmd
```

Do not copy certificates from another installation. The launcher creates a
unique local certificate authority on first use.

## First launch

1. Start the emulator and wait for Android to finish booting.
2. Close the game if it is already running.
3. Double-click `start-server.cmd`.
4. Your browser should open <http://127.0.0.1:8080>. If it does not, open that
   address yourself.
5. Wait for preparation to import the installed game's data and all nine languages.
   Select the emulator if several are connected. The panel switches to Accounts
   when validation succeeds.
6. Select **Local** in the top bar.
7. Wait until the status becomes **Private server active**.
8. Create or select a profile under **Accounts**, then select **Open account**.

The first switch to Local mode can take longer because certificates, runtime
folders, and a verified Android backup are created.

## Multiple emulators or devices

The launcher automatically selects a connected device that contains the
configured game package. If more than one device matches, find the serial with
`adb devices`, then set `android.serial` in `server.json` so the persistent
control panel uses the same device. See [Configuration](CONFIGURATION.md#android).

You can test a serial before saving it with a one-off status command:

```bat
start-server.cmd status -Serial 127.0.0.1:5555
```

## Run from source

Install these development tools first:

- Go 1.25.5 or a later compatible Go 1.25 release;
- Node.js 22 with npm;
- Rust with both Linux musl targets listed below;
- ADB and the emulator requirements above.

Then run:

```powershell
git clone https://github.com/Layen-lang/PTCGP-Private-Server.git
cd PTCGP-Private-Server\web
npm ci
cd ..
.\start-server.cmd
```

`start-server.cmd` also builds the Rust importer and both Android readers when
their sources change or an executable is missing. Install Rust and its
`x86_64-unknown-linux-musl` and `aarch64-unknown-linux-musl` targets beforehand.
You can still build these tools separately with `./native/build.ps1`.

The launcher rebuilds the frontend or Go executables when their source files
are newer than the generated output. For the full manual build and validation
workflow, see [Development](DEVELOPMENT.md).

## Updating

Signed program updates download automatically in published installations. Use
**Update and restart** (or **Mettre à jour et redémarrer** in French) at the
bottom of the sidebar to install a verified update. Program files are replaced
at the root and the panel restarts automatically. Accounts and runtime settings stay in place;
preparation imports changed game data when needed. Update the game itself
through its official source. Local mode refuses an unsupported game build.

See [Preparation and updates](LOCAL-DATA-UPDATES.md) for recovery and the first
migration from an older distribution.

## Uninstalling safely

1. Start the emulator and make sure ADB can see it.
2. Select **Official** and wait for restoration to complete.
3. Run `start-server.cmd status` and confirm routing, certificate, and native
   library states are official.
4. Select **Stop all**.
5. Delete the extracted project folder.

Do not delete the installation while Android restoration is pending. If a step
fails, use the [troubleshooting guide](TROUBLESHOOTING.md).
