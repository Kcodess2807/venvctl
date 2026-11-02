<h1 align="center">venvctl</h1>

<p align="center">
  <b>Control your Python virtual environments on Windows.</b><br>
  One <code>on</code>/<code>off</code> in PowerShell, CMD and Git Bash. Find, unlock, repair and prune venvs from a single binary.
</p>

<p align="center">
  <a href="https://github.com/Kcodess2807/venvctl/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/Kcodess2807/venvctl/ci.yml?branch=main&style=flat-square&label=ci"></a>
  <img alt="Windows 10/11" src="https://img.shields.io/badge/platform-Windows%2010%2F11-0078D4?style=flat-square">
  <img alt="PowerShell, CMD, Git Bash" src="https://img.shields.io/badge/shells-PowerShell%20%C2%B7%20CMD%20%C2%B7%20Git%20Bash-5391FE?style=flat-square">
  <a href="LICENSE"><img alt="MIT license" src="https://img.shields.io/badge/license-MIT-green?style=flat-square"></a>
  <a href="https://github.com/Kcodess2807/venvctl/releases/latest"><img alt="latest release" src="https://img.shields.io/github/v/release/Kcodess2807/venvctl?style=flat-square&label=release"></a>
  <a href="https://pypi.org/project/venvctl-win/"><img alt="PyPI venvctl-win" src="https://img.shields.io/pypi/v/venvctl-win?style=flat-square&label=pypi"></a>
</p>

<p align="center"><sub>venvctl creates environments with uv or venv and manages them afterwards. It does not install packages or Python versions.<br>Not related to the <code>venvctl</code> package on PyPI.</sub></p>

---

```text
PS D:\code\api> venvctl nuke old-api
1 process holds files in C:\Users\you\AppData\Local\venvctl\envs\old-api:
  PID  NAME    SPAWNED BY      OPEN FILES
30728  Python  powershell.exe  1: python.exe
terminate 1 process? [y/N]
```

Deleting a venv on Windows often fails with `Access is denied (os error 5)` and no explanation. venvctl identifies the process holding the files and what launched it, asks before stopping it, and refuses when the holder is an editor or a shell. The folder is renamed before it is deleted, so a failed delete never leaves a half-removed environment behind.

## Features

| | |
|---|---|
| **One activation command** | `venvctl on` and `venvctl off` behave the same in PowerShell 5.1 and 7, CMD and Git Bash. `Activate.ps1` is never run; PATH is saved once and restored exactly. |
| **Auto-activate on `cd`** | Opt-in. Only project roots you have approved with `venvctl allow` activate, because activating a venv runs its code. About 0.4 ms per prompt when nothing changed. |
| **Safe deletion** | `nuke` and `prune` name the locking processes via the Windows Restart Manager, protect editors and shells, and delete rename-first. PowerToys File Locksmith is used as a read-only fallback when installed. |
| **Discovery** | `scan` walks every fixed drive for `pyvenv.cfg`; `list` shows size, Python version, last use and broken or missing environments; `prune` removes them by age, size or status. |
| **Repair after a move** | `repair` rewrites the absolute paths that pip and uv launchers embed, so a renamed project folder works again without reinstalling. Environments venvctl creates are relocatable and never need it. |
| **Editor visibility** | Central environments are linked to their project through a PEP 832 `.venv` redirect file. |
| **Diagnostics** | `doctor` checks the Microsoft Store `python.exe` stub, the ExecutionPolicy scope that blocks your profile, long paths, the VC++ runtime, shell hooks and venvs inside cloud-synced folders. |
| **Scriptable** | Every command that prints results supports `--json`, and exit codes are fixed: 0 ok, 1 failure, 2 usage, 3 refused, 4 nothing to do. |

## Installation

**Requirements:** Windows 10 or 11 (x64 or arm64). No admin rights needed. Python itself is not required to install venvctl; [uv](https://docs.astral.sh/uv/) is used for `create` when it is installed.

### 1. Get `venvctl.exe`

Every option installs the same single binary. Pick one.

> The winget package is waiting for Microsoft's review. Until it shows up, use any of the other options.

**winget**

```powershell
winget install Kcodess2807.venvctl
```

**Installer script.** Downloads the latest release, checks it against `SHA256SUMS`, puts `venvctl.exe` in `%LOCALAPPDATA%\venvctl\bin` and adds that folder to your user `PATH`. It does not touch your shell profiles.

```powershell
irm https://raw.githubusercontent.com/Kcodess2807/venvctl/main/install.ps1 | iex
```

**pip.** The package is called `venvctl-win`; the name `venvctl` on PyPI belongs to an unrelated project.

```powershell
pip install venvctl-win
```

pip puts `venvctl.exe` in that Python's `Scripts\` folder. If `venvctl` is then "not recognized", that folder is not on your `PATH`; run `python -m venvctl_win init` instead in step 2. The hooks store the full path to the exe, so `on`/`off`/`cd` keep working either way.

**uv.** Same package, installed as a tool in its own isolated environment:

```powershell
uv tool install venvctl-win
```

If `venvctl` is "not recognized" afterwards, run `uv tool update-shell` once and open a new terminal.

**Just trying it?** `uvx` runs venvctl without installing anything. Good for the read-only commands:

```powershell
uvx --from venvctl-win venvctl doctor   # what's wrong with this machine's Python setup
uvx --from venvctl-win venvctl scan     # find every venv on your drives
```

`on`, `off` and `cd` need the shell hooks from step 2, which point at a fixed exe path, so use a real install for those.

**Build from source** (works today; needs [Go](https://go.dev/dl/) 1.26 or newer):

```powershell
git clone https://github.com/Kcodess2807/venvctl
cd venvctl
go build -o venvctl.exe ./cmd/venvctl

# put it in a folder on your user PATH
$bin = "$env:LOCALAPPDATA\venvctl\bin"
New-Item -ItemType Directory -Force $bin | Out-Null
Copy-Item venvctl.exe $bin
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$bin", "User")
```

Open a new terminal so the new `PATH` is picked up.

Release zips with `SHA256SUMS` and a [Scoop manifest](packaging/scoop/venvctl.json) will also be on the [Releases](https://github.com/Kcodess2807/venvctl/releases) page.

### 2. Install the shell hooks

`on`, `off` and `cd` change your current shell, which needs a small hook in each shell's startup file (PowerShell `$PROFILE`, the CMD AutoRun registry value, Git Bash `.bashrc`). Preview first, then install:

```powershell
venvctl init --dry-run   # shows every change it would make, changes nothing
venvctl init             # installs the hooks
```

Open a new terminal afterwards. If PowerShell refuses to load your profile ("running scripts is disabled"), `venvctl doctor` names the policy scope responsible and the fix.

### 3. Check it works

```powershell
venvctl version          # the installed version
venvctl doctor           # checks PATH, the Store python stub, ExecutionPolicy, hooks and more
cd D:\code\your-project
venvctl on               # activates the nearest .venv; your prompt shows its name
venvctl off
```

### Updating

Re-run the same install command (`winget upgrade Kcodess2807.venvctl`, the installer script, `pip install -U venvctl-win`, or `uv tool upgrade venvctl-win`). If the exe moved to a new folder, run `venvctl init` again; `venvctl doctor` warns when a hook points at an old path.

### Uninstalling

```powershell
venvctl init --remove    # removes the hooks from every shell
```

Then remove the binary the way you installed it (`winget uninstall Kcodess2807.venvctl`, `pip uninstall venvctl-win`, `uv tool uninstall venvctl-win`, or delete `%LOCALAPPDATA%\venvctl\bin` and its `PATH` entry). Your venvs and venvctl's registry live in `%LOCALAPPDATA%\venvctl`; delete that folder too only if you no longer want the central venvs inside it.

## Usage

```powershell
venvctl on                    # activate the nearest .venv
venvctl on api                # activate by name or path
venvctl off

venvctl create api            # central environment, linked to the current folder
venvctl create --local        # .\.venv in the current folder

venvctl scan                  # register every venv on your drives
venvctl list --sort size      # see them with size and last use
venvctl prune --older-than 180d --dry-run

venvctl nuke old-api          # delete, unlocking first
venvctl repair --dry-run      # preview the fix for a moved venv
venvctl doctor                # diagnose this machine's Python setup
```

Run `venvctl --help` for the full command list, or `venvctl <command> --help` for details and examples.

### Auto-activation

```powershell
venvctl config auto-activate on   # once; takes effect in new terminals
cd D:\code\api
venvctl allow                     # trust this project root
```

From then on, entering the project activates its venv and leaving deactivates it. Environments you or your editor activated by hand are never touched. In CMD this requires [Clink](https://chrisant996.github.io/clink/) and is experimental.

## How it works

```text
venvctl on ──► shell function ──► venvctl.exe prints a script ──► the shell evaluates it in place
               (installed once by `venvctl init` in $PROFILE, the CMD AutoRun registry value and .bashrc)
```

A child process cannot modify its parent shell's environment, so venvctl uses the same pattern as fnm, zoxide and starship: the binary prints shell code and a small function evaluates it. PowerShell still has to load your profile; if your execution policy prevents that, `venvctl doctor` names the scope responsible and the fix.

Full details, including the Restart Manager integration, launcher formats handled by `repair`, and the trust model, are in the [design notes](design.md) and the [project record](venvctl-complete.md).

## Comparison

| | Activate in every shell | Find venvs across drives | Delete a locked venv safely | Repair a moved venv |
|---|:-:|:-:|:-:|:-:|
| **uv** | – | – | – | avoided via `--relocatable` |
| **VS Code** | its own terminals | workspace only | – | – |
| **PowerToys File Locksmith** | – | – | finds more lockers; `--kill` stops all of them, no delete | – |
| **killpy** | – | ✓ more environment types | plain delete | – |
| **venvctl** | ✓ | ✓ | ✓ attribution, editor protection, rename-first | ✓ pip, venv and older uv |

## Status

v0.1.0 is the first release. The binaries are not code-signed yet, so Windows SmartScreen or your antivirus may warn on first run; every release lists the SHA256 of each file in `SHA256SUMS`.

- Tested on Windows 11 with PowerShell 5.1, CMD and Git Bash.
- CMD auto-activation (Clink), oh-my-posh and starship integration are untested.
- Repair of launchers written by uv 0.9.9 and later is verified against synthetic launchers only.

## Documentation

- [Design](design.md): architecture and command flows
- [JSON output](docs/json.md): schemas and exit codes
- [Redirect files](redirect.md): how PEP 832 `.venv` files are used
- [Project record](venvctl-complete.md): everything built, tested and decided

## License

[MIT](LICENSE)
