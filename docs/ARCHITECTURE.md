# Architecture

PTCGP Private Server is a local compatibility stack with a short-lived command
controller, a persistent browser panel, a game-facing server, and temporary
Android integration.

## Component map

```text
Browser
  │ http://127.0.0.1:8080
  ▼
Launcher / control panel ───────► Android emulator through ADB + root
  │ reverse proxy                     │
  │ http://127.0.0.1:8081             │ local routing + CA + TLS patch
  ▼                                    ▼
Private server ◄──── HTTPS / gRPC ── Game client
  │
  ├── SQLite profiles and rules
  ├── curated master data and images
  └── sanitized traffic history
```

## Processes and ports

| Component | Entry point | Default address | Responsibility |
| --- | --- | --- | --- |
| CLI controller | `start-server.cmd` / `ptcgp-launcher.exe` | none | Run one action, manage processes, and report status |
| Control panel | `ptcgp-launcher.exe serve-panel` | `127.0.0.1:8080` | Serve the React UI and coordinate Android actions |
| Private server | `ptcgp-server.exe serve` | `127.0.0.1:443` | Serve local HTTPS, REST, and gRPC game endpoints |
| Admin API | private server | `127.0.0.1:8081` | Profile, catalog, pack-rule, and traffic operations |

The panel proxies administration requests so the browser only needs the
launcher address. Mutating control actions require the panel's same-origin CSRF
token. Administration listeners are required to remain on loopback.

## Source layout

| Path | Role |
| --- | --- |
| `cmd/ptcgp-launcher` | CLI, panel process, browser launch, and Windows tray |
| `cmd/ptcgp-server` | Game-facing server command |
| `internal/control` | Process lifecycle, status, Android routing, and recovery |
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

1. Validate `server.json`, client profile, contracts, data, and patch manifest.
2. Generate or reuse the installation's local certificates.
3. Start the private server and verify that it owns its recorded process.
4. Discover the configured Android package through ADB.
5. Back up and hash-check the exact native library.
6. Apply the guarded patch, temporary CA store, hosts routing, and ADB tunnel.
7. Record the active mode and expose status to the control panel.

Official and Stop actions reverse those Android changes using the verified
backup. A Stop request made without the emulator writes a pending-restoration
marker; the next complete status check resumes restoration.

## Persistence

| Location | Lifetime | Contents |
| --- | --- | --- |
| `data/ptcgp.db` | Persistent | Local profiles, inventory, progression, and pack rules |
| `data/runtime/` | Runtime | PIDs, mode, journal, logs, patch state, and recovery marker |
| `certs/` | Per installation | Local certificate authority, certificate, and key |
| `game-data/` | Per release | Read-only curated tables and indexed images |
| Browser storage | Per browser profile | Language and visual preferences |

`data/`, `certs/`, and traffic-development workspaces are ignored by Git.

## Compatibility boundaries

The server implements a local subset of the client-facing REST and gRPC
contracts. It is not official server code and it does not attempt to provide
all online services. In particular, PvP matchmaking and the bidirectional
`Battle/Connect` stream are not implemented.

Every release pins one game version, build hash, protocol fingerprint, and
native-library patch. Exact checks are a safety boundary: unknown clients fail
closed instead of receiving an unverified patch.

## Security model

The project assumes one trusted local Windows user and one controlled emulator.
It is not multi-user and is not an Internet service. The primary safeguards
are loopback-only listeners, strict configuration, process ownership checks,
CSRF protection, hash-guarded patching, and sanitized traffic previews.

For operational rules and vulnerability reporting, see
[SECURITY.md](../SECURITY.md).
