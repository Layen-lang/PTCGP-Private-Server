# Contributing

The public project starts at version `1.7.2.0`, targeting Pokemon TCG Pocket
`1.7.2`. New work should be recorded under `Unreleased` in `CHANGELOG.md` until
a new project revision is prepared.

## Scope

Contributions should remain focused on local interoperability. Do not add game
binaries, credentials, certificates, traffic captures, player data, or personal
information. Changes to the curated runtime data must be intentional, minimal,
and documented in the pull request.

## Before opening a pull request

Run the complete validation suite from the repository root:

```powershell
go test ./...
go vet ./...
cd web
npm ci
npm run lint
npm exec tsc -- -b
npm run build
cd ..
./scripts/check_publication.ps1
```

Commit the regenerated `internal/admin/dist` files when the frontend changes.
Keep generated protobuf sources synchronized with their `.proto` declarations.

## Conventions

- Use English for code, documentation, issues, and commit messages.
- Keep services bound to the loopback interface.
- Add tests for behavior changes where practical.
- Do not commit local databases, logs, certificates, captures, or binaries.
- Keep `VERSION`, `CHANGELOG.md`, release tags, and packaged releases aligned.
