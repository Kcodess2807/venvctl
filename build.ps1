# Release build for venvctl.
#
#   .\build.ps1                     # version from `git describe`, amd64 + arm64, no signing
#   .\build.ps1 -Version 0.2.0
#   .\build.ps1 --sign              # or -Sign: signs with signtool when VENVCTL_SIGN_CERT is set,
#                                   # otherwise prints the exact signtool commands it would run
#
# Output in dist\: venvctl-<ver>-windows-<arch>\venvctl.exe, a .zip of each, and SHA256SUMS.
# Binaries are never stripped (-s -w): stripped Go binaries trip Defender's ML heuristics.
# VENVCTL_SIGN_CERT = path to a .pfx; VENVCTL_SIGN_PASSWORD = its password (optional);
# VENVCTL_SIGN_TIMESTAMP = RFC 3161 server (default http://timestamp.digicert.com).
param(
    [string]$Version = "",
    [switch]$Sign,
    [string[]]$Arch = @("amd64", "arm64")
)
if ($args -contains "--sign") { $Sign = $true }
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not $Version) {
    $described = (git describe --tags --always --dirty 2>$null)
    if ($described -match '^v?(\d+\.\d+\.\d+.*)$') { $Version = $Matches[1] }
    else { $Version = "0.0.0-dev+" + ($described -replace '[^0-9A-Za-z.-]', '') }
}
$numeric = "0.0.0.0"
if ($Version -match '^(\d+)\.(\d+)\.(\d+)') { $numeric = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0" }
$date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd")
$dist = Join-Path $PSScriptRoot "dist"
if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory $dist | Out-Null
Write-Host "venvctl $Version ($numeric), built $date, arch: $($Arch -join ', ')"

# Windows resources: version info, icon, manifest with longPathAware=true.
$syso = @()
if (Get-Command go-winres -ErrorAction SilentlyContinue) {
    Push-Location cmd\venvctl
    try {
        go-winres make --in ..\..\winres\winres.json --arch ($Arch -join ",") --product-version $Version --file-version $numeric
        if ($LASTEXITCODE) { throw "go-winres failed" }
        $syso = Get-ChildItem rsrc_windows_*.syso | ForEach-Object FullName
    } finally { Pop-Location }
} else {
    Write-Warning "go-winres not found: building without version info, icon and long-path manifest. Install it with: go install github.com/tc-hib/go-winres@latest"
}

$ldflags = "-X venvctl/internal/cli.version=$Version -X venvctl/internal/cli.buildDate=$date"
try {
    foreach ($a in $Arch) {
        $env:GOOS = "windows"; $env:GOARCH = $a; $env:CGO_ENABLED = "0"
        $out = Join-Path $dist "venvctl-$Version-windows-$a"
        New-Item -ItemType Directory $out | Out-Null
        go build -trimpath -ldflags $ldflags -o (Join-Path $out "venvctl.exe") .\cmd\venvctl
        if ($LASTEXITCODE) { throw "go build failed for $a" }
        foreach ($f in "README.md", "LICENSE") { if (Test-Path $f) { Copy-Item $f $out } }
    }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    $syso | ForEach-Object { Remove-Item $_ -ErrorAction SilentlyContinue }
}

$exes = Get-ChildItem $dist -Recurse -Filter venvctl.exe | ForEach-Object FullName
if ($Sign) {
    $ts = if ($env:VENVCTL_SIGN_TIMESTAMP) { $env:VENVCTL_SIGN_TIMESTAMP } else { "http://timestamp.digicert.com" }
    $pass = @()
    if ($env:VENVCTL_SIGN_PASSWORD) { $pass = @("/p", $env:VENVCTL_SIGN_PASSWORD) }
    if ($env:VENVCTL_SIGN_CERT) {
        $signtool = (Get-Command signtool -ErrorAction SilentlyContinue).Source
        if (-not $signtool) {
            $signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
                Sort-Object FullName | Select-Object -Last 1 -ExpandProperty FullName
        }
        if (-not $signtool) { throw "signtool.exe not found; install the Windows SDK or put signtool on PATH" }
        foreach ($exe in $exes) {
            & $signtool sign /fd SHA256 /tr $ts /td SHA256 /f $env:VENVCTL_SIGN_CERT @pass $exe
            if ($LASTEXITCODE) { throw "signtool failed for $exe" }
        }
    } else {
        Write-Host "VENVCTL_SIGN_CERT is not set; would run:"
        foreach ($exe in $exes) {
            Write-Host "  signtool sign /fd SHA256 /tr $ts /td SHA256 /f <VENVCTL_SIGN_CERT> [/p <VENVCTL_SIGN_PASSWORD>] `"$exe`""
        }
    }
}

# Zip each arch (after signing, so the zips hold signed binaries), then checksum everything.
Get-ChildItem $dist -Directory | ForEach-Object {
    Compress-Archive -Path (Join-Path $_.FullName "*") -DestinationPath "$($_.FullName).zip"
}
$sums = Get-ChildItem $dist -Recurse -File -Include *.zip, venvctl.exe | Sort-Object FullName | ForEach-Object {
    $rel = $_.FullName.Substring($dist.Length + 1).Replace("\", "/")
    "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $rel
}
[IO.File]::WriteAllText((Join-Path $dist "SHA256SUMS"), ($sums -join "`n") + "`n")
Get-Content (Join-Path $dist "SHA256SUMS")
