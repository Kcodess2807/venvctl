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
- **Migration on first run, not at install time.** It is triggered by a missing `venvctl
egistry.json` (not a missing folder, so an already-copied `bin` does not block it). Central envs are renamed, not copied, and then repaired, because their launchers embed absolute paths. The old folder is never deleted.
- **`VENVCTL_HOME`** overrides the state root. Legacy folders are then its siblings, which keeps tests off the real `%LOCALAPPDATA%`.
- **Found while testing the compat code:** PowerShell's `Get-Item`/`Remove-Item` silently ignore a `function:global:` prefix, so the prompt was never restored by `off` (this was true in pyGo and WinVenv too). Prompt functions are now read and written as `${function:global:name}` and removed by unqualified path. Verified by an on/off round trip whose prompt matches before and after.

## 18. Hook checks compare the exe each hook calls (Phase 0)
Matching the text `init pwsh` counted old `wv` lines as installed. Each hook line's quoted exe is now extracted and compared with the running `venvctl.exe`, so a hook that calls another exe is reported as "stale". Profile paths come from the Documents known folder instead of spawning PowerShell. That's faster, follows OneDrive redirection, and removed a duplicate lookup (audit item 5).

## 19. One error and exit-code contract (Phase 1)
- **`Fail(code, msg, fix)` everywhere.** It prints `✗ msg` then `  → fix`, or JSON on stderr in `--json` mode. Cobra's own errors are classified into exit 2.
- **Exit 4 for "nothing to do".** An empty `list`, `prune` with no candidates, `repair` with nothing to fix, an existing `link`, a repeated `allow`/`deny`. Scripts can branch on it, and the message prints as a note, not an error.
- **Color only on a real console.** It's off for `--no-color`, `NO_COLOR`, `--json` and output that isn't a console. The console is switched to VT mode, and if that fails there's no color.
- **Padding-aware table helper instead of tabwriter.** tabwriter counted ANSI codes as width, which misaligned styled headers.
- **`Run(args, out, err, in) int`** so tests drive the real command tree with captured output. No mocks.

## 20. Estimated last activity, so age filters are safe (Phases 1-2)
`last_used` only exists for venvs activated through venvctl, and all 77 of yours were "never". If `--older-than` used only that, it would select everything, which is dangerous for `prune`. `last_activity` takes the newest of `last_used` and the modification times of the venv folder, `pyvenv.cfg` and `site-packages`, which changes on every install. It's shown with a `~` in `list`. Prune also needs at least one criterion and never selects the active venv.

## 21. Ambiguous names: ask in a console, print the commands otherwise (Phase 0)
Several of your venvs share a name. A console gets a numbered picker. Anything else (scripts, `--json`, mintty) gets exit 2 and the exact `venvctl <verb> "<path>"` lines, so it's never a dead end and never a guess.

## 22. Redirect files are written by default for central envs (Phase 3)
This reverses entry 15's "read-only" stance, on your instruction. The safety rules keep it reversible:
- conflicts are checked *before* the env is created
- a `.venv` folder is never replaced
- a redirect pointing elsewhere is replaced only with consent or `--force`
- `--no-redirect` skips it, and `unlink` undoes it

Doctor's IDE check accepts either a project redirect or a VS Code-family settings entry. It parses `settings.json` as JSON with comments and trailing commas, and never edits it.

## 23. Auto-activate: trust plus cheap hooks (Phase 4)
- **Trust is explicit** (`allow`), because putting a folder's `Scripts` on PATH runs its code on the next `python`.
- **`trust.json` keys are lower case with forward slashes**, so the hooks check membership with a substring search for `"<key>"`. That needs no JSON parsing in PowerShell, bash or Lua, and `SetEscapeHTML(false)` keeps `&` literal.
- **Each shell does the cheap part itself.** PowerShell uses .NET calls only. Bash uses builtins only, plus one `pwd -W` on a root change, because MSYS paths like `/tmp` can't be converted by hand. Lua uses file checks. `venvctl.exe` is spawned only on a trusted root change, or to leave an auto-activated venv. Measured cost with the folder unchanged: 0.43 ms at p99.
- **`_VENVCTL_AUTO=1` marks what auto-activate owns.** Anything activated without it (you, VS Code) is never deactivated or replaced. A manual `on` clears the mark.
- **Attachment.** Prompt wrapping by default. oh-my-posh's `Set-PoshContext` and starship's `Invoke-Starship-PreCommand` when they own the prompt. The `cd` wrapper (zoxide-safe) is an explicit opt-in (`VENVCTL_AUTO_HOOK=cd`), because overriding `cd` is the most invasive option.
- **CMD only through Clink.** Plain CMD has no per-prompt hook. The Clink script is written only while auto-activate is on, and removed otherwise.
- **The hook is emitted only when enabled.** `init pwsh`/`bash` print it only when config enables it, so turning it off takes effect in new shells without editing profiles.

## 24. Release builds stay unstripped and reproducible (Phase 6)
`-trimpath`, version from `git describe`, `-X` for the version and date, and never `-s -w` (Defender heuristics). Resources come from `go-winres`, including `longPathAware` in the manifest. Signing is wired but gated on `VENVCTL_SIGN_CERT`; without it, the script prints the exact commands. `.gitattributes` forces LF, because Windows runners check out CRLF and gofmt rejects it.

## 25. Testing without touching your machine
Every behaviour test runs against `VENVCTL_HOME` or `LOCALAPPDATA` set to a temp folder, with fake or real temp venvs. Running the real exe without it would trigger the first-run migration on your real `%LOCALAPPDATA%`; the safety classifier blocked that twice, correctly. Commands that edit your profile, AutoRun or `.bashrc` (`venvctl init`) are only run as `--dry-run`. `testdata\autocd.ps1` drives a real PowerShell prompt in its own session, and never loads or writes `$PROFILE`.

## Open items
- Run `venvctl init` yourself; your hooks still date from WinVenv.
- The first `venvctl` run migrates `%LOCALAPPDATA%\pygo`; delete it, and `\winvenv`, after checking `venvctl list`.
- Clink, oh-my-posh and starship attachment, and interactive prompts are untested here.
- No release workflow, signing certificate, real icon or README text yet.
- One `project` per central env.
- Killing a venv `python.exe` launcher can orphan its base-interpreter child; Restart Manager lists the child only if it maps venv `.pyd` files.
