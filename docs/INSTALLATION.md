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
- Enough free space for the release, imported game data, your profiles, and any emulator.

### Android device: emulator or rooted phone

The Android device must provide all of the following:

- a 64-bit Android system (x86-64 or ARM64) able to run the supported ARM64 game;
- ADB access;
- root through `su`;
- support for `adb reverse`;
- writable or bind-mountable system files;
- Pokémon TCG Pocket **1.7.5**, installed by you.

These requirements are stricter than simply enabling Android developer mode.
If a device cannot provide root or system mounts, it is not compatible.
Root is mandatory on physical phones as well as emulators. A rooted physical
Android phone has been tested successfully, including over Wi-Fi ADB. This
confirms the tested setup; other models and ROMs must satisfy the same checks.

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

Your Android device must appear with the state `device`, not `offline` or
`unauthorized`.

You can also verify root access:

```bat
adb -s DEVICE_SERIAL shell su -c id
```

Replace `DEVICE_SERIAL` with the exact serial from `adb devices`. Accept the
root manager's request on Android if shown. The result must identify user
`root` or UID `0`.

## Physical Android phone: USB or Wi-Fi

The phone runs the game; the Windows computer runs the private server. Keep
the computer and the chosen ADB connection available throughout preparation,
playing, and restoration. Install the supported game and finish its resource
download on the phone before preparing local data.

### USB connection

1. Enable **Developer options**, then **USB debugging** on the phone.
2. Connect it with a USB data cable, unlock it, and accept the computer's
   debugging authorization prompt.
3. Run `adb devices -l` on Windows. If nothing appears, check the cable and
   install the manufacturer's Windows ADB USB driver if needed. See
   [Android's hardware-device setup](https://developer.android.com/studio/run/device).
4. Run the root check above using the phone's serial and grant shell root access
   in its root manager.
5. Keep the cable connected, then follow [First launch](#first-launch). Select
   the phone beside **Refresh**, enable **Local**, and open a local account.

### Wi-Fi connection on Android 11 or later

Use current Android SDK Platform-Tools. On the phone, enable **Developer
options → Wireless debugging** on the same local network as the computer.

1. Choose **Pair device with pairing code** on the phone.
2. On Windows, run `adb pair` with the address and pairing port shown there:

   ```bat
   adb pair 192.168.1.50:37099
   ```

   Enter the displayed pairing code when asked. Addresses and ports here are
   examples; substitute the phone's values.
3. Return to the main **Wireless debugging** screen. If `adb devices -l` does
   not list the connected phone, use `adb connect` with that screen's address
   and connection port:

   ```bat
   adb connect 192.168.1.50:41815
   adb devices -l
   ```

   The connection port is distinct from the pairing port. Use the displayed
   connection port rather than assuming `5555`.
4. Check root using the exact serial listed by ADB, then follow
   [First launch](#first-launch). The USB cable is not needed for this method.

These steps follow the official
[ADB wireless-debugging guide](https://developer.android.com/tools/adb#connect-to-a-device-over-wi-fi).

### Wi-Fi after an initial USB connection

If Wireless debugging is unavailable, connect and authorize the phone over USB
first. With both devices on the same local network, run:

```bat
adb -s PHONE_USB_SERIAL tcpip 5555
adb connect 192.168.1.50:5555
adb devices -l
```

Replace `PHONE_USB_SERIAL` with the USB serial and `192.168.1.50` with the
phone's Wi-Fi address. Once the network connection reports `device`, unplug
USB and select the Wi-Fi phone entry in the panel. This method requires the
phone's ADB daemon to support TCP mode. Android documents it as the
[USB-assisted Wi-Fi method](https://developer.android.com/tools/adb#wireless).

After playing, restore **Official** while ADB is still connected. For this TCP
method, `adb -s 192.168.1.50:5555 usb` returns ADB to USB mode; for paired
Wireless debugging, switch **Wireless debugging** off on the phone.

### Using the game over Wi-Fi

Wi-Fi access alone is insufficient: the phone must be connected to ADB and
listed as `device`. **Refresh** updates the selector after that connection;
it does not pair phones automatically. Local mode creates the game's ADB
reverse tunnel automatically, just as it does over USB. Keep game and internal
administration listeners on loopback; you do not need to change
`android.serverAddress`, expose port `443`, or configure `runtime.lanAddress`
for this ADB setup.

The optional [home Wi-Fi panel listener](CONFIGURATION.md#runtime) is only for
opening the control panel in the phone's browser. It is separate from connecting
the game through Wi-Fi ADB.

If the Wi-Fi address or connection port changes, reconnect using the current
values, select **Refresh**, and re-enable **Local** to restore the tunnel.
When changing between USB and Wi-Fi, restore **Official** through the current
connection first, then connect and select the new transport before enabling
**Local** again. Restore **Official** or use **Stop all** before disconnecting
ADB; an unavailable device's restoration remains pending until it reconnects.

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

1. Start the emulator or connect the rooted phone over USB or Wi-Fi ADB, and
   wait for Android to finish booting.
2. Close the game if it is already running.
3. Double-click `start-server.cmd`.
4. Your browser should open <http://127.0.0.1:8080>. If it does not, open that
   address yourself.
5. Wait for preparation to import the installed game's data and all nine languages.
   Select the Android device if several are connected. The panel switches to Accounts
   when validation succeeds.
6. Select **Local** in the top bar.
7. Wait until the status becomes **Private server active**.
8. Create or select a profile under **Accounts**, then select **Open account**.

The first switch to Local mode can take longer because certificates, runtime
folders, and a verified Android backup are created.

## Multiple emulators or devices

Use the device selector at the top right, next to **Refresh**. It remains visible
with one device, and the panel remembers your choice in `data/device.json`.
Leave `android.serial` empty to restore that remembered choice on startup;
setting it explicitly overrides the saved selection.

You can use several emulators at once: select A, enable **Local**, and open one
account; then select B, enable **Local**, and open another. Each emulator has
its own game session, server endpoint, tunnel, and restoration state. A running
account cannot be opened on another emulator. **Official** affects the selected
emulator; **Stop all** restores and stops every managed game session.

Connected rooted phones use the same selector and per-device controls. A local
account cannot run on a phone and an emulator at the same time either.

Switching devices does not repeat installation preparation. Imported game data
is shared, and compatibility is checked before Local mode starts on each device.
ADB transports that refer to the same Android instance are treated as one device.

You can test a serial before saving it with a one-off status command:

```bat
start-server.cmd status -Serial 127.0.0.1:5555
```

## Run from source

Install these development tools first:

- Go 1.25.5 or a later compatible Go 1.25 release;
- Node.js 22 with npm;
- Rust with both Linux musl targets listed below;
- ADB and the Android-device requirements above.

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

1. Connect every Android device used with Local mode and make sure ADB can see them.
2. Select each device in the panel, select **Official**, and wait for its
   restoration to complete.
3. Confirm each device's routing, certificate, and native-library states are
   official. For a command-line check, use `start-server.cmd status -Serial <serial>`.
4. Select **Stop all**.
5. Delete the extracted project folder.

Do not delete the installation while Android restoration is pending. If a step
fails, use the [troubleshooting guide](TROUBLESHOOTING.md).
