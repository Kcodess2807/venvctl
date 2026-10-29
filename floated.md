# floated.md: decision log (personal reference)

Each entry: what I chose, why, and where it connects. Newest decisions go at the bottom.

## 1. Wrapper function plus `__shell`, not a child-process trick
A child process cannot edit its parent's environment. So `pygo init` installs a tiny shell function. For on/off/cd it calls `pygo.exe __shell <shell> ...`, which prints a script, and the function evals it. Everything else passes straight through. Connects: `shell.Init` (wrapper text) <-> `cli.shellCmd` (hidden command) <-> `shell.Activate/Deactivate/Cd` (script text). The set of wrapped verbs lives in one place, `shell.Handled`.

## 2. Eval a string, never run `Activate.ps1`
Running a `.ps1` triggers ExecutionPolicy. `Invoke-Expression` on text does not. This is the whole fix for `PSSecurityException`. Side effect: `pygo doctor` still warns when policy is Restricted, because `$PROFILE` itself will not load then.

## 3. CMD uses doskey plus a temp batch file
CMD has no eval. The macro is `pygo.exe __shell cmd $* & call pygo_env.cmd`. `pygo_env.cmd` is rewritten on every call, even when empty or failing, so a stale script never re-runs. Installed through `HKCU\...\Command Processor\AutoRun`, appended with ` & ` to keep any existing AutoRun. Caveat: doskey macros only work in interactive CMD, not inside `.bat` files.

## 4. Original PATH saved in `_PYGO_OLD_PATH`
Deactivate restores it exactly instead of trying to strip our entry. Activating twice does not stack, because the saved value is only set when empty.

## 5. Registry is a JSON file, written by scan, create, and on activate
`registry.json` is the source of truth for `list`, name lookup, and `last_used`. `scan` both adds and prunes entries whose `pyvenv.cfg` is gone. Paths compare case-insensitively (`SamePath`) because Windows paths are.

## 6. Local vs central storage
`pygo create --local` makes `.\.venv`. Default is central at `%LOCALAPPDATA%\pygo\envs\<name>`. Local envs take the project folder name, not ".venv", so `pygo list` is readable. `scan` always includes the central dir because the walk skips `AppData`.

## 7. Create delegates to uv, falls back to `python -m venv`
uv is faster and manages Python downloads. The fallback uses the PEP 514 registry finder, so `--python 3.12` works without uv if that install is registered.

## 8. Scanner: goroutine per directory, 64 concurrent `ReadDir`s
Simple and fast on SSDs. Stops descending when it finds a `pyvenv.cfg`. Skips symlinks (avoids loops), `AppData`, `node_modules`, `.git`, Windows and Program Files. Known ceiling: goroutine count grows with directory count (`ponytail:` comment in code).

## 9. Restart Manager over handle enumeration
`rstrtmgr.dll` gives PIDs and names for files held open, without admin and without undocumented NT calls. Only `.exe/.dll/.pyd` are registered, since those are what lock deletes in practice. `nuke` refuses by default and lists lockers; `--force` terminates them. Delete then clears read-only bits and retries five times because handles release slightly after a kill.

## 10. Repair rewrites absolute paths, handling uv trampolines
Old location is read from `VIRTUAL_ENV` in `activate.bat`. Every file in `Scripts\` gets a byte replace of the old path (both slash forms). uv launcher exes store the path with a length field before a `UVUV`/`UVPY` marker, so they get the length fixed up. This is the one piece with a test (`envmgr_test.go`). Not covered: `pyvenv.cfg` `home` (points at the base Python, which a venv move does not change).

## 11. Doctor is read-only
It reports and prints a fix; it never edits system settings. Exit code is non-zero only for hard failures, not warnings.

## 12. Output: lipgloss marks, tabwriter tables, no TUI
Colored status marks and plain aligned tables. I did not add bubbletea or huh: nothing is interactive yet. Add them if `nuke` gets a multi-select prune picker.

## 13. Toolchain
Go was not installed. I installed the official zip into `%LOCALAPPDATA%\Programs\Go` (user-local, no admin). You still need it on PATH for your own builds.

## 14. Fixes found by running it
- `repair` could not read uv-made venvs: uv's `activate.bat` has the path in a `for %%i in ("...")` line, not `set VIRTUAL_ENV=`. The pattern now accepts both, with an optional `@`.
- `doctor` showed warnings as failures (check order in `cli.doctorCmd`). Warn now wins over fail, and missing hooks is only a warning so a fresh install exits 0.
- A registry Python key with no version printed as `()`. It now falls back to company/tag.
- `go mod tidy` raised `go.mod` to `go 1.26.0` because the latest dependencies require it. The brief said 1.22+, so this is compatible, just newer.

## Verified by running
Build, vet, unit test, `doctor`, `create` (uv), `list`, `scan`, `repair` after a folder move (python and a pip launcher still work), `nuke` refusing then succeeding with `--force` against a live python process (Restart Manager found the PID), PowerShell on/off/cd under a Restricted execution policy, CMD on/off via the doskey macro expansion.

## Not verified
- `pygo init` itself (profile and AutoRun edits). I did not run it, to avoid editing your shell config. Run it yourself, then restart the shell.
- Real doskey in an interactive CMD window (only the expansion was tested).
- Git Bash wrapper (script generated, not executed).

## 15. Changes from the 29 Oct research brief
The brief was checked against this machine first, then only items that fix or harden existing commands were built.

- **Repair corrupted uv launchers (real bug, reproduced).** Current uv ends console-script launchers with `UVSC`; I only knew `UVUV`/`UVPY`. Repair fell through to a plain byte replace, the length field went stale, and the launcher died with "Failed to canonicalize script path". Now `UVSC`, `UVPY`, `UVUV` all get the trailer rewrite. Test updated to `UVSC`. Correction to entry 14: the earlier "pip launcher still works" claim was not actually tested then; it is now (pip launchers were always fine).
- **`repair --dry-run`.** Lists files it would rewrite. Rewriting `.exe`s looks like malware to AV, so preview first. Not default, to keep the command simple.
- **Nuke: attribute, ask, never kill editors.** Lock table shows the parent process (Toolhelp snapshot). Without `--force` it asks `[y/N]` in a console, errors when not interactive. Editors and shells (Code, Cursor, PyCharm, devenv, Explorer, Terminal, pwsh, cmd, bash) are never killed, the user closes them. Registry is saved even when a later arg fails.
- **Restart Manager race.** `RmGetList` retries on ERROR_MORE_DATA when the list grows between calls.
- **Rename-first delete.** `.venv` is renamed to `<name>.<nanos>.pygo-trash` before deleting. If rename fails, the venv is untouched ("still locked, nothing deleted") instead of half-deleted (the uv #11092 corruption). Backoff 50/100/200/400/800 ms covers AV handles. Leftover trash is reported by path.
- **PEP 832 redirect files, read-only.** `FromDir` follows a `.venv` that is a file: one line, relative to its folder, BOM tolerated, content treated as path data only. So `pygo on` in such a project works. I do not *write* redirect files yet: the PEP is a draft, and tools that expect `.venv` to be a directory (uv sync today, unverified) could break on a file.
- **PowerShell 5.1 encoding.** Wrapper sets `[Console]::OutputEncoding` to UTF-8 around the `pygo __shell` call and restores it. Verified with a venv under an Arabic+accented folder name.
- **Prompt ownership.** If oh-my-posh (`POSH_THEME`/`POSH_SHELL`) or starship (`STARSHIP_SHELL`) is active, pygo does not wrap the prompt. All shells now set `VIRTUAL_ENV_PROMPT`, which those engines read. You have oh-my-posh installed, so this matters if you enable it.
- **Doctor: policy scope.** Reads `Get-ExecutionPolicy -List`, takes the first non-Undefined scope (ignoring the probe's own Process scope), names it, and gives a Group Policy specific fix (`powershell -NoExit -Command "pygo init pwsh | Out-String | iex"`) when MachinePolicy/UserPolicy blocks. Side effect: it now shows your real `RemoteSigned (CurrentUser)` instead of the probe's `Bypass`.
- **Doctor: venvs in cloud folders.** Warns for registered venvs under OneDrive (env vars), Dropbox, Google Drive, iCloudDrive. Checked that Go's `ReadDir` treats OneDrive placeholder folders as normal directories, so `scan` still walks them.
- **Unstripped build installed.** The brief cites Defender flagging stripped (`-s -w`) Go builds. The installed binary is now built without `-ldflags "-s -w"` (7.1 MB vs 4.9 MB).

### Deliberately not built from the brief
- **Auto-activate on `cd` with `pygo allow` trust.** Biggest item; needs the prompt-hook design from brief §2.4. Build it next if activation should be the on-ramp.
- **Writing redirect files / `--junction`.** See above; junctions are unsafe in OneDrive anyway.
- **Windows Terminal fragment, cached init text.** Only matter on GPO-locked or AV-slow machines; yours is neither.
- **CMD AutoRun guard variable.** The brief's recursion risk comes from `FOR /F` in AutoRun. Our hook is one `doskey` line, no subprocess, so no recursion. A guard env var would be inherited by child cmd windows, which don't inherit doskey macros, so it would break `pygo` in them.
- **Code signing, version resources, winget/scoop, arm64.** Distribution work, do before sharing the binary.
- **`create` warning inside cloud folders.** Doctor covers it after the fact.

## 16. Renamed to pyGo
You asked for the name pyGo. Command is `pygo` (lowercase, easiest to type; Windows does not care about case), product name pyGo in help text. Renamed together so nothing half-matches: Go module `pygo`, `cmd/pygo`, state dir `%LOCALAPPDATA%\pygo`, hook files `pygo_hook.cmd`/`pygo_env.cmd`, env vars `_PYGO_OLD_PATH`/`_PYGO_OLD_PROMPT`/`_PYGO_OLD_PS1`, trash suffix `.pygo-trash`, shell function `pygo`.
Migration: copied `registry.json` (your scanned venvs) to the new folder, installed `pygo.exe` in `%LOCALAPPDATA%\pygoin`, swapped the user PATH entry. Old `%LOCALAPPDATA%\winvenv` stays until the old `wv` hook lines are removed from the profile, AutoRun and `.bashrc`, because those still call `wv.exe`. No migration code in the binary: it was a one-time move for one machine.
Side note: a `pygo scan` of the temp folder registered 12 venvs from other Claude temp sessions; they were unregistered (folders left alone).

## 17. Renamed to venvctl
Renamed to venvctl. Reason: `pygo` collides with an existing ReiterAdam/pygo venv CLI on GitHub, the PyGo Framework CLI, pygo-mcp, and pygo-tools on PyPI. `*ctl` signals a tool that manages things it didn't create, matching the uv-creates / venvctl-manages positioning.
Entries 1-16 above keep the names that were current when they were written (`pygo`, `_PYGO_OLD_PATH`, `%LOCALAPPDATA%\pygo`, ...); today each is the `venvctl` equivalent (`venvctl-complete.md` section 11b has the full table).
- **Compatibility, kept small on purpose.** `off`/`on` adopt and clear the `_PYGO_OLD_*` variables of shells activated before the rename. Old `.pygo-trash`/`.wv-trash` folders count as trash. There is no `pygo` alias.
- **`venvctl init` cleans up both older hook generations itself** (WinVenv and pyGo), which replaces the manual paste-in snippet from entry 16. It filters line-by-line and segment-by-segment in pure functions, so the rules are unit-tested on strings. It keeps profile encoding and line endings, writes `.bashrc` LF without a BOM, and keeps VS Code's AutoRun segment in order. It has `--dry-run` and `--remove`.
- **Migration on first run, not at install time.** It is triggered by a missing `venvctlegistry.json` (not a missing folder, so an already-copied `bin` does not block it). Central envs are renamed, not copied, and then repaired, because their launchers embed absolute paths. The old folder is never deleted.
- **`VENVCTL_HOME`** overrides the state root. Legacy folders are then its siblings, which keeps tests off the real `%LOCALAPPDATA%`.
- **Found while testing the compat code:** PowerShell's `Get-Item`/`Remove-Item` silently ignore a `function:global:` prefix, so the prompt was never restored by `off` (this was true in pyGo and WinVenv too). Prompt functions are now read and written as `${function:global:name}` and removed by unqualified path. Verified by an on/off round trip whose prompt matches before and after.

## Open items
- No `pygo prune` (bulk delete by age or size) yet; `list` shows the data it would need.
- `config.json` is read but never written by a command.
- `scan` passes the central dir twice if you also give it as a root. Harmless, just a duplicate walk.
- Repair's byte replace would also hit a longer path that starts with the old one (old `C:\x\proj`, file mentions `C:\x\proj2`). Rare inside a venv's `Scripts\`.
- Killing a venv `python.exe` launcher can orphan its base-interpreter child. Restart Manager lists that child too only if it maps venv `.pyd`s.
- Go lives in `%LOCALAPPDATA%\Programs\Go\bin` (now on your user PATH).
