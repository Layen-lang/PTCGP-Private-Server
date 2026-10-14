# Configuration reference

The release works with the bundled `server.json`; most users should not edit
it. This reference is for development, fixed-device selection, and diagnosis.

The configuration file is strict JSON. Unknown fields, missing required values,
invalid addresses, and incompatible release metadata stop startup.

## Selecting a configuration file

By default, the launcher searches for `server.json` from the current directory
upward, then beside the installed executables. Override it with either:

```bat
start-server.cmd --config C:\path\to\server.json
```

```powershell
$env:PTCGP_SERVER_CONFIG = 'C:\path\to\server.json'
.\start-server.cmd
```

Relative data and runtime paths are resolved from the directory containing the
selected configuration file, not from the terminal's current directory.

## Runtime

| Field | Default | Purpose |
| --- | --- | --- |
| `runtime.address` | `127.0.0.1:443` | First device's local HTTPS and gRPC game endpoint |
| `runtime.adminAddress` | `127.0.0.1:8081` | First device's internal administration API |
| `runtime.launcherAddress` | `127.0.0.1:8080` | Browser control panel |
| `runtime.lanAddress` | `0.0.0.0:8082` in the bundled configuration | Home Wi-Fi panel listener; omit or empty to disable |
| `runtime.database` | `data/ptcgp.db` | SQLite profile database |
| `runtime.certificate` | `certs/server.pem` | Generated server certificate |
| `runtime.privateKey` | `certs/server-key.pem` | Generated private key |
| `runtime.certificateAuthority` | `certs/ca.pem` | Generated local CA certificate |
| `runtime.trafficLog` | `data/runtime/traffic.jsonl` | Sanitized traffic history |
| `runtime.runtimeDirectory` | `data/runtime` | Process IDs, logs, mode, and recovery state |
| `runtime.strictMetadata` | `true` | Require the approved client metadata profile |

`adminAddress` and `launcherAddress` must use `127.0.0.1` and must be different.
Keep the game, internal administration, and primary panel services on loopback.
The bundled configuration also enables the separate home Wi-Fi panel listener
at `0.0.0.0:8082`. To enable it in an older configuration, set `lanAddress` to
that value and restart the launcher. Open
`http://<PC IPv4 address>:8082` on a device connected to the same home network.
The Wi-Fi listener accepts private IPv4 clients and numeric local addresses;
the existing origin and CSRF checks still apply. All devices on that network
can control the panel. Allow TCP port 8082 in Windows Firewall for the private
network and local subnet only. Do not forward this port on your router.
Remove `lanAddress` to disable Wi-Fi access.

Additional devices receive independent loopback game and administration ports
automatically. Their addresses are saved in `emulators.json` under
`runtime.runtimeDirectory`; no manual port configuration is needed. Each
device's ADB tunnel maps Android port `443` to its own Windows game endpoint.

## Android

| Field | Purpose |
| --- | --- |
| `android.serial` | Optional startup ADB serial; empty restores `data/device.json`, then enables automatic discovery |
| `android.package` | Installed Android game package |
| `android.activity` | Activity opened by the launcher |
| `android.serverAddress` | Host address visible from Android when reverse tunneling is unavailable |
| `android.redirectHosts` | Hosts redirected only while Local mode is active |

For normal use, choose the device in the panel; no manual serial is required.
A rooted phone connects through USB or Wi-Fi ADB, following the
[phone setup guide](INSTALLATION.md#physical-android-phone-usb-or-wi-fi).
With the launcher-managed ADB reverse tunnel, keep the bundled
`android.serverAddress` unchanged. `runtime.lanAddress` only enables browser
access to the panel; it does not establish the phone's ADB connection.
`data/device.json` is written automatically after selection. Independent server
addresses are saved in `data/runtime/emulators.json`; additional device runtime
files live under `data/runtime/emulators/`. Accounts and imported data are shared.
These runtime paths assume the default `runtime.runtimeDirectory`.
For a temporary command-line device override, use `-Serial` instead of editing the file.
Do not change package, activity, routing, or host values unless you are building
and validating a different supported client profile.

## Data

| Field | Purpose |
| --- | --- |
| `data.masterData` | Locale-aware master-data directory |
| `data.images` | Indexed image directory |

The bundled `server.json` does not set these paths. Before the panel enables
Local mode, the launcher resolves a validated generation under
`data/generations/`. The server performs the same resolution when started
directly. Do not point these fields at an incomplete `.pending` generation;
only development installations normally override them.

## Client, contracts, and patch

The `client`, `contracts`, and `patch` sections describe one approved game
build:

- `client` pins application, SDK, protocol, host, and build identifiers;
- `contracts` pins the Protobuf descriptor fingerprint compiled into the
  server;
- `patch` defines a byte- and hash-guarded TLS patch for the exact native
  library.

These values are release metadata, not user preferences. Do not edit version or
hash fields to bypass compatibility checks. Supporting another game build
requires a newly validated profile and patch.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `PTCGP_SERVER_CONFIG` | Select another configuration file |
| `PTCGP_ADB_SERIAL` | Select a default ADB device serial |
| `PTCGP_TEST_MASTER_DATA` | Opt in to development integration checks against a local dataset |

An explicit command-line option takes precedence over its environment default.

## Command-line reference

```text
start-server.cmd [action] [options]
```

### Actions

| Action | Effect |
| --- | --- |
| omitted or `ui` | Start and open the control panel |
| `local` | Enable Local mode on the selected device, then open the panel |
| `online` | Restore Official mode on the selected device, then open the panel |
| `stop` | Restore all managed devices and stop their servers and the panel |
| `status` | Inspect and print the selected device's state |

### Options

| Option | Effect |
| --- | --- |
| `-Serial <serial>` | Override the selected ADB device; `stop` still affects all managed devices |
| `-Package <name>` | Override the Android package for this command |
| `-NoOpen` | Do not open a browser window |
| `-Json` | Print action or status output as JSON |
| `--config <path>` | Use another `server.json` |

Windows-style option names are case-insensitive. Example:

```bat
start-server.cmd status -Serial 127.0.0.1:5555 -Json
```
