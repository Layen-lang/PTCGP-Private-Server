param(
    # Folder holding the built executables (ptcgp-private-server.exe and
    # bin\ptcgp-server.exe); the repository root after desktop\build.ps1.
    [string]$Build = (Join-Path $PSScriptRoot '..'),
    # Release folder. Keep the previous release's files here (vpk download)
    # so that a delta package is produced.
    [string]$Output = (Join-Path $PSScriptRoot '..\dist\releases'),
    # Project version with four numbers (default: the VERSION file).
    [string]$Version,
    [string]$Vpk = 'vpk'
)
# Packages the program with Velopack: installer, portable archive, update
# packages and the release feed read by installed applications.
#
# Everything in the staged folder is shipped and replaced as a whole by an
# update, so a new file or folder needs no change to the updater: add it to
# the staged folder below. User data never lives in that folder.
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$Build = [IO.Path]::GetFullPath($Build)
$Output = [IO.Path]::GetFullPath($Output)
if (!$Version) { $Version = (Get-Content (Join-Path $root 'VERSION') -Raw).Trim() }
if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)\.(\d+)$') { throw "Invalid version: $Version" }
# Velopack versions have three numbers: the game patch and the project
# revision share the last one (1.8.1.3 is packaged as 1.8.103).
if ([int]$Matches[4] -ge 100) { throw "The revision must be below 100: $Version" }
$packVersion = '{0}.{1}.{2}' -f $Matches[1], $Matches[2], ([int]$Matches[3] * 100 + [int]$Matches[4])

$stage = Join-Path $root 'dist\stage'
if (Test-Path $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path (Join-Path $stage 'bin') | Out-Null
Copy-Item -LiteralPath (Join-Path $Build 'ptcgp-private-server.exe') -Destination $stage
Copy-Item -LiteralPath (Join-Path $Build 'bin\ptcgp-server.exe') -Destination (Join-Path $stage 'bin')
Copy-Item -LiteralPath (Join-Path $root 'native\THIRD_PARTY_NOTICES.txt') -Destination (Join-Path $stage 'bin')
foreach ($file in 'server.json', 'README.md', 'CHANGELOG.md', 'LICENSE', 'NOTICE.md', 'SECURITY.md') {
    Copy-Item -LiteralPath (Join-Path $root $file) -Destination $stage
}
Set-Content -LiteralPath (Join-Path $stage 'VERSION') -Value $Version -NoNewline -Encoding ascii
Copy-Item -LiteralPath (Join-Path $root 'docs') -Destination (Join-Path $stage 'docs') -Recurse
Copy-Item -LiteralPath (Join-Path $root 'profiles') -Destination (Join-Path $stage 'profiles') -Recurse

& $Vpk pack `
    --packId PTCGPPrivateServer `
    --packVersion $packVersion `
    --packDir $stage `
    --mainExe ptcgp-private-server.exe `
    --packTitle 'PTCGP Private Server' `
    --packAuthors 'PTCGP Private Server' `
    --icon (Join-Path $PSScriptRoot 'assets\app.ico') `
    --outputDir $Output
if ($LASTEXITCODE -ne 0) { throw 'Velopack packaging failed' }
Write-Host "Packaged $Version as $packVersion in $Output"
