# Manual end-to-end test of venvctl auto-activate in PowerShell.
#
#   powershell -NoProfile -File testdata\autocd.ps1 [-Exe path\to\venvctl.exe]
#
# It builds a temp tree with real venvs (uv, else python -m venv), points VENVCTL_HOME at a temp
# folder, loads `venvctl init pwsh` with auto-activate forced on into THIS session only, and drives
# the real prompt function through cd transitions. It never reads or writes your $PROFILE, your
# real trust.json or registry, and deletes the temp tree at the end. Exit code = number of failures.
param([string]$Exe = (Join-Path $PSScriptRoot '..\venvctl.exe'))

$ErrorActionPreference = 'Stop'
$Exe = (Resolve-Path $Exe).Path
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("venvctl-autocd-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$fails = 0

function New-Venv($dir) {
    New-Item -ItemType Directory -Force (Split-Path $dir) | Out-Null
    if (Get-Command uv -ErrorAction SilentlyContinue) { uv venv -q $dir } else { python -m venv --without-pip $dir }
}
function Step($label, $dir, $wantVenv, $wantAuto) {
    Set-Location -LiteralPath $dir
    $null = prompt                       # what the console does before every prompt
    $got = if ($env:VIRTUAL_ENV) { $env:VIRTUAL_ENV } else { '' }
    $auto = [bool]$env:_VENVCTL_AUTO
    $ok = ($got -eq $wantVenv) -and ($auto -eq $wantAuto)
    $mark = if ($ok) { 'PASS' } else { $script:fails++; 'FAIL' }
    "{0}  {1,-44} VIRTUAL_ENV={2} auto={3}" -f $mark, $label, ($got.Replace($tmp, '<tmp>')), $auto
    if (-not $ok) { "      want VIRTUAL_ENV={0} auto={1}" -f $wantVenv.Replace($tmp, '<tmp>'), $wantAuto }
}

try {
    # A: trusted project with a subfolder and a nested package that has its own venv.
    # B: second trusted project. C: untrusted project. D: pyproject.toml only. M: manual venv.
    'A', 'B', 'C' | ForEach-Object { New-Venv "$tmp\$_\.venv" }
    New-Venv "$tmp\A\pkg\.venv"
    New-Venv "$tmp\M\.venv"
    New-Item -ItemType Directory -Force "$tmp\A\sub\deep", "$tmp\D", "$tmp\home" | Out-Null
    Set-Content "$tmp\D\pyproject.toml" '[project]'
    Set-Content "$tmp\A\pkg\pyproject.toml" '[project]'

    $env:VENVCTL_HOME = "$tmp\home"; $env:VENVCTL_AUTO_ACTIVATE = '1'
    Remove-Item Env:VIRTUAL_ENV, Env:_VENVCTL_AUTO, Env:_VENVCTL_OLD_PATH -ErrorAction SilentlyContinue
    function global:prompt { 'PS> ' }    # plain prompt, so the hook wraps it
    & $Exe init pwsh | Out-String | Invoke-Expression
    & $Exe allow "$tmp\A" | Out-Null; & $Exe allow "$tmp\B" | Out-Null; & $Exe allow "$tmp\A\pkg" | Out-Null
    $origPath = $env:PATH

    Step 'enter trusted A'                    "$tmp\A"          "$tmp\A\.venv"     $true
    Step 'deeper inside A: no-op'             "$tmp\A\sub\deep" "$tmp\A\.venv"     $true
    Step 'nested package: closest .venv wins' "$tmp\A\pkg"      "$tmp\A\pkg\.venv" $true
    Step 'switch to trusted B'                "$tmp\B"          "$tmp\B\.venv"     $true
    Step 'untrusted C: leaves B, hint only'   "$tmp\C"          ''                 $false
    Step 'pyproject-only D: nothing'          "$tmp\D"          ''                 $false
    "PASS  PATH restored after auto deactivate: $($env:PATH -eq $origPath)"
    if ($env:PATH -ne $origPath) { $fails++ }

    venvctl on "$tmp\M\.venv" | Out-Null      # manual activation
    Step 'manual venv survives entering A'    "$tmp\A"          "$tmp\M\.venv"     $false
    Step 'manual venv survives leaving A'     "$tmp\D"          "$tmp\M\.venv"     $false
    venvctl off | Out-Null
    Step 'after off, A auto-activates again'  "$tmp\A"          "$tmp\A\.venv"     $true
    & $Exe deny "$tmp\A" | Out-Null
    Step 'denied A: left on next root change' "$tmp\B"          "$tmp\B\.venv"     $true
    Step 'denied A: not activated'            "$tmp\A"          ''                 $false
    Step 'leave everything'                   $tmp              ''                 $false
} finally {
    Set-Location ([IO.Path]::GetTempPath())
    Remove-Item Env:VENVCTL_HOME, Env:VENVCTL_AUTO_ACTIVATE -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
"$fails failure(s)"
exit $fails
