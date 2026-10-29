<p align="center">
  <samp>venvctl on · venvctl nuke · venvctl repair · venvctl doctor</samp>
</p>

<p align="center">
  <img alt="platform" src="https://img.shields.io/badge/platform-Windows%2010%2F11-0078D4?style=flat-square">
  <img alt="shells" src="https://img.shields.io/badge/shells-PowerShell%20%7C%20CMD%20%7C%20Git%20Bash-5391FE?style=flat-square">
  <img alt="binary" src="https://img.shields.io/badge/single%20binary-7.8%20MB-555?style=flat-square">
  <img alt="go" src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square">
  <img alt="status" src="https://img.shields.io/badge/status-pre--release-orange?style=flat-square">
</p>

# venvctl

<p align="center">
  <b>venvctl is a single-binary Windows CLI that manages your Python virtual environments.</b>
  <br>
  It gives you one <code>on</code>/<code>off</code> in PowerShell, CMD and Git Bash, finds forgotten venvs, deletes locked ones
  <br>
  without killing your editor, and repairs venvs after you move them. uv creates environments; venvctl manages them.
</p>

<p align="center"><sub>Not related to the <code>venvctl</code> package on PyPI.</sub></p>

<br>

---

```text
PS D:\code\api> venvctl nuke api
1 process(es) hold files in C:\Users\you\AppData\Local\venvctl\envs\api:
  PID  NAME    SPAWNED BY      OPEN FILES
30728  Python  powershell.exe  1: python.exe
terminate 1 process(es)? [y/N]
```

Windows normally answers "Access is denied (os error 5)" and never says who. venvctl names the process, says what started it, and asks first. It refuses outright when the holder is VS Code, PyCharm or a shell. It renames the venv away before deleting, so a failed delete never leaves it half-deleted.

<br>

---

## Why not just activate scripts and `uv run`?

| You hit | Usually | With venvctl |
|---|---|---|
| A different activate command per shell | `Activate.ps1`, `activate.bat`, `source activate` | `venvctl on`, the same in all three |
| `.venv` won't delete | close things at random, reboot | the lockers are named, your editor is protected, the delete is rename-first |
| A project folder was moved, so its `pip.exe` breaks | recreate the venv | `venvctl repair` rewrites the launcher paths |
| 50 forgotten venvs eating disk | WinDirStat and guesswork | `venvctl list --sort size`, then `venvctl prune --older-than 180d` |
| `python` opens the Microsoft Store | search the error | `venvctl doctor` says which alias it is and how to fix it |

It never runs `Activate.ps1`. PowerShell still needs to load your profile; if your policy blocks that, `venvctl doctor` explains the fix.

<br>

---

## How it compares

| | Activate in every shell | Find all venvs on disk | Delete a locked venv safely | Repair a moved venv |
|---|---|---|---|---|
| **uv** | ✗ no `activate` (by design) | ✗ | ✗ "Access is denied" | relocatable venvs avoid it |
| **VS Code** | inside its own terminals only | workspace only | ✗ | ✗ |
| **PowerToys File Locksmith** | ✗ | ✗ | finds more lockers than venvctl, but its `--kill` stops every one of them, with no delete step | ✗ |
| **killpy** | ✗ | ✓ more env types (conda, poetry, pipx) | ✗ plain delete | ✗ |
| **venvctl** | ✓ PowerShell 5.1/7, CMD, Git Bash | ✓ every fixed drive | ✓ names lockers, protects editors, asks, rename-first | ✓ pip/venv and older uv venvs |

When the Restart Manager names no locker, venvctl asks File Locksmith, if it's installed, for a read-only full scan.

<br>

---

## Numbers

| Measured on a Windows 11 laptop | |
|---|---|
| Venvs found by the author's first `venvctl scan` | 77 venvs, 15.8 GB |
| Auto-activate hook cost per prompt, folder unchanged | 0.43 ms (p99) |
| A full `venvctl` run, compared with an empty `python -c pass` | ~36 ms vs ~71 ms |

<br>

---

## Quick start

There are no release downloads yet, so build it from source. You need Go 1.26+.

```powershell
git clone <this repo> venvctl; cd venvctl
go build -o venvctl.exe ./cmd/venvctl
mkdir "$env:LOCALAPPDATA\venvctl\bin" -Force | Out-Null
copy venvctl.exe "$env:LOCALAPPDATA\venvctl\bin\"   # then add that folder to your user PATH
venvctl init --dry-run   # shows exactly what it would add to your shell profiles
venvctl init             # then open a new terminal
```

Five commands cover most days:

```powershell
venvctl on               # activate the nearest .venv (or: venvctl on api)
venvctl create api       # central venv, linked to this folder (uses uv if installed)
venvctl list --sort size # every venv with size and last use
venvctl nuke old-thing   # delete, unlocking it first
venvctl doctor           # what's wrong with this machine's Python setup
```

<br>

---

## How it works

```text
venvctl on ──→ shell function ──→ venvctl.exe prints a script ──→ the shell evaluates it in place
                  (installed by `venvctl init` in $PROFILE, CMD AutoRun, .bashrc)
```

- **Activation** works the way fnm, zoxide and starship do. A child process can't change your shell, so the shell evaluates text the binary prints. PATH is saved once and restored exactly.
- **Auto-activate on `cd`** is opt-in (`venvctl config auto-activate on`). It only activates folders you trust with `venvctl allow`, because activating a venv runs its code. In CMD it needs Clink and is experimental (untested).
- **`create` makes relocatable venvs** with uv 0.2.31+, so they never need repair. `venvctl repair` fixes venvs made by pip/venv and older uv after their folder moved; new uv venvs made with `--relocatable` don't need it.
- **Editors find central venvs** through a PEP 832 `.venv` redirect file in the project folder (draft PEP; see [redirect.md](redirect.md)).
- **Scripting** is supported: every command has `--json` and fixed exit codes (0 ok, 1 failure, 2 usage, 3 refused, 4 nothing to do). See [docs/json.md](docs/json.md).

<br>

---

## Status and limits

- Pre-release, not signed yet, Windows only by design.
- Tested on PowerShell 5.1, CMD and Git Bash. Clink, oh-my-posh and starship integration are untested.
- Repair of uv ≥ 0.9.9 launchers is tested on synthetic launchers, not yet on a real current-uv install.
- `venvctl init --remove` takes every hook out again: profile lines, the AutoRun entry and `.bashrc`.

## Docs

- [The complete project record](venvctl-complete.md)
- [Design](design.md)
- [JSON output](docs/json.md)
- [`.venv` redirect files](redirect.md)
- [Decision log](floated.md)

---

<p align="center">
  <a href="venvctl-complete.md">Complete record</a> · <a href="docs/json.md">JSON</a> · <a href="redirect.md">Redirect files</a>
</p>

<p align="center">
  <sub>License: not chosen yet (add a LICENSE file before publishing).</sub>
</p>
