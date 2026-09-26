# Development

This page covers the source workflow. For the component model first, read
[Architecture](ARCHITECTURE.md).

## Toolchain

- Go 1.25.5 or a later compatible Go 1.25 release.
- Node.js 22 and npm.
- Rust with the `x86_64-unknown-linux-musl` and
  `aarch64-unknown-linux-musl` targets for the importer and Android readers.
- ADB available on `PATH`.
- Windows for launcher, system-tray, and Android integration testing.
- A compatible rooted emulator for end-to-end tests.

## Install dependencies

```powershell
cd web
npm ci
cd ..
```

Install the Rust cross-compilation targets:

```powershell
rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl
```

Go and Cargo dependencies are downloaded by their build commands.

## Run from source

```powershell
.\start-server.cmd
```

In a source checkout, the script runs the Go launcher controller. The launcher
rebuilds the frontend, the two Go executables, the Rust importer, and both
Android readers when their inputs are newer than generated outputs.

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
./native/build.ps1
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
cargo test --locked --manifest-path native/importer/Cargo.toml
cd web
npm run lint
npm exec tsc -- -b
npm run build
cd ..
.\scripts\check_publication.ps1
```

CI also checks Go formatting, verifies that `internal/admin/dist` matches the
frontend source, and builds the native importer and both Android readers.

Tests use a synthetic master-data fixture from `internal/testfixture`; they do
not require or publish local game data. Set `PTCGP_TEST_MASTER_DATA` only when
you intentionally run compatible integration checks against a local dataset.

## Generated code and assets

- Keep generated `.pb.go` files synchronized with their `.proto` declarations.
- Treat `internal/admin/dist` as generated but committed release input.
- Never commit `data/`, `certs/`, binaries, traffic captures, Android backups,
  APKs, native libraries, credentials, or personal paths.
- Keep imported game data in ignored `data/generations/`. Commit only validated
  extraction plans and compatibility metadata under `profiles/`.
- Keep generated native build outputs out of Git. See [Game data](GAME-DATA.md)
  for the runtime layout.

## Release versioning

Versions have four numeric components:

```text
GAME_MAJOR.GAME_MINOR.GAME_PATCH.PROJECT_REVISION
```

For example, `1.7.2.0` is the first project release for game version `1.7.2`.
Git tags use the same value with a `v` prefix.

## Release process

1. Update `VERSION` and [CHANGELOG.md](../CHANGELOG.md). Review `server.json`
   and update it when client compatibility metadata changes.
2. Verify that the game version, build metadata, contracts, patch manifest,
   extraction plan, and image index belong to the same validated profile.
3. Run every validation command above.
4. Configure the matching Ed25519 public key repository variable
   `PTCGP_UPDATE_PUBLIC_KEY` and private signing seed secret
   `PTCGP_UPDATE_SIGNING_SEED` before the first signed release. Keep the seed
   out of the repository.
5. Push a tag equal to `v` plus the exact `VERSION` value.
6. The release workflow builds Go and Rust executables, stages documentation
   and `profiles/`, creates the ZIP, SHA-256 list, and signed update manifest,
   then publishes a GitHub release. It does not package `game-data/`.

See [Preparation and updates](LOCAL-DATA-UPDATES.md#building-and-publishing)
for the signing and profile requirements.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution scope and language
requirements.
