# Desktop application

PTCGP Private Server ships as two Windows executables:

| File | Role |
| --- | --- |
| `ptcgp-private-server.exe` | The desktop app a user launches. It also contains the data importer and both Android readers |
| `bin\ptcgp-server.exe` | The single backend: control panel API, accounts, Android control, data preparation, Wi-Fi web panel, and the game server for every device |

There are no other executables or libraries. The Android readers are Linux
programs for emulators: they are compiled into the app and streamed to the
device with `adb exec-in` during preparation, without being written on the
computer.

## How it starts

1. The window opens straight away (about 150 ms on a warm start). The
   interface is drawn on the GPU with [GPUI](https://gpui.rs), the Rust UI
   framework behind Zed and Discord's native client, plus GPUI Kit components.
   No browser engine or webview is involved.
2. If no server answers on `runtime.launcherAddress`, the app starts
   `bin\ptcgp-server.exe serve-panel` in the background. It listens within
   about 60 ms, then validates and loads the game data.
3. Accounts appear as soon as `/api/bootstrap` answers, in a few
   milliseconds. ADB-backed status and device discovery fill in when they
   arrive.

The server keeps running when the window closes, so games stay connected. The
System Tray **Open** entry reopens the window.

## One server for every device

Each device still gets its own game and administration ports, because ADB
tunnels from all devices arrive from `127.0.0.1` and the port identifies the
device. All of these listeners run inside the one `ptcgp-server.exe`
process. Starting, stopping, and recovering a device's server only starts or
cancels its listeners.

Command-line actions (`status`, `local`, `online`, `stop`) go through the
running server when one is listening:

```powershell
.\ptcgp-private-server.exe status
```

## Data preparation

The server runs `ptcgp-private-server.exe import …` to extract images and
master data, so preparation needs the app next to the `bin` folder.

## Building

Requirements are the same as for the native tools: Go, Rust and the two musl
targets used by the Android readers.

```powershell
powershell -ExecutionPolicy Bypass -File desktop\build.ps1
```

The script writes `ptcgp-private-server.exe` and `bin\ptcgp-server.exe` at the
repository root (or `-Output <folder>`). `cargo run --release` in `desktop/`
also works for interface work; it embeds the readers built by
`native/build.ps1` into `native/target/readers`.

## Source layout

| Path | Role |
| --- | --- |
| `desktop/src/main.rs` | Entry point, `import` role and command-line forwarding |
| `desktop/src/backend.rs` | Installation lookup, preferences, server start |
| `desktop/build.rs` | Compiles the Android readers into the app |
| `desktop/src/api.rs` | HTTP client for the panel API (same CSRF rules as the web panel) |
| `desktop/src/http.rs` | Image loading for GPUI |
| `desktop/src/theme.rs` | Port of `web/src/theme.ts` (presets and colour derivation) |
| `desktop/src/i18n.rs`, `desktop/locales/` | Translations generated from `web/src/i18n/locales` |
| `desktop/src/ui/` | Shell and pages, mirroring `web/src/App.tsx` and `web/src/pages/` |

Desktop preferences (language, theme, last page) are stored in
`data/desktop.json`.

## Known limits

- Signed updates replace `bin\ptcgp-server.exe` and the profiles. Updating the
  desktop app itself through the updater is not done yet.
- Game servers now stop with the server process. After a restart, select
  **Local** again for a device that was in local mode.
- Live traffic polls the server every second instead of using the
  server-sent event stream.
