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
| Root is false | `su` is unavailable or denied | Enable the emulator's root mode and test `adb shell su -c id` |
| More than one device matches | Automatic selection is ambiguous | Pass `-Serial` or set `android.serial` |
| Port already in use | Another process owns `443`, `8080`, or `8081` | Stop that process or restore the configured port |
| Version or hash mismatch | The game build does not match this release | Return to Official mode and install the exact supported version |
| Panel opens but data fails to load | Missing or damaged `game-data` | Extract the complete release again |
| Browser does not open | Default-browser launch failed | Open <http://127.0.0.1:8080> manually |
| Official restoration is pending | Stop was requested while the emulator was unavailable | Reconnect the emulator and run the launcher again |

## ADB checks

Run these commands in order:

```bat
adb version
adb devices
adb shell su -c id
adb reverse --list
```

If the device is offline, restart ADB:

```bat
adb kill-server
adb start-server
adb devices
```

If several devices are connected, address one explicitly:

```bat
start-server.cmd status -Serial 127.0.0.1:5555
```

Replace the example serial with the exact value printed by `adb devices`.

## Ports 443, 8080, and 8081

The default ports are:

| Port | Owner |
| --- | --- |
| `443` | Local game HTTPS and gRPC server |
| `8080` | Browser control panel |
| `8081` | Internal administration API |

Inspect a port without stopping anything:

```powershell
Get-NetTCPConnection -State Listen -LocalPort 443,8080,8081 |
  Select-Object LocalAddress,LocalPort,OwningProcess
```

The launcher does not kill processes it does not own. Close the conflicting
application yourself. Changing the game-server port is not a normal workaround
because Android routing expects port `443`.

## Version or native-library mismatch

The launcher checks the game version and SHA-256 hashes before applying or
restoring the TLS patch. It refuses unknown files intentionally.

1. Keep the emulator connected.
2. Run `start-server.cmd online`.
3. Confirm the installed game is exactly the version stated in the root
   [README](../README.md).
4. Use the project release made for that game version.

Do not edit hashes in `server.json` to bypass verification. A forced patch on
an unknown library can leave the game installation unusable.

## Missing game data

A published release includes `game-data/images` and `game-data/master-data`.
If either directory is missing or partially extracted, download the archive
again and extract all files. See [Game data](GAME-DATA.md) for the expected
layout.

## Logs

Use the panel's **Logs** page first. If startup fails before the panel loads,
inspect these local files:

```text
data/runtime/launcher.stderr.log
data/runtime/server.stderr.log
```

The runtime directory can also contain the current mode, process IDs, operation
journal, traffic history, and a pending-restoration marker. Do not edit these
files while the launcher is running.

Logs can contain private device or account context. Redact them before sharing.

## Safe recovery

If Local mode failed halfway through:

1. Leave the installation folder intact.
2. Start the same emulator and verify ADB plus root.
3. Run `start-server.cmd online`.
4. Run `start-server.cmd status` and confirm official routing, certificate, and
   native-library states.
5. Run `start-server.cmd stop` when restoration is complete.

If restoration still fails, keep the exact installation and its runtime state,
collect the smallest relevant log excerpt, and report the problem privately if
it contains sensitive data. Follow the [security policy](../SECURITY.md).
