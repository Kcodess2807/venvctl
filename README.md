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
  <img alt="pre-release" src="https://img.shields.io/badge/status-pre--release-orange?style=flat-square">
</p>

<p align="center"><sub>venvctl creates environments with uv or venv and manages them afterwards. It does not install packages or Python versions.<br>Not related to the <code>venvctl</code> package on PyPI.</sub></p>

---

```text
PS D:\code\api> venvctl nuke old-api
1 process(es) hold files in C:\Users\you\AppData\Local\venvctl\envs\old-api:
  PID  NAME    SPAWNED BY      OPEN FILES
30728  Python  powershell.exe  1: python.exe
terminate 1 process(es)? [y/N]
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
| **Scriptable** | Every command supports `--json`, and exit codes are fixed: 0 ok, 1 failure, 2 usage, 3 refused, 4 nothing to do. |

## Installation

Pre-built binaries and package-manager installs will come with the first release. Until then, build from source with Go 1.26 or newer:

```powershell
git clone https://github.com/Kcodess2807/venvctl
cd venvctl
go build -o venvctl.exe ./cmd/venvctl
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\venvctl\bin" | Out-Null
Copy-Item venvctl.exe "$env:LOCALAPPDATA\venvctl\bin\"
```

Add `%LOCALAPPDATA%\venvctl\bin` to your user `PATH`, then install the shell hooks:

```powershell
venvctl init --dry-run   # shows every change it would make to your shell profiles
venvctl init             # installs the hooks; open a new terminal afterwards
```

`venvctl init --remove` uninstalls the hooks again.

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
               (installed once by `venvctl init` in $PROFILE, the CMD AutoRun key and .bashrc)
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

venvctl is pre-release. Binaries are unsigned, and there are no release downloads yet.

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
