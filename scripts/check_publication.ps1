$ErrorActionPreference = 'Stop'

$tracked = @(git -c core.quotepath=false ls-files)
$forbiddenPrefixes = @(
    'analysis/',
    'backups/',
    'bin/',
    'certs/',
    'data/',
    'tools/',
    'traffic-intercepter/'
)
$forbiddenExtensions = @('.apk', '.aab', '.dex', '.har', '.key', '.p12', '.pfx', '.pem', '.so')
$imageExtensions = @('.png', '.jpg', '.jpeg', '.webp')
$maxPortablePathLength = 180

$unsafe = foreach ($path in $tracked) {
    $normalized = $path.Replace('\', '/')
    if ($normalized.StartsWith('scripts/', [StringComparison]::OrdinalIgnoreCase) -and
        $normalized -ne 'scripts/check_publication.ps1') {
        $path
        continue
    }
    if ($forbiddenPrefixes.Where({ $normalized.StartsWith($_, [StringComparison]::OrdinalIgnoreCase) }).Count -gt 0) {
        $path
        continue
    }
    if ($forbiddenExtensions -contains [IO.Path]::GetExtension($normalized).ToLowerInvariant()) {
        $path
        continue
    }
    if ($normalized.StartsWith('game-data/', [StringComparison]::OrdinalIgnoreCase)) {
        $extension = [IO.Path]::GetExtension($normalized).ToLowerInvariant()
        $isImage = $normalized.StartsWith('game-data/images/', [StringComparison]::OrdinalIgnoreCase) -and
            ($imageExtensions -contains $extension)
        $isImageIndex = $normalized -eq 'game-data/images/index.json'
        $isMasterData = $normalized -match '^game-data/master-data/[^/]+/[^/]+\.json$'
        if (-not ($isImage -or $isImageIndex -or $isMasterData)) {
            $path
        }
    }
}

$oversized = foreach ($path in $tracked) {
    $item = Get-Item -LiteralPath $path
    if ($item.Length -ge 50MB) {
        "$path ($($item.Length) bytes)"
    }
}

$overlong = @($tracked | Where-Object { $_.Length -gt $maxPortablePathLength })

if ($oversized) {
    throw "Files at or above GitHub's 50 MiB warning threshold are tracked:`n$($oversized -join "`n")"
}

if ($overlong) {
    throw "Paths longer than $maxPortablePathLength characters are not portable to default Windows Git checkouts:`n$($overlong -join "`n")"
}

if ($unsafe) {
    throw "Private or third-party files are tracked:`n$($unsafe -join "`n")"
}

$personalPaths = @(git grep -n -I -E '[A-Za-z]:\\Users\\' -- . 2>$null)
if ($personalPaths) {
    throw "Personal Windows paths were found:`n$($personalPaths -join "`n")"
}

Write-Output "Publication safety check passed for $($tracked.Count) tracked files."
exit 0
