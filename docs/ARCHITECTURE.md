# Architecture

PTCGP Private Server is a local compatibility stack with a native desktop
app, a persistent backend that hosts one game-facing server per active
Android device, and temporary Android integration. Devices may be rooted
emulators or physical Android phones connected through USB or Wi-Fi ADB.

## Component map

```text
Desktop app (ptcgp-private-server.exe)
  │ http://127.0.0.1:8080 (loopback API)
  ▼
Backend / control panel (ptcgp-server.exe)
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
| Desktop app | `ptcgp-private-server.exe` | none | Native interface; data importer and Android readers |
| Control panel | `ptcgp-server.exe serve-panel` | `127.0.0.1:8080` | Panel API for the desktop app, Android actions, and hosting of every game server |
| Game server | inside the control panel, one per active device | First: `127.0.0.1:443`; others: allocated loopback ports | Serve local HTTPS, REST, and gRPC game endpoints |
| Admin API | each game server | First: `127.0.0.1:8081`; others: allocated loopback ports | Administration and per-device live traffic |
| CLI | `ptcgp-server.exe <action>` | none | Run one action through the running panel |

The panel proxies administration requests so the desktop app only needs the
launcher address. It serves no web pages. Mutating control actions require the panel's same-origin CSRF
token. Administration listeners are required to remain on loopback.

Additional game and administration ports are allocated automatically and saved
in `data/runtime/emulators.json`. Android port `443` is tunneled to the respective
Windows game port. Certificates, imported data, and the SQLite database are
shared; game credentials and sessions are scoped to an emulator.

## Source layout

| Path | Role |
| --- | --- |
| `cmd/ptcgp-server` | Single backend: panel, CLI, Windows tray, and server subcommands |
| `internal/servercmd` | Game-facing server command, hosted in the panel process |
| `desktop` | Native desktop app (Rust, GPUI); see [Desktop application](DESKTOP.md) |
| `internal/control` | Per-device process lifecycle, selection, status, Android routing, and recovery |
| `internal/android` | Device discovery and installed-game compatibility checks |
| `internal/provision` | Local import, validation, and generation selection |
| `desktop/src/updater.rs` | Program updates through Velopack and GitHub releases |
| `native/importer` and `native/reader` | Game data extraction on Windows and Android |
| `profiles` | Published extraction plans and image index |
| `internal/admin` | Administration API handlers |
| `internal/playerapi` | gRPC Player API compatibility handlers |
| `internal/restapi` | HTTP compatibility handlers |
| `internal/store` | SQLite persistence and migrations |
| `internal/catalog` | Master-data loading, indexes, and validation |
| `internal/packlab` | Official and custom pack-opening rules |
| `internal/protocol` | Approved client metadata policy |
| `internal/proto` | Interoperability protocol declarations and generated Go code |

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
managed emulator in parallel. An unavailable device that may carry local changes
(a recorded patch, fallback tunnel or Local mode) receives a pending-restoration
marker; later status checks resume restoration when it reconnects, including
devices that are not currently selected, at most every 30 seconds for those.
Devices without local changes are stopped without contacting them.

Status checks never delay a mode change: a starting operation interrupts the
check in progress (except a restoration, which always completes). Within one
operation, package details and Android namespace probes are read once.

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
| `data/backups/` | Per installation | Accounts database copied before a new program version first starts |
| `packages/` | Per installation | Update packages downloaded by Velopack |
| `data/desktop.json` | Per installation | Desktop language, theme, and last page |

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
patching, and sanitized traffic previews.

For operational rules and vulnerability reporting, see
[SECURITY.md](../SECURITY.md).
