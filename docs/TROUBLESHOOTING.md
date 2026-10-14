# Troubleshooting

Start with the status command from the extracted project folder:

```bat
start-server.cmd status
```

In Local mode, the server should be running, Android should be connected with
root, and routing, certificates, and the native library should all report their
local state.

## Quick diagnosis

| Symptom | Most likely cause | What to do |
| --- | --- | --- |
| `adb` is not recognized | Platform-Tools is missing from `PATH` | Install it, update `PATH`, and open a new terminal |
| No device is listed | Emulator ADB is disabled or still starting | Enable ADB, restart the emulator, then run `adb devices` |
| Device is `offline` | Stale ADB connection | Restart ADB and reconnect the emulator |
| Device is `unauthorized` | Android has not approved this computer | Accept the debugging prompt in Android |
| USB phone is missing | USB debugging, cable, authorization, or Windows ADB driver | Enable USB debugging, use a data cable, authorize the computer, and check the manufacturer's ADB driver |
| Wi-Fi phone is missing | Wireless ADB is not connected | Follow the [Wi-Fi phone setup](INSTALLATION.md#wi-fi-connection-on-android-11-or-later), confirm `adb devices -l` lists it, then select **Refresh** |
| Wireless pairing succeeds but connection fails | Pairing port used as connection port, changed address/port, or network isolation | Use the connection address on the main Wireless debugging screen; check that the local network allows computer-to-phone connections |
| Root is false on a physical phone | Phone is unrooted or shell root was denied | Root is mandatory; authorize `su` for the ADB shell in the root manager and repeat the root check |
| Root is false | `su` is unavailable or denied | Enable the emulator's root mode and test `adb -s <serial> shell su -c id` |
| More than one device matches | Automatic selection is ambiguous | Choose a device beside **Refresh**; use `-Serial` for command-line actions |
| MuMu appears but LDPlayer 9 does not | Both emulators expose ADB on port `5555` | Enable LDPlayer ADB, wait for Android, then select **Refresh**; the launcher reads LD9's NAT log to discover its separate endpoint |
| Account is in use on another emulator | That emulator owns the active account session | Choose another account, or select **Official** on the owning emulator to release it |
| Switching devices or accounts opens preparation | Old panel build, or missing/invalid imported data | Restart the updated launcher; valid imported data is reused for all emulators and accounts |
| Port already in use | Another process owns a configured or assigned port | Inspect the port named in the error and stop the conflicting process |
| Version or hash mismatch | The installed game build is unsupported | Return to Official mode and use a project release that supports that game build |
| Preparation is blocked or data fails to load | Game resources are incomplete, or the local generation is missing or damaged | Finish the official game's resource download, then select **Retry preparation** in the panel |
| Browser does not open | Default-browser launch failed | Open <http://127.0.0.1:8080> manually |
| Official restoration is pending | Stop was requested while the emulator was unavailable | Reconnect the emulator and run the launcher again |

An authentication error such as `103-011-010` on Android 14 or newer usually
means the local CA is not visible inside the game's Android mount namespace.
Current builds install and verify the CA in both the legacy system store and
the Conscrypt APEX before reporting **Private server active**. If the error
persists after updating, switch to **Official**, then back to **Local**, and
inspect that emulator's `server.stderr.log` for `tls: unknown certificate`.
The first emulator uses `data/runtime/`; additional emulators use
`data/runtime/emulators/<id>/`, as described in [Architecture](ARCHITECTURE.md#persistence).

## ADB checks

Run these commands in order:

```bat
adb version
adb devices
```

Use the exact serial from `adb devices` for the emulator you are diagnosing:

```bat
adb -s 127.0.0.1:5555 shell su -c id
adb -s 127.0.0.1:5555 reverse --list
start-server.cmd status -Serial 127.0.0.1:5555
```

If the device is offline, restart ADB:

```bat
adb kill-server
adb start-server
adb devices
```

After restarting ADB, re-enable **Local** on each affected emulator to restore
any missing reverse tunnel.

When MuMu binds `127.0.0.1:5555` and LDPlayer 9 exposes a wildcard NAT listener
on the same port, the launcher probes LD9 through `127.0.0.2:5555`. **Refresh**
reruns discovery. Multiple ADB serials for the same Android instance are grouped
as one device in the selector.

## Ports 443, 8080, and 8081

The default ports are:

| Port | Owner |
| --- | --- |
| `443` | First emulator's local game HTTPS and gRPC server |
| `8080` | Browser control panel |
| `8081` | First emulator's internal administration API |

Additional emulators receive their own loopback game and administration ports,
saved in `data/runtime/emulators.json`. Their Android port `443` is tunneled to
the corresponding Windows game port; no manual port assignment is required.

Inspect a port without stopping anything:

```powershell
Get-NetTCPConnection -State Listen -LocalPort 443,8080,8081 |
  Select-Object LocalAddress,LocalPort,OwningProcess
```

The launcher does not kill processes it does not own. Close the conflicting
application yourself. For an additional emulator, replace the example ports in
the inspection command with the port named in the error.

## Version or native-library mismatch

If preparation reports `game native library unavailable: exit status 1`, Android
may be loading the library directly from a split APK. The preparation check now
validates its SHA-256 inside the installed APK when the extracted file is absent;
local-mode setup extracts it before applying the patch. Restart the launcher
after updating the server and select **Retry preparation**. If it still fails,
the error identifies the missing library or the file that could not be read.

The launcher checks the game version and SHA-256 hashes before applying or
restoring the TLS patch. It refuses unknown files intentionally.

1. Keep the emulator connected.
2. Select that emulator in the panel and select **Official**, or run
   `start-server.cmd online -Serial <serial>`.
3. Confirm the installed game is a version supported by the current project
   release, as stated in the root [README](../README.md).
4. Install a project release that supports that version, if one is available.

Do not edit hashes in `server.json` to bypass verification. A forced patch on
an unknown library can leave the game installation unusable.

## Missing or damaged game data

The release contains extraction profiles and tools, not the game's images or
master data. The panel imports them from the installed game into
`data/generations/`. Start the official game and finish its resource download,
then return to the panel and select **Retry preparation**. Keep the
installation folder and any `.pending` generation intact so a retry can reuse
completed work.

If the panel reports an unsupported game build or an unknown Unity schema,
install a compatible project release. See
[Preparation and updates](LOCAL-DATA-UPDATES.md) and [Game data](GAME-DATA.md).

## Logs

Use the panel's **Logs** page first. If startup fails before the panel loads,
inspect these local files:

```text
data/runtime/launcher.stderr.log
data/runtime/server.stderr.log
data/runtime/emulators/<id>/server.stderr.log
```

The first server uses the root runtime directory; additional servers use a
directory derived from their serial. See [Architecture](ARCHITECTURE.md#persistence)
for the directory ID and [Configuration](CONFIGURATION.md#runtime) for custom paths.

The runtime directory can also contain the current mode, process IDs, operation
journal, traffic history, and a pending-restoration marker. Do not edit these
files while the launcher is running.

Logs can contain private device or account context. Redact them before sharing.

## Safe recovery

If Local mode failed halfway through:

1. Leave the installation folder intact.
2. Start the same emulator and verify ADB plus root.
3. Run `start-server.cmd online -Serial <serial>` for the affected emulator.
4. Run `start-server.cmd status -Serial <serial>` and confirm official routing,
   certificate, and native-library states.
5. Run `start-server.cmd stop` when restoration is complete. This restores and
   stops all managed emulators' servers, then closes the panel.

If restoration still fails, keep the exact installation and its runtime state,
collect the smallest relevant log excerpt, and report the problem privately if
it contains sensitive data. Follow the [security policy](../SECURITY.md).
