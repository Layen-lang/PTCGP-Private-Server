# Development

This page covers the source workflow. For the component model first, read
[Architecture](ARCHITECTURE.md).

## Toolchain

- Go 1.25.5 or a later compatible Go 1.25 release.
- Rust with the `x86_64-unknown-linux-musl` and
  `aarch64-unknown-linux-musl` targets for the importer and Android readers.
- ADB available on `PATH`.
- Windows for launcher, system-tray, and Android integration testing.
- A compatible rooted Android emulator or physical phone for end-to-end tests.

## Install dependencies

Install the Rust cross-compilation targets:

```powershell
rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl
```

Go and Cargo dependencies are downloaded by their build commands.

## Run from source

```powershell
powershell -ExecutionPolicy Bypass -File desktop/build.ps1
.\ptcgp-private-server.exe
```

`desktop/build.ps1` builds the backend (`bin\ptcgp-server.exe`), the Android
readers, and the desktop app, which compiles the readers in. Rebuild after
changing Go or Rust sources.

## Manual build

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
go build -o bin/ptcgp-server.exe ./cmd/ptcgp-server
./native/build.ps1
cargo build --release --manifest-path desktop/Cargo.toml
Copy-Item desktop/target/release/ptcgp-private-server.exe ptcgp-private-server.exe
```

`desktop/build.ps1` runs the same steps; see [Desktop application](DESKTOP.md).

## Validate before a pull request

```powershell
go test ./...
go vet ./...
cargo test --locked --manifest-path native/importer/Cargo.toml
cargo build --release --manifest-path desktop/Cargo.toml
.\scripts\check_publication.ps1
```

CI also checks Go formatting and builds the native importer and both Android
readers.

Tests use a synthetic master-data fixture from `internal/testfixture`; they do
not require or publish local game data. Set `PTCGP_TEST_MASTER_DATA` only when
you intentionally run compatible integration checks against a local dataset.

## Generated code and assets

- Keep generated `.pb.go` files synchronized with their `.proto` declarations.
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
Git tags use the same value with a `v` prefix. The README release badge reads
the latest GitHub release tag, so the displayed project version updates
automatically after each release.

## Release process

1. Update `VERSION` and [CHANGELOG.md](../CHANGELOG.md). Review `server.json`
   and update it when client compatibility metadata changes.
2. Verify that the game version, build metadata, contracts, patch manifest,
   extraction plan, and image index belong to the same validated profile.
3. Run every validation command above.
4. Push a tag equal to `v` plus the exact `VERSION` value.
5. The release workflow builds the Go and Rust executables, then
   `desktop/package.ps1` stages the program folder and packages it with
   [Velopack](https://velopack.io): installer, portable archive, full and delta
   update packages, and the release feed. `vpk upload github` publishes them
   as the GitHub release that installed applications read. It does not package
   `game-data/`.

See [Preparation and updates](LOCAL-DATA-UPDATES.md#building-and-publishing)
for packaging, local update tests and profile requirements.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for contribution scope and language
requirements.
