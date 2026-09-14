# Development

This page covers the source workflow. For the component model first, read
[Architecture](ARCHITECTURE.md).

## Toolchain

- Go 1.25.5 or a later compatible Go 1.25 release.
- Node.js 22 and npm.
- Windows for launcher, system-tray, and Android integration testing.
- A compatible rooted emulator for end-to-end tests.

## Install dependencies

```powershell
cd web
npm ci
cd ..
```

Go modules are downloaded automatically by Go commands.

## Run from source

```powershell
.\start-server.cmd
```

In a source checkout, the script runs the Go launcher controller. The launcher
rebuilds the frontend and the two local executables when source inputs are newer
than generated outputs.

To run the frontend development server separately:

```powershell
cd web
npm run dev
```

The Vite development server listens on `127.0.0.1:5173`. It is a frontend
workflow, not a replacement for the Go launcher and administration backend.

## Manual build

```powershell
cd web
npm run build
cd ..
New-Item -ItemType Directory -Force bin | Out-Null
go build -o bin/ptcgp-launcher.exe ./cmd/ptcgp-launcher
go build -o bin/ptcgp-server.exe ./cmd/ptcgp-server
```

The frontend build is written to `internal/admin/dist` and embedded in the Go
executables. Commit regenerated files in that directory whenever the web source
changes.

## Validate before a pull request

```powershell
go test ./...
go vet ./...
cd web
npm run lint
npm exec tsc -- -b
npm run build
cd ..
.\scripts\check_publication.ps1
```

CI also checks Go formatting and verifies that `internal/admin/dist` matches
the frontend source.

Tests use a synthetic master-data fixture from `internal/testfixture`; they do
not require or publish local game data. Set `PTCGP_TEST_MASTER_DATA` only when
you intentionally run compatible integration checks against a local dataset.

## Generated code and assets

- Keep generated `.pb.go` files synchronized with their `.proto` declarations.
- Treat `internal/admin/dist` as generated but committed release input.
- Never commit `data/`, `certs/`, binaries, traffic captures, Android backups,
  APKs, native libraries, credentials, or personal paths.
- Keep runtime game data within the layout documented in
  [Game data](GAME-DATA.md).

## Release versioning

Versions have four numeric components:

```text
GAME_MAJOR.GAME_MINOR.GAME_PATCH.PROJECT_REVISION
```

For example, `1.7.2.0` is the first project release for game version `1.7.2`.
Git tags use the same value with a `v` prefix.

## Release process

1. Update `VERSION`, `server.json`, and [CHANGELOG.md](../CHANGELOG.md).
2. Verify that the game version, build metadata, contracts, patch manifest, and
   data bundle belong to the same validated profile.
3. Run every validation command above.
4. Push a tag equal to `v` plus the exact `VERSION` value.
5. The release workflow builds both Windows executables, stages documentation
   and game data, creates the ZIP and SHA-256 manifest, and publishes a GitHub
   release.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution scope and language
requirements.
