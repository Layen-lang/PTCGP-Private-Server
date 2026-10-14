# Architecture

PTCGP Private Server is a local compatibility stack with a short-lived command
controller, a persistent browser panel, one game-facing server per active
Android device, and temporary Android integration. Devices may be rooted
emulators or physical Android phones connected through USB or Wi-Fi ADB.

## Component map

```text
Browser
  │ http://127.0.0.1:8080
  ▼
Launcher / control panel
  ├── ADB + root ──► Android device A ── HTTPS / gRPC ──► Private server A
  ├── ADB + root ──► Android device B ── HTTPS / gRPC ──► Private server B
  ├── shared SQLite profiles, account reservations, and pack rules
  ├── shared imported master data, images, and local certificates
  └── per-device mode, process state, recovery, and traffic history
```

Each game reaches its own server through an ADB reverse tunnel. The panel
manages shared profiles directly and routes live traffic requests to the
selected device's administration endpoint.

## Processes and ports

| Component | Entry point | Default address | Responsibility |
| --- | --- | --- | --- |
| CLI controller | `start-server.cmd` / `ptcgp-launcher.exe` | none | Run one action, manage processes, and report status |
| Control panel | `ptcgp-launcher.exe serve-panel` | `127.0.0.1:8080` | Serve the React UI and coordinate Android actions |
| Private server | `ptcgp-server.exe serve`, one per active device | First: `127.0.0.1:443`; others: allocated loopback ports | Serve local HTTPS, REST, and gRPC game endpoints |
| Admin API | each private server | First: `127.0.0.1:8081`; others: allocated loopback ports | Administration and per-device live traffic |

The panel proxies administration requests so the browser only needs the
launcher address. Mutating control actions require the panel's same-origin CSRF
token. Administration listeners are required to remain on loopback.

Additional game and administration ports are allocated automatically and saved
in `data/runtime/emulators.json`. Android port `443` is tunneled to the respective
Windows game port. Certificates, imported data, and the SQLite database are
shared; game credentials and sessions are scoped to an emulator.

## Source layout

| Path | Role |
| --- | --- |
| `cmd/ptcgp-launcher` | CLI, panel process, browser launch, and Windows tray |
| `cmd/ptcgp-server` | Game-facing server command |
| `cmd/ptcgp-release` | Signed update manifest creation during publication |
| `internal/control` | Per-device process lifecycle, selection, status, Android routing, and recovery |
| `internal/android` | Device discovery and installed-game compatibility checks |
| `internal/provision` | Local import, validation, and generation selection |
| `internal/updates` | Signed release download, activation, and rollback |
| `native/importer` and `native/reader` | Game data extraction on Windows and Android |
| `profiles` | Published extraction plans and image index |
| `internal/admin` | Embedded frontend and administration handlers |
| `internal/playerapi` | gRPC Player API compatibility handlers |
| `internal/restapi` | HTTP compatibility handlers |
| `internal/store` | SQLite persistence and migrations |
| `internal/catalog` | Master-data loading, indexes, and validation |
| `internal/packlab` | Official and custom pack-opening rules |
| `internal/protocol` | Approved client metadata policy |
| `internal/proto` | Interoperability protocol declarations and generated Go code |
| `web` | React and TypeScript control panel source |

## Local-mode lifecycle

1. Open the control panel. If no validated generation exists, discover the
   installed game, import its data, and publish a complete generation.
2. Select an emulator. Before Local mode, check its installed game's version
   and native library against the published profile and resolve the shared
   validated generation.
3. Generate or reuse the installation's local certificates.
4. Start that device's private server and verify that it owns its recorded
   process.
5. Back up and hash-check the exact native library.
6. Apply the guarded patch, temporary CA store, hosts routing, and ADB tunnel.
7. Record the active mode and expose status to the control panel.
8. On **Open account**, reserve the profile exclusively for that device,
   write its account state, and start its game. Other devices' sessions remain
   active.

An existing validated generation lets the panel open Accounts without an
emulator. Local mode still requires a connected compatible emulator.
Switching the selected device or account does not repeat data preparation.

Official reverses the selected device's Android changes using its verified
backup, stops its server, and releases its account. Stop all does this for every
managed emulator. An unavailable device receives a pending-restoration marker;
later status checks resume restoration when it reconnects, including devices
that are not currently selected.

SQLite enforces one active device reservation per profile. Emulator-scoped
credentials prevent cloned Android account state from sharing game sessions.
The panel also releases reservations when it detects a stopped server or game,
allowing a short grace period while a game starts.

## Persistence

| Location | Lifetime | Contents |
| --- | --- | --- |
| `data/ptcgp.db` | Persistent | Shared profiles, inventory, progression, pack rules, and device account reservations |
| `data/device.json` | Persistent | Automatically remembered device selection |
| `data/runtime/emulators.json` | Persistent | Managed device serials and independent server addresses |
| `data/runtime/` | Runtime | Launcher journal and logs, plus the first device's PIDs, mode, traffic, patch state, and recovery marker |
| `data/runtime/emulators/<id>/` | Runtime | Additional devices' separate PIDs, logs, traffic, patch state, and recovery markers |
| `certs/` | Per installation | Local certificate authority, certificate, and key |
| `profiles/` | Per release | Validated extraction and compatibility metadata |
| `data/generations/` | Per imported game profile | Validated local master data and indexed images |
| `data/updates/` | Per installation | Verified update downloads, program backups, and installer diagnostics |
| Browser storage | Per browser profile | Language and visual preferences |

`data/`, `certs/`, and traffic-development workspaces are ignored by Git.
Runtime locations above assume the default `runtime.runtimeDirectory`. Each
additional device's directory ID is the first 16 hexadecimal characters of
its serial's SHA-256 hash.

## Compatibility boundaries

The server implements a local subset of the client-facing REST and gRPC
contracts. It is not official server code and it does not attempt to provide
all online services. In particular, PvP matchmaking and the bidirectional
`Battle/Connect` stream are not implemented.

Every release pins one game version, build hash, protocol fingerprint, and
native-library patch. Exact checks are a safety boundary: unknown clients fail
closed instead of receiving an unverified patch.

## Security model

The project assumes one trusted local Windows user controlling one or more
compatible rooted Android devices, including physical phones. Concurrent game sessions share that user's data
and panel; there are no separate panel user accounts. It is not an Internet
service. The primary safeguards are loopback game and administration listeners,
strict configuration, process ownership checks, CSRF protection, hash-guarded
patching, and sanitized traffic previews. Optional home Wi-Fi panel access is
described in [Configuration](CONFIGURATION.md#runtime).

For operational rules and vulnerability reporting, see
[SECURITY.md](../SECURITY.md).
