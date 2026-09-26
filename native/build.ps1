param([string]$OutputDirectory = (Join-Path $PSScriptRoot '../bin'))
# Also invoked by the source launcher, using a temporary output directory.
$ErrorActionPreference = 'Stop'
$destination = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $destination | Out-Null
cargo build --locked --release --manifest-path (Join-Path $PSScriptRoot 'importer/Cargo.toml')
if ($LASTEXITCODE -ne 0) { throw 'Importer build failed' }
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'importer/target/release/ptcgp-importer.exe') -Destination $destination
$sysroot = (rustc --print sysroot).Trim()
$hostTriple = ((rustc -vV | Select-String '^host: ').ToString() -replace '^host: ', '').Trim()
$linker = Join-Path $sysroot "lib/rustlib/$hostTriple/bin/rust-lld.exe"
if (!(Test-Path -LiteralPath $linker)) { throw "Missing Rust linker: $linker" }
$previousFlags = $env:RUSTFLAGS
try {
    $env:RUSTFLAGS = '-C linker-flavor=ld.lld'
    foreach ($architecture in @('x86_64', 'aarch64')) {
        $target = "$architecture-unknown-linux-musl"
        $variable = 'CARGO_TARGET_' + $target.ToUpperInvariant().Replace('-', '_') + '_LINKER'
        $previousLinker = [Environment]::GetEnvironmentVariable($variable)
        try {
            [Environment]::SetEnvironmentVariable($variable, $linker)
            cargo build --locked --release --target $target --manifest-path (Join-Path $PSScriptRoot 'reader/Cargo.toml')
            if ($LASTEXITCODE -ne 0) { throw "Reader build failed for $target. Install its standard library with rustup target add $target." }
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot "reader/target/$target/release/ptcgp-reader") -Destination (Join-Path $destination "ptcgp-reader-$architecture")
        } finally { [Environment]::SetEnvironmentVariable($variable, $previousLinker) }
    }
} finally { $env:RUSTFLAGS = $previousFlags }
