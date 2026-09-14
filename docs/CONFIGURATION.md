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
| `runtime.address` | `127.0.0.1:443` | Local HTTPS and gRPC game endpoint |
| `runtime.adminAddress` | `127.0.0.1:8081` | Internal administration API |
| `runtime.launcherAddress` | `127.0.0.1:8080` | Browser control panel |
| `runtime.database` | `data/ptcgp.db` | SQLite profile database |
| `runtime.certificate` | `certs/server.pem` | Generated server certificate |
| `runtime.privateKey` | `certs/server-key.pem` | Generated private key |
| `runtime.certificateAuthority` | `certs/ca.pem` | Generated local CA certificate |
| `runtime.trafficLog` | `data/runtime/traffic.jsonl` | Sanitized traffic history |
| `runtime.runtimeDirectory` | `data/runtime` | Process IDs, logs, mode, and recovery state |
| `runtime.strictMetadata` | `true` | Require the approved client metadata profile |

`adminAddress` and `launcherAddress` must use `127.0.0.1` and must be different.
Keep all three services on loopback. This project is not designed or hardened
for LAN or Internet exposure.

## Android

| Field | Purpose |
| --- | --- |
| `android.serial` | Fixed ADB device serial; empty enables automatic discovery |
| `android.package` | Installed Android game package |
| `android.activity` | Activity opened by the launcher |
| `android.serverAddress` | Host address visible from Android when reverse tunneling is unavailable |
| `android.redirectHosts` | Hosts redirected only while Local mode is active |

For a temporary device override, prefer `-Serial` instead of editing the file.
Do not change package, activity, routing, or host values unless you are building
and validating a different supported client profile.

## Data

| Field | Purpose |
| --- | --- |
| `data.masterData` | Locale-aware master-data directory |
| `data.images` | Indexed image directory |

The default values point to the two runtime directories under `game-data/`.
Only development installations normally override them.

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
| `local` | Enable Local mode, then open the panel |
| `online` | Restore Official mode, then open the panel |
| `stop` | Restore Android and stop the server and panel |
| `status` | Inspect and print the current state |

### Options

| Option | Effect |
| --- | --- |
| `-Serial <serial>` | Use one ADB device for this command |
| `-Package <name>` | Override the Android package for this command |
| `-NoOpen` | Do not open a browser window |
| `-Json` | Print action or status output as JSON |
| `--config <path>` | Use another `server.json` |

Windows-style option names are case-insensitive. Example:

```bat
start-server.cmd status -Serial 127.0.0.1:5555 -Json
```
