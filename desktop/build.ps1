param(
    # Installation folder to write (the repository root by default).
    [string]$Output = (Join-Path $PSScriptRoot '..')
)
# Builds the two PTCGP Private Server executables:
#   ptcgp-private-server.exe  desktop app (GPUI), data importer and Android readers
#   bin\ptcgp-server.exe      backend: panel, accounts, Android control, game servers
# Requirements: Go, Rust and both musl targets for the readers.
# desktop\package.ps1 turns the result into an installer and update packages.
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$Output = [IO.Path]::GetFullPath($Output)
New-Item -ItemType Directory -Force -Path (Join-Path $Output 'bin') | Out-Null

Push-Location $root
try {
    $env:GOFLAGS = '-buildvcs=false'
    go build -trimpath -ldflags '-s -w' -o (Join-Path $Output 'bin\ptcgp-server.exe') ./cmd/ptcgp-server
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    # The readers are compiled into the desktop app, not shipped as files.
    & (Join-Path $root 'native\build.ps1')
} finally { Pop-Location }

Push-Location $PSScriptRoot
try {
    cargo build --release
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
} finally { Pop-Location }
$app = Join-Path $Output 'ptcgp-private-server.exe'
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'target\release\ptcgp-private-server.exe') -Destination $app -Force
foreach ($file in $app, (Join-Path $Output 'bin\ptcgp-server.exe')) {
    Write-Host ("{0} ({1} MB)" -f $file, [math]::Round((Get-Item -LiteralPath $file).Length / 1MB, 1))
}
