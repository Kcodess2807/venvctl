# pyGo: complete project record

Everything built, decided, tested, fixed and left open while building pyGo, as of 29 Oct 2026.
For a shorter view, see `design.md` (architecture) and `floated.md` (decision log).

---

## 1. What pyGo is

pyGo (command: `pygo`) is a single-binary, Windows-native manager for Python virtual environments, written in Go. It does not replace uv or pip. It manages the environments themselves: activating, finding, deleting, repairing and diagnosing them.

### The four problems it solves

| # | Problem on Windows | pyGo answer |
|---|---|---|
| 1 | Activation syntax differs per shell (`Activate.ps1`, `activate.bat`, `source activate`), and PowerShell often refuses `Activate.ps1` with `PSSecurityException` because of ExecutionPolicy. | `pygo on` / `pygo off` work the same in PowerShell, CMD and Git Bash, and never run a `.ps1` file. |
| 2 | A compiled program cannot change its parent shell's environment variables. This is why uv has no `uv activate`. | A small shell function, installed once by `pygo init`, evaluates the text `pygo` prints. |
| 3 | Deleting a `.venv` fails with `Access is denied (os error 5)` because `python.exe`, `ruff.exe`, language servers and so on hold files open. | `pygo nuke` asks Windows which processes hold the files, shows them, and kills them after confirmation. |
| 4 | Dozens of forgotten venvs eat 20 GB+, with no list, no way to jump to them, and breakage after a folder rename. | `pygo scan`, `pygo list`, `pygo cd`, `pygo repair`. |

Plus `pygo doctor`, which diagnoses the usual machine-level causes: Store python stubs, ExecutionPolicy, long paths, the VC++ runtime and so on.

### Tech stack

| Piece | Choice |
|---|---|
| Language | Go. The spec said 1.22+. `go.mod` now says `go 1.26.0` because the latest dependencies require it. Built with Go 1.27.3. |
| CLI framework | `github.com/spf13/cobra` v1.10.2 |
| Output styling | `github.com/charmbracelet/lipgloss` v1.1.0 (colored marks, bold, faint) plus stdlib `text/tabwriter` for tables |
| Windows APIs | `golang.org/x/sys/windows` and `golang.org/x/sys/windows/registry` v0.49.0 |
| Venv creation | Delegates to `uv venv` when uv is on PATH, else `python -m venv` |
| Not used | bubbletea / huh. Nothing is interactive enough yet. The one prompt (`[y/N]` in nuke) is a plain `fmt.Scanln`. |

---

## 2. Timeline of what happened

1. **Brief received.** A spec named the tool WinVenv with the binary `wv`, along with the package layout.
2. **No Go on the machine.** I downloaded the official Go 1.27.3 zip and extracted it to `%LOCALAPPDATA%\Programs\Go`. That's user-local, so no admin was needed.
3. **Code written.** I wrote all seven Go source files and one unit test.
4. **First build and smoke test.** This exposed three bugs, which I fixed (section 9.1).
5. **Lock detection verified** against a live `python.exe`. Getting a process that stays alive took three attempts, because the first two test processes exited instantly.
6. **End-to-end shell tests.** PowerShell passed under a `Restricted` policy, and CMD passed through manual macro expansion.
7. **Docs written.** `design.md` and `floated.md`.
8. **Go put on your user PATH.**
9. **`wv.exe` installed** to `%LOCALAPPDATA%\winvenv\bin` and that folder added to your user PATH.
10. **`wv init` blocked for me.** The Claude Code safety classifier classes editing shell profiles and AutoRun as "persistence". You ran it yourself, after a first try with a stray `!` prefix in your own terminal.
11. **Hooks verified** in a fresh PowerShell.
12. **Research brief received.** I verified its claims on this machine. This found a real repair bug with uv launchers, and I made eight hardening changes (section 9.2).
13. **Ponytail audit.** It found 12 simplifications, about 54 lines. None are applied (section 10).
14. **Renamed to pyGo.** Code, folders, state folder, PATH and registry were all moved. The shell hook migration is left for you to run (section 11).
15. **This document.**

---

## 3. Project layout

```
D:\PROJECTS\pygo\
├── cmd\pygo\main.go               13 lines   entry point, calls cli.Execute
├── internal\
│   ├── cli\cli.go                 494 lines  every cobra command, output styling, nuke confirm
│   ├── discovery\discovery.go     244 lines  pyvenv.cfg scanner, PEP 514 registry reader, drives
│   ├── doctor\doctor.go           278 lines  the 9 diagnostics
│   ├── envmgr\envmgr.go           341 lines  registry.json, config.json, resolve, create, delete, repair
│   ├── envmgr\envmgr_test.go       37 lines  launcher path-rewrite test
│   ├── shell\shell.go             253 lines  hook text per shell, activate/deactivate/cd scripts, init install
│   └── winapi\winapi.go           187 lines  Restart Manager, process table, kill, Store alias detection
├── go.mod / go.sum
├── .gitignore                     ignores /pygo.exe and /dist/
├── pygo.exe                       latest build (unstripped, about 7.1 MB)
├── design.md                      architecture summary
├── floated.md                     decision log (16 entries)
└── pygo-complete.md               this file
```

About 1,850 lines of Go in total.

### Package dependencies (one direction, no cycles)

```
cmd/pygo ──> cli ──┬──> doctor ──┬──> discovery
                   │             ├──> envmgr ──> discovery
                   │             ├──> shell
                   │             └──> winapi
                   ├──> envmgr
                   ├──> shell
                   ├──> winapi
                   └──> discovery
```

There's no interface or abstraction layer. Each package is a set of plain functions.

---

## 4. Files pyGo keeps on disk

All under `%LOCALAPPDATA%\pygo\` (on this machine `C:\Users\Piyush\AppData\Local\pygo\`):

| Path | Purpose |
|---|---|
| `bin\pygo.exe` | The installed binary. This folder is on your user PATH. |
| `registry.json` | Every known venv: `name`, `path`, `python` (base interpreter dir from `pyvenv.cfg`), `version`, `central`, `last_used`. Sorted by name. Currently 77 entries, 15.8 GB. |
| `config.json` | Optional, hand-edited. `scan_roots` (list of folders for `pygo scan`) and `default_python` (for `pygo create`). No command writes it. |
| `envs\<name>\` | Central environments made by `pygo create <name>`. |
| `pygo_hook.cmd` | The CMD hook: one `doskey` line, called by AutoRun. |
| `pygo_env.cmd` | Rewritten on every CMD `pygo` call. The doskey macro `call`s it. |

Shell profile entries that `pygo init` adds:

| Shell | Where | Line |
|---|---|---|
| Windows PowerShell 5.1 / PowerShell 7 | each `$PROFILE` | `& '<path>\pygo.exe' init pwsh \| Out-String \| Invoke-Expression` |
| CMD | `HKCU\Software\Microsoft\Command Processor\AutoRun` | `if exist "<...>\pygo_hook.cmd" call "<...>\pygo_hook.cmd"` (appended with ` & ` if AutoRun already had something) |
| Git Bash | `%USERPROFILE%\.bashrc` | `eval "$('<posix path>/pygo.exe' init bash)"` |

Each entry is preceded by the comment `# pyGo shell hook` and added only once, even if you rerun init.

---

## 5. Command reference

| Command | Aliases | Flags | What it does |
|---|---|---|---|
| `pygo on [name\|path]` | `activate` | | Activates a venv. With no argument it uses the nearest `.venv` or `venv` walking up from the current folder. Needs the shell hook. |
| `pygo off` | `deactivate` | | Restores PATH and prompt. Needs the hook. |
| `pygo cd <name\|path>` | | | Changes the shell to the venv's project folder, the parent of `.venv`. Needs the hook. |
| `pygo init [pwsh\|cmd\|bash]` | | | With no argument it installs hooks into every detected shell. With a shell name it prints that shell's hook text, which is what profiles evaluate. |
| `pygo create [name]` | | `-p/--python 3.12`, `-l/--local` | Makes a central env at `envs\<name>`, or `.\.venv` with `--local`. Uses uv if present. Registers it. |
| `pygo list` | `ls` | | Table of name, Python version, size, last used and path. It flags `missing` (folder gone) and `broken-base` (base interpreter gone), and prints the total size. Sizes are computed in parallel. |
| `pygo scan [root...]` | | | Walks the roots, or `config.json` `scan_roots`, or all fixed drives. Registers every `pyvenv.cfg` it finds, unregisters entries whose folder is gone, and always includes the central `envs` folder. |
| `pygo nuke <name\|path>...` | `rm`, `remove` | `-f/--force` | Deletes venvs safely (section 7.2). |
| `pygo repair [path]` | | `-n/--dry-run` | Fixes a moved or renamed venv (section 7.3). |
| `pygo doctor` | | | Runs 9 checks. Exit code 1 only on hard failures, not warnings. |
| `pygo __shell <shell> ...` | | | Hidden. Called only by the shell wrappers. |

### How a name or path is resolved (`envmgr.Resolve`)

1. With no argument it walks up from the current folder, looking for `.venv` or `venv`.
2. If the argument is a folder containing `pyvenv.cfg`, it uses that.
3. If the argument is a name under `%LOCALAPPDATA%\pygo\envs\`, it uses that.
4. If the argument matches a registered name, case-insensitively, it uses that. If two venvs share the name (you have several, like `gravity-render`), it is an error asking for a path.

A `.venv` that is a file, not a folder, is treated as a PEP 832 redirect (section 7.5).

Local `.venv` and `venv` folders are named after their project folder, so `D:\PROJECTS\AeroMix\.venv` lists as `AeroMix`.

---

## 6. How activation works

### 6.1 The core trick

A child process can't change its parent's environment. So:

1. `pygo init` puts a small function named `pygo` into each shell.
2. When you type `pygo on`, `pygo off` or `pygo cd`, the function runs `pygo.exe __shell <shell> on ...`.
3. `pygo.exe` resolves the venv, stamps `last_used` in the registry, and prints a script as plain text.
4. The function evaluates that text inside the current shell.
5. Every other subcommand (`list`, `nuke`, and so on) passes straight through to `pygo.exe`.

The set of intercepted verbs is `on, activate, off, deactivate, cd`. It's defined in `shell.Handled` and repeated in the wrapper text.

### 6.2 Why ExecutionPolicy doesn't block it

ExecutionPolicy governs script files (`.ps1`). It doesn't govern strings passed to `Invoke-Expression`. pyGo never runs `Activate.ps1`. It evaluates text, so `pygo on` worked in a test shell forced to `Restricted`.

One caveat remains. `$PROFILE` is itself a `.ps1`, so under `Restricted` or `AllSigned` the profile, and with it the hook, does not load. `pygo doctor` reports this and names the deciding scope. On Group Policy-locked machines, where `-ExecutionPolicy Bypass` is ignored, it suggests starting shells with:

```
powershell -NoExit -Command "pygo init pwsh | Out-String | iex"
```

### 6.3 What the activate script does, per shell

| Step | PowerShell | CMD | Git Bash |
|---|---|---|---|
| Save original PATH once | `$env:_PYGO_OLD_PATH` | `_PYGO_OLD_PATH` | `_PYGO_OLD_PATH` |
| Set venv vars | `VIRTUAL_ENV`, `VIRTUAL_ENV_PROMPT` | same | same, with a POSIX path like `/c/...` |
| PATH | `<venv>\Scripts;` + saved PATH | same | `<venv>/Scripts:` + saved PATH |
| Clear `PYTHONHOME` | yes | yes | yes |
| Prompt | wraps `prompt` to prefix `(name)`, unless oh-my-posh or starship is active | `PROMPT=(name) <old>` saved in `_PYGO_OLD_PROMPT` | `PS1` prefix saved in `_PYGO_OLD_PS1`, unless oh-my-posh or starship is active |
| Message | `activated <name>` | same | same |

Because the original PATH is saved only once, activating a second venv replaces the first instead of stacking. `pygo off` restores PATH and prompt exactly and removes the variables.

PowerShell strings are escaped for single quotes (`'` becomes `''`). Bash strings are escaped as `'\''`.

### 6.4 PowerShell specifics

- The wrapper switches `[Console]::OutputEncoding` to UTF-8 around the `pygo.exe` call and restores it afterwards. PowerShell 5.1 otherwise decodes native output with the OEM codepage, which garbles non-ASCII paths. I tested a venv under an Arabic-plus-accented folder name.
- Prompt engines are detected with `POSH_THEME`/`POSH_SHELL` (oh-my-posh) and `STARSHIP_SHELL`. When one is active, pyGo leaves the prompt alone, and those engines show the venv from `VIRTUAL_ENV`/`VIRTUAL_ENV_PROMPT`. You have oh-my-posh installed.
- `init` asks both `pwsh` and `powershell` for `$PROFILE`, because 7 and 5.1 use different files. On this machine only Windows PowerShell 5.1 exists.

### 6.5 CMD specifics

CMD has no eval, so the hook is a doskey macro:

```
doskey pygo="<path>\pygo.exe" __shell cmd $* $T call "<...>\pygo_env.cmd"
```

`$T` separates commands in doskey. Every `pygo` call goes through `__shell cmd`, which:

- writes the activate, deactivate or cd script to `pygo_env.cmd` for hook verbs, or
- writes an empty `pygo_env.cmd` and runs the normal command itself for everything else.

The file is rewritten on every call, even on errors, so a stale script never runs again.

Doskey macros only work at an interactive CMD prompt, not inside `.bat` files or under `cmd /c`.

I deliberately did not add a recursion-guard variable (decision 15 in `floated.md`). The hook is one doskey line with no subprocess, so it can't recurse. A guard variable would be inherited by child CMD windows, which don't inherit doskey macros, so it would break `pygo` in them.

### 6.6 Git Bash specifics

Paths are converted from `C:\a\b` to `/c/a/b`, the same as CPython's own `activate` on MSYS.

---

## 7. Windows internals in detail

### 7.1 Restart Manager (who is locking my files?)

`golang.org/x/sys/windows` does not wrap `rstrtmgr.dll`, so pyGo loads it with `windows.NewLazySystemDLL` and calls four functions:

1. `RmStartSession`
2. `RmRegisterResources` with every `.exe`, `.dll` and `.pyd` under the venv. Those are the file types that block deletes in practice, so the list is hundreds of files, not tens of thousands.
3. `RmGetList` in a loop. It retries up to five times on `ERROR_MORE_DATA` (234), which happens when the list grows between the size query and the real call.
4. `RmEndSession` (deferred)

It needs no admin rights for processes in your own session.

For each locker, pyGo adds:

- **Exe path** via `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `QueryFullProcessImageName`
- **Parent process name** from a Toolhelp snapshot (`CreateToolhelp32Snapshot` + `Process32First/Next`), shown as "SPAWNED BY" (for example `Code.exe` or `powershell.exe`)

The Go struct layouts (`rmUniqueProcess`, `rmProcessInfo` with `[256]uint16` app name and `[64]uint16` service name) mirror the Win32 headers.

### 7.2 Nuke flow

```
resolve target
  folder already gone? -> unregister, done
Restart Manager -> lockers
  any locker is an editor/shell (Code, Code - Insiders, Cursor, PyCharm, IntelliJ,
  Visual Studio, Explorer, Windows Terminal, pwsh, powershell, cmd, bash)?
      -> refuse; you close it yourself
  else --force?                -> kill all
  else console attached?       -> "kill N process(es)? [y/N]"
  else (scripts, pipes)        -> error, nothing deleted
kill = OpenProcess(PROCESS_TERMINATE) + TerminateProcess
delete:
  rename <dir> -> <dir>.<nanoseconds>.pygo-trash   (retries 0/50/100/200/400/800 ms)
     rename failed -> "still locked, nothing deleted", venv untouched
  clear read-only bits under the trash folder
  RemoveAll(trash)                                  (same backoff)
     failed -> "venv removed but leftovers remain in <trash>"
unregister; print "freed <size>"
```

Why rename first: uv issue #11092 shows venvs left half-deleted and corrupted when a delete fails midway. A rename is all-or-nothing, so a locked venv stays intact.

Why backoff: handles from killed processes and antivirus scans release a few hundred milliseconds late.

The registry is saved even if a later argument fails, so venvs deleted earlier in the same command are still unregistered.

### 7.3 Repair (after moving or renaming a venv)

A venv records its own absolute path in several places in `Scripts\`. After a move, the launchers like `pip.exe` and uv-installed commands point at the old path.

Algorithm:

1. Read the old location from `Scripts\activate.bat`. Two formats are recognized:
   - `python -m venv`: `set "VIRTUAL_ENV=C:\old\path"`
   - uv: `@for %%i in ("C:\old\path") do @set "VIRTUAL_ENV=%%~fi"`
2. If old equals current, do nothing ("already points at ...").
3. For every file in `Scripts\`:
   - **uv launcher** (file ends in `UVSC`, `UVPY` or the old `UVUV`). The layout is `[launcher exe][zip][python path utf-8][u32 little-endian length][magic]`. pyGo replaces the path inside that trailer and rewrites the length field.
   - **Everything else** (pip/distlib launchers, `activate`, `activate.bat`, `activate.fish`, and so on) gets a plain byte replace of the old path, in both `C:\` and `C:/` forms. For pip launchers the path sits in a `#!` shebang before a zip. Shifting the zip is harmless because zipimport finds it from the end-of-file record.
4. Update the registry: remove the old path and add the new one.

`--dry-run` lists the files that would change and writes nothing. Rewriting `.exe` files looks like malware behavior to antivirus software, so preview first if in doubt.

`pyvenv.cfg` is deliberately left alone. Its `home` points at the base Python, which doesn't move when the venv moves.

### 7.4 Store app execution alias detection

`%LOCALAPPDATA%\Microsoft\WindowsApps\python.exe` is a 0-byte reparse point that opens the Microsoft Store. pyGo detects it with `FindFirstFile`. That reports `FILE_ATTRIBUTE_REPARSE_POINT` with reparse tag `IO_REPARSE_TAG_APPEXECLINK` (`0x8000001B`) in `Reserved0`.

### 7.5 PEP 832 redirect files (read-only support)

PEP 832 (draft, targeting Python 3.15) lets a project's `.venv` be a one-line text file pointing at the real venv. pyGo follows such files:

- one UTF-8 line, with a BOM tolerated
- a relative path is resolved against the file's folder
- the file must be under 32 KB and is treated as path data only, never executed
- the venv is named after the project folder

pyGo does not write redirect files yet. The PEP is a draft, and tools that expect `.venv` to be a folder could break on a file.

### 7.6 PEP 514 interpreter discovery

Installed Pythons are read from three registry roots:

- `HKCU\Software\Python`
- `HKLM\Software\Python`
- `HKLM\Software\WOW6432Node\Python` (32-bit)

For each `<Company>\<Tag>`, pyGo reads `InstallPath\ExecutablePath` (or `InstallPath` default + `python.exe`), `Version` or `SysVersion`, and `SysArchitecture`. It skips installs whose exe no longer exists and removes duplicates by exe path. Results are sorted newest first, with 64-bit before 32-bit.

`FindInterpreter("3.12")` returns the newest match. With no version requested it falls back to `python` on PATH. This is only used when uv isn't installed.

### 7.7 Filesystem scan

- One goroutine per folder, with at most 64 directory reads running at once
- Stops going deeper once a folder contains `pyvenv.cfg`
- Skips symlinks and these folders (case-insensitive): `Windows`, `Program Files`, `Program Files (x86)`, `ProgramData`, `$Recycle.Bin`, `System Volume Information`, `AppData`, `node_modules`, `.git`, `__pycache__`, `.cache`, `site-packages`
- Default roots: every fixed drive via `GetLogicalDrives` + `GetDriveType == DRIVE_FIXED`
- OneDrive placeholder folders are reparse points, but Go reports them as normal directories (I verified this on your OneDrive), so they're scanned

---

## 8. Doctor: the 9 checks

| Check | Pass | Warn | Fail | Fix it suggests |
|---|---|---|---|---|
| python on PATH | first `python.exe` on PATH is a real install | none on PATH | first one is the Store stub | turn off App execution aliases, or reorder PATH |
| Store app execution alias | no stubs | `python.exe`/`python3.exe` stubs exist | | disable them in Settings |
| registered interpreters (PEP 514) | lists versions | none registered | | uv-managed Pythons still work |
| PowerShell ExecutionPolicy | first non-Undefined scope (ignoring the probe's own Process scope) allows scripts | | `Restricted` or `AllSigned` | `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`, or the Group Policy workaround |
| LongPathsEnabled | `1` | disabled | | admin `reg add ... LongPathsEnabled /d 1` |
| VC++ 2015-2022 runtime | `System32\vcruntime140.dll` exists | | missing | install `vc_redist.x64.exe` |
| uv | on PATH | missing | | uv install one-liner |
| shell hooks | all detected shells hooked | some or none missing | | `pygo init` |
| venvs in cloud-synced folders | none | registered venvs under OneDrive, Dropbox, Google Drive or iCloudDrive | | move out, nuke the synced copy |

### Your machine right now

| Check | Result |
|---|---|
| python on PATH | ✓ `C:\ProgramData\anaconda3\python.exe` (+3 more later in PATH) |
| Store alias | ! stubs present, harmless because Anaconda comes first |
| interpreters | ✓ 3.13.7, 3.12.3, 3.11.7, 3.10.0, plus one PythonCore/3.11 entry with no version |
| ExecutionPolicy | ✓ `RemoteSigned`, set at `CurrentUser` scope |
| LongPathsEnabled | ! disabled |
| VC++ runtime | ✓ present |
| uv | ✓ `C:\Users\Piyush\.local\bin\uv.exe` |
| hooks | ! reports powershell and bash installed, cmd missing. This is misleading (section 12). |
| cloud venvs | ✓ none |

---

## 9. Bugs found and fixed

### 9.1 From the first smoke test

| Bug | Cause | Fix |
|---|---|---|
| `repair` failed on every uv-made venv: "cannot determine previous location" | uv's `activate.bat` puts the path in a `for %%i in ("...")` line with a leading `@`, not `set VIRTUAL_ENV=` | the pattern accepts both forms |
| doctor showed warnings as failures and exited 1 on a fresh install | checked `!OK` before `Warn` | warn wins; missing hooks became a warning |
| a blank `()` interpreter row | a registry entry with no version or architecture | falls back to `Company/Tag` |

### 9.2 From the research brief

| Change | Why |
|---|---|
| **uv launcher repair (real bug, reproduced).** Current uv ends console-script launchers with `UVSC`, but pyGo only knew `UVUV`/`UVPY`. Repair fell back to a plain replace, the length field went stale, and the command died with "Failed to canonicalize script path". | The fix handles all three magics. The test now uses `UVSC`. A real `pyflakes.exe` runs after a move and repair. |
| `repair --dry-run` | preview before rewriting `.exe` files |
| nuke: parent process, `[y/N]` prompt, never kill editors or shells | a wrong kill loses unsaved work in an editor |
| Restart Manager retry on `ERROR_MORE_DATA` | lock list can grow between calls |
| rename-first delete with backoff | no more half-deleted venvs |
| read PEP 832 redirect files | forward-compatible, costs nothing |
| UTF-8 around the PowerShell wrapper call | non-ASCII paths in PS 5.1 |
| leave oh-my-posh / starship prompts alone; set `VIRTUAL_ENV_PROMPT` | don't fight prompt engines |
| doctor names the ExecutionPolicy scope; Group Policy advice | it used to report the probe shell's own `Bypass`, not your real `RemoteSigned` |
| doctor cloud-folder check | a venv is 5-25k files to sync |
| installed binary built without `-ldflags "-s -w"` | Defender has flagged stripped Go builds |

### 9.3 Build hiccups during the brief work

These were editing mistakes, not product bugs:

- an edit stored a raw BOM character in Go source instead of the `\ufeff` escape
- GNU sed read `\u` as "uppercase next letter" and wrote `Feff`

Both were fixed. The source now uses `"\xef\xbb\xbf"`.

---

## 10. Ponytail audit (findings, not applied)

| # | Tag | Cut | Replacement | ~Lines |
|---|---|---|---|---|
| 1 | stdlib | `versionLess` + `atoiPrefix` in discovery | `strconv.Atoi` per part + `slices.Compare` | -12 |
| 2 | delete | `Venv.Home`/`Version` and the cfg parse in `Scan` (the scan command re-parses anyway) | `Scan` returns `[]string` | -8 |
| 3 | yagni | `NotFoundError` type | `fmt.Errorf` | -8 |
| 4 | shrink | two copy-pasted backoff loops in `Delete` | one `retry` closure | -7 |
| 5 | shrink | `$PROFILE` lookup duplicated in shell and doctor | one `shell.Profiles()` | -6 |
| 6 | shrink | mutex and shared total in `list` | per-index `[]int64`, sum after `Wait` | -4 |
| 7 | delete | `EvalSymlinks` in `exePath` | `os.Executable()` alone | -3 |
| 8 | shrink | `orNone` single-use helper | inline | -3 |
| 9 | shrink | redundant OK/Warn assignments in `interpreters()` | `OK: true` literal | -2 |
| 10 | shrink | Group Policy detection by searching the fix text for "Policy" | a `gpo` bool | 0 |
| 11 | stdlib | `shell.hookDir()` duplicates `envmgr.Dir()` | import envmgr | -1 |
| 12 | native | lipgloss (13 indirect modules) for 5 styles | ANSI codes + `SetConsoleMode` VT flag + `NO_COLOR` check. Your spec asked for lipgloss, so this one is your call. | +10, -1 dep |

Net: about -54 lines, and -1 dependency if lipgloss goes.

---

## 11. The rename from WinVenv/wv to pyGo/pygo

| Thing | Before | After |
|---|---|---|
| Product name | WinVenv | pyGo |
| Command | `wv` | `pygo` |
| Go module | `winvenv` | `pygo` |
| Entry folder | `cmd/wv` | `cmd/pygo` |
| State folder | `%LOCALAPPDATA%\winvenv` | `%LOCALAPPDATA%\pygo` |
| Hook files | `wv_hook.cmd`, `wv_env.cmd` | `pygo_hook.cmd`, `pygo_env.cmd` |
| Env vars | `_WV_OLD_PATH`, `_WV_OLD_PROMPT`, `_WV_OLD_PS1` | `_PYGO_OLD_PATH`, `_PYGO_OLD_PROMPT`, `_PYGO_OLD_PS1` |
| Delete trash suffix | `.wv-trash` | `.pygo-trash` |
| Profile comment | `# WinVenv shell hook` | `# pyGo shell hook` |

Migration so far:

- `registry.json` copied to the new folder, so your scanned venvs are preserved
- `pygo.exe` installed to `%LOCALAPPDATA%\pygo\bin`
- user PATH entry swapped from `winvenv\bin` to `pygo\bin`
- two deleted test venvs pruned
- 12 venvs from other Claude temp folders unregistered, with their folders left alone

**Still to do (by you).** Your PowerShell profile, CMD AutoRun and `.bashrc` still call the old `wv.exe`. I checked that they contain only the old pyGo lines. The PowerShell profile has nothing else in it. Paste this into your own PowerShell window:

```powershell
$prof = "$env:USERPROFILE\Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1"
Set-Content $prof (Get-Content $prof | Where-Object { $_ -notmatch 'winvenv|WinVenv shell hook' })
$rc = "$env:USERPROFILE\.bashrc"
$t = ([IO.File]::ReadAllText($rc) -split "`n" | Where-Object { $_ -notmatch 'winvenv|WinVenv shell hook' }) -join "`n"
[IO.File]::WriteAllText($rc, $t, [Text.UTF8Encoding]::new($false))
Remove-ItemProperty "HKCU:\Software\Microsoft\Command Processor" -Name AutoRun
& "$env:LOCALAPPDATA\pygo\bin\pygo.exe" init
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\winvenv"
```

Then open a new terminal and run `pygo doctor`.

The `.bashrc` rewrite keeps Unix line endings and writes no BOM, so Git Bash won't print `$'\r': command not found`.

---

## 12. Known issues

- **Doctor's hook check gives false positives.** It looks for the text `init pwsh` / `init bash` in your profiles, which the old `wv` lines also contain. Right now it says PowerShell and Bash are hooked even though they point at `wv.exe`. The fix is to also require `pygo` in the matched line. Until then, trust it only after the migration above.
- **Repair's byte replace can over-match.** If the old path is `C:\x\proj` and a file also mentions `C:\x\proj2`, the second gets rewritten too. This is rare inside `Scripts\`.
- **Orphaned child processes.** Killing a venv `python.exe` launcher can leave its base-interpreter child running. Restart Manager lists that child too, but only if it maps files from the venv.
- **`scan` walks the central folder twice** if you also pass it as a root. Harmless.
- **Scanning a temp folder registers other tools' throwaway venvs.** Point `scan` at real project roots, or set `scan_roots` in `config.json`.
- **Duplicate names.** Several of your venvs share a name (`gravity-render` three times, `agent-gateway`, `Cli`, `gateway`, `overreach`, and others). `pygo on <name>` errors for those, and you need to pass the path.
- **Odd registry entries.** `P.A.R.A.K.H` has `home = /usr/bin`, so it was created from WSL and will show `broken-base`. `virt` points at a Store-installed Python 3.12.

---

## 13. What was tested, and how

| Area | Test | Result |
|---|---|---|
| Build | `gofmt`, `go vet`, `go test`, `go build` | clean, test passes |
| Unit | launcher rewrite for pip shebang, uv `UVSC` trailer, forward-slash form | pass |
| create | central env via uv, `-p 3.12` | works |
| list / scan | registry, sizes, prune of deleted envs | works |
| repair | moved a uv venv and a pip venv, then ran `pyflakes.exe` and `pip.exe` | both work after repair; dry-run lists 6 files |
| nuke | live `python.exe` holding the venv: non-interactive refuses and leaves the venv intact; `--force` kills, deletes, no trash left | works, parent shown as `powershell.exe` |
| PowerShell activation | fresh `powershell -ExecutionPolicy Restricted`, wrapper from `init`: on, python path inside venv, re-on, off, PATH restored, cd | works |
| Non-ASCII | venv under `تجربة_é`, activated in PS 5.1 | `VIRTUAL_ENV` matches, python resolves |
| CMD activation | the exact macro expansion run in a `.cmd`: on, `where python`, off, pass-through `ls` | works |
| Installed hooks | fresh PowerShell loading your real profile (under the old `wv` name) | works |
| Redirect file | `proj\.venv` containing `..\uvv_moved` | `pygo on` resolves the target |
| doctor | on your machine | output in section 8 |

### Not tested

- `pygo init` run by me. It was blocked, and you ran it under the old name.
- Git Bash activation in a real Git Bash window. The script is generated but has never been executed.
- A real interactive CMD window with the doskey macro.
- The `[y/N]` prompt. My shell has no console attached.
- The editor-protection refusal, since I never had VS Code actually locking a venv.
- Anything under the new `pygo` hooks, which wait on the migration in section 11.

---

## 14. Decisions in brief

The full reasoning is in `floated.md`. Short version:

1. A wrapper function plus text eval, rather than a subshell. It's the same pattern as fnm, zoxide and starship.
2. Never run `Activate.ps1`. Evaluating text is policy-immune.
3. CMD uses doskey plus a per-call `.cmd` file, because CMD has no eval.
4. Save the original PATH once and restore it exactly.
5. The registry is a JSON file. Paths compare case-insensitively.
6. Envs are central by default, with `--local` for `.\.venv`. Local envs are named after their project.
7. Creation delegates to uv, with a `python -m venv` fallback using PEP 514.
8. The scanner runs one goroutine per folder, with 64 directory reads at a time.
9. Lock detection uses Restart Manager, not undocumented handle enumeration. It needs no admin.
10. Repair rewrites paths in place and understands uv trampolines.
11. Doctor is read-only and only advises.
12. Output uses lipgloss marks and tabwriter tables, with no TUI framework yet.
13. Go is installed user-local.
14. Fixes from the first smoke test (section 9.1).
15. Changes from the research brief (section 9.2), plus what was deliberately not built.
16. The rename to pyGo.

---

## 15. Not built yet (and why)

| Item | Why not yet |
|---|---|
| Auto-activate on `cd`, with a `pygo allow` trust step | Biggest feature left. It needs a prompt hook that only spawns `pygo.exe` when the project root changes. The obvious next build. |
| `pygo prune` (bulk delete by age or size) | `list` already shows the data it would use |
| Writing PEP 832 redirect files / `--junction` | PEP still a draft; junctions break OneDrive sync |
| Windows Terminal fragment, cached init text | only matter on Group Policy-locked or antivirus-slowed machines |
| Code signing, version resources, winget/scoop, arm64 build | do before sharing the binary with anyone |
| Warning when `create --local` targets a cloud folder | doctor catches it after the fact |
| Git repository | the project isn't under version control yet |

---

## 16. Everyday cheat sheet

```powershell
pygo scan                     # find and register venvs (all fixed drives, or config scan_roots)
pygo scan D:\PROJECTS         # just one tree
pygo list                     # everything, with sizes and last-used
pygo create api -p 3.12       # central env
pygo create --local           # .\.venv in this folder
pygo on                       # nearest .venv upward
pygo on api                   # by name
pygo on D:\PROJECTS\x\.venv   # by path (use for duplicate names)
pygo off
pygo cd api                   # jump to project folder
pygo nuke old-thing           # asks before killing lockers
pygo nuke old-thing --force   # kills without asking (never editors/shells)
pygo repair --dry-run         # after a folder move: preview
pygo repair                   # then apply
pygo doctor                   # machine diagnosis
```

### Building from source

```powershell
cd D:\PROJECTS\pygo
go vet ./...
go test ./...
go build -o pygo.exe ./cmd/pygo
Copy-Item pygo.exe "$env:LOCALAPPDATA\pygo\bin\pygo.exe" -Force
```

Don't strip the build with `-ldflags "-s -w"`, because of the Defender false-positive risk. Don't move `pygo.exe` after `pygo init` either, because the hooks record its path. If you do move it, run `pygo init` again.

### Optional machine fix

From an admin terminal:

```
reg add HKLM\SYSTEM\CurrentControlSet\Control\FileSystem /v LongPathsEnabled /t REG_DWORD /d 1 /f
```
