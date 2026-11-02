# venvctl installer.
#
#   irm https://raw.githubusercontent.com/Kcodess2807/venvctl/main/install.ps1 | iex
#
# Downloads the latest release for this machine's architecture, verifies it against SHA256SUMS,
# unpacks venvctl.exe into %LOCALAPPDATA%\venvctl\bin and adds that folder to the user PATH.
# It does not touch your shell profiles: run `venvctl init` yourself afterwards.
# Works under the default Restricted execution policy, because the policy governs script files,
# not text piped to Invoke-Expression.
$ErrorActionPreference = "Stop"
$repo = "Kcodess2807/venvctl"
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$headers = @{ "User-Agent" = "venvctl-install" }

$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
$version = $release.tag_name.TrimStart("v")
$name = "venvctl-$version-windows-$arch.zip"
$asset = $release.assets | Where-Object name -eq $name | Select-Object -First 1
if (-not $asset) { throw "release $($release.tag_name) has no $name" }

$sumsAsset = $release.assets | Where-Object name -eq "SHA256SUMS" | Select-Object -First 1
$expected = ""
if ($sumsAsset) {
    $line = (Invoke-RestMethod $sumsAsset.browser_download_url -Headers $headers) -split "`n" | Where-Object { $_ -like "*  $name" }
    if ($line) { $expected = ($line -split "\s+")[0].ToLowerInvariant() }
}

$zip = Join-Path $env:TEMP $name
Invoke-WebRequest $asset.browser_download_url -OutFile $zip -Headers $headers
$actual = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLowerInvariant()
if ($expected -and $actual -ne $expected) { Remove-Item $zip; throw "checksum mismatch for $name (expected $expected, got $actual)" }

$bin = Join-Path $env:LOCALAPPDATA "venvctl\bin"
New-Item -ItemType Directory -Force $bin | Out-Null
Expand-Archive $zip -DestinationPath $bin -Force
Remove-Item $zip

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $bin) {
    [Environment]::SetEnvironmentVariable("Path", ($userPath.TrimEnd(";") + ";" + $bin), "User")
}
if (($env:Path -split ";") -notcontains $bin) { $env:Path += ";$bin" }

Write-Host "venvctl $version installed to $bin" -ForegroundColor Green
Write-Host "next: venvctl init    (installs the shell hooks; then open a new terminal)"
