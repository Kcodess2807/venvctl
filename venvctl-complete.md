# venvctl: complete project record

Everything built, decided, tested, fixed and left open while building venvctl, as of 29 Oct 2026.
It was first called WinVenv (`wv`), then pyGo (`pygo`), and is now venvctl. Sections 11 and 11b cover the two renames.
For shorter views, see `design.md` (architecture), `floated.md` (decision log), `docs/json.md` (JSON schemas) and `redirect.md` (`.venv` redirect files).

---

## 1. What venvctl is

venvctl is a single-binary, Windows-native manager for Python virtual environments, written in Go. It does not replace uv or pip; uv creates, venvctl manages. It activates, finds, links, deletes, repairs and diagnoses environments.

### The problems it solves

| # | Problem on Windows | venvctl answer |
|---|---|---|
| 1 | Activation differs per shell, and PowerShell often refuses `Activate.ps1` with `PSSecurityException` | `venvctl on` / `off` work the same in PowerShell, CMD and Git Bash, and never run a `.ps1` |
| 2 | A compiled program cannot change its parent shell's environment, which is why uv has no `uv activate` | `venvctl init` installs a small shell function that evaluates the text venvctl prints |
| 3 | Deleting a `.venv` fails with `Access is denied (os error 5)` because processes hold files open | `venvctl nuke` names the processes (and who started them), stops them with your OK, and never half-deletes |
| 4 | Dozens of forgotten venvs eat 20 GB+, with no list, no jump-to, and breakage after a rename | `scan`, `list`, `prune`, `cd`, `repair` |
| 5 | Central venvs are invisible to editors | `.venv` redirect files (PEP 832, draft) via `create`, `link`, `unlink` |
| 6 | Typing `on` in every new terminal | Opt-in auto-activate on `cd`, for project roots you trust |

Plus `venvctl doctor`, which diagnoses machine-level causes: Store stubs, ExecutionPolicy scope, long paths, VC++ runtime, hooks, cloud-synced venvs and IDE visibility.

### Tech stack

| Piece | Choice |
|---|---|
| Language | Go, `go.mod` `go 1.26.0`, built with Go 1.27.3 |
| CLI | `github.com/spf13/cobra` v1.10.2 (grouped help, completion) |
| Styling | `github.com/charmbracelet/lipgloss` v1.1.0, plus a small padding-aware table helper |
| Windows APIs | `golang.org/x/sys/windows` and `/registry` v0.49.0 |
| Venv creation | `uv venv` if uv is on PATH, else `python -m venv` |
| Resources | `go-winres` (version info, icon, `longPathAware` manifest) at build time |

---

## 2. Timeline

1. **Brief received.** The tool was named WinVenv, with the binary `wv`.
2. **Go installed.** Go 1.27.3 went into `%LOCALAPPDATA%\Programs\Go`, which needs no admin, and onto the user PATH.
3. **First version.** All the code and one unit test were written, and three bugs from the first smoke test were fixed.
4. **Tested.** Lock detection was checked against a live `python.exe`. PowerShell activation was checked under `Restricted`, and CMD through the macro expansion.
5. **Installed.** You ran `wv init` yourself, after Claude Code's safety classifier blocked it for me.
6. **Research brief.** It turned up the uv `UVSC` repair bug and led to eight hardening changes.
7. **Ponytail audit.** It found 12 simplifications.
8. **Renamed to pyGo.** The hook migration snippet was given to you but never run.
9. **Phase 0 fixes.** Stale-hook detection, boundary-aware repair, the duplicate-name picker and scan root dedupe.
10. **Phase 1, CLI pass.** `--json`, `--quiet`, `--no-color`, exit codes, `Fail()`, the overview, and the richer `list`, `scan`, `nuke` and `create` flags. Also grouped help, `version` and `completion`.
11. **Phase 2.** `prune`.
12. **Renamed to venvctl.** This added legacy hook cleanup in `init`, a first-run migration, `VENVCTL_HOME` and compatibility for `_PYGO_OLD_*`. It also fixed the PowerShell prompt restore. The local install was updated.
13. **Phase 3.** PEP 832 redirect files: `create` links, and the `link` and `unlink` commands. Doctor gained "IDE discovery".
14. **Phase 4.** Auto-activate on `cd`, with `allow`, `deny` and `config`, and hooks for PowerShell, Git Bash and Clink. Includes `bench-hook` and `testdata\autocd.ps1`.
15. **Phase 5.** Coexistence with VS Code.
16. **Phase 6.** Release engineering: `winres`, `build.ps1`, winget and Scoop drafts, CI, and forced LF line endings.
17. **Phase 7.** The rest of the audit applied.
18. **Docs.** `docs/json.md` added, and this file refreshed.

The change history is in section 18.

---

## 3. Project layout

```
D:\PROJECTS\venvctl\                    (was D:\PROJECTS\pygo, renamed at the end of this run)
├── cmd\venvctl\main.go                  11   os.Exit(cli.Run(args, stdout, stderr, stdin))
├── internal\
│   ├── cli\cli.go                      540   root, global flags, exit codes, Fail, output, migration, version, completion, overview, doctor
│   ├── cli\envs.go                    1273   resolve + prompts, create, link, unlink, list, scan, nuke, prune, repair, parsing
│   ├── cli\shellcmds.go                465   on/off/cd stubs, init, allow, deny, config, bench-hook, __shell (on/off/cd/auto)
│   ├── cli\cli_test.go                 380   flags and exit codes (table), list filters, link/unlink, auto transitions, prune
│   ├── discovery\discovery.go          256   concurrent scan (+ redirect files, trash skip), PEP 514, versions, drives
│   ├── doctor\doctor.go                434   the checks (section 9)
│   ├── doctor\doctor_test.go            30   VS Code settings coverage, JSONC stripping
│   ├── envmgr\envmgr.go                496   registry, config, resolve/merge, create, delete (rename-first), repair, cloud roots, activity
│   ├── envmgr\redirect.go               47   .venv inspect / write / remove
│   ├── envmgr\trust.go                 126   trust.json, FindRoot (auto-activate root rule)
│   ├── envmgr\migrate.go               138   pyGo state migration, legacy dirs, trash sweep
│   ├── envmgr\*_test.go                239   repair rewrite + boundary, migration end to end, FindRoot, trust
│   ├── shell\shell.go                  338   hook code per shell, activate/deactivate scripts, legacy vars, status
│   ├── shell\install.go                434   init: profile/AutoRun filtering, encodings, Clink script, report
│   ├── shell\auto.go                   268   auto-activate hooks (PowerShell, bash, Clink)
│   ├── shell\install_test.go           156   filters, VS Code blocks, encodings, template rendering
│   └── winapi\winapi.go                209   Restart Manager, process table, module snapshot, kill, Store alias
├── testdata\autocd.ps1                  72   manual end-to-end auto-activate test
├── winres\                                   winres.json, icon.png, icon16.png (placeholders)
├── packaging\winget\manifest.yaml            draft
├── packaging\scoop\venvctl.json              draft
├── .github\workflows\ci.yml                  vet, test, build both arches on windows-latest
├── build.ps1                                 release build, checksums, --sign
├── docs\json.md, redirect.md, design.md, floated.md, venvctl-complete.md, README.md (placeholder)
└── .gitattributes (LF), .gitignore
```

That's about 6,000 lines of Go, about a third of them tests.

```
cmd/venvctl ─> cli ─┬─> doctor ─┬─> discovery
                    │           ├─> envmgr ─> discovery
                    │           ├─> shell ──> envmgr
                    │           └─> winapi
                    ├─> envmgr, shell, winapi, discovery
```

There are no interfaces or abstraction layers, just plain functions.

---

## 4. Files venvctl keeps on disk

The state folder is `%LOCALAPPDATA%\venvctl\`, or `VENVCTL_HOME` if that is set.

| Path | Purpose |
|---|---|
| `bin\venvctl.exe` | The installed binary. This folder is on your user PATH. |
| `registry.json` | Every known venv: `name`, `path`, `python`, `version`, `central`, `last_used`, `size` (cached by `list`/`prune`), `project` (linked folder). |
| `config.json` | `scan_roots`, `default_python`, `auto_activate`, and after migration `migrated_from`/`migrated_at`. `config auto-activate` writes it; writes keep unknown keys. |
| `trust.json` | `{"trusted": [...]}`: roots allowed to auto-activate, stored lower case with forward slashes. |
| `envs\<name>\` | Central environments |
| `venvctl_hook.cmd` | The CMD hook: one doskey line, called from AutoRun |
| `venvctl_env.cmd` | Rewritten on every CMD `venvctl` call; the macro `call`s it |

Elsewhere:

| Where | What | Written by |
|---|---|---|
| each PowerShell `$PROFILE` (5.1 and 7) | `# venvctl shell hook` + `& '<exe>' init pwsh \| Out-String \| Invoke-Expression` | `venvctl init` |
| `HKCU\Software\Microsoft\Command Processor\AutoRun` | `if exist "<state>\venvctl_hook.cmd" call "..."`, joined with ` & ` to other segments | `venvctl init` / `init cmd` |
| `%USERPROFILE%\.bashrc` | `# venvctl shell hook` + `eval "$('<posix exe>' init bash)"` | `venvctl init` |
| `%LOCALAPPDATA%\clink\venvctl.lua` | Clink auto-activate script | `venvctl init cmd`, only when auto-activate is on and Clink is installed |
| `<project>\.venv` (a file) | PEP 832 redirect: one line, absolute venv path, UTF-8, no BOM, LF | `create` (central), `link`; removed by `unlink` |
| `<venv>.<n>.venvctl-trash` | Transient during delete; left only if a delete is interrupted, and swept by the next nuke | `nuke`, `prune` |

---

## 5. Command reference

### Global flags and exit codes

| Flag | Effect |
|---|---|
| `--json` | One JSON document on stdout, errors as JSON on stderr, no styling, no prompts (docs/json.md) |
| `-q`, `--quiet` | Errors only |
| `--no-color` | No colors; also implied by `NO_COLOR`, by output that isn't a console, and by `--json` |

| Exit | Meaning |
|---|---|
| 0 | success (doctor warnings included) |
| 1 | failure |
| 2 | usage error: unknown command, bad flag or argument |
| 3 | refused: venvctl declined to act (editor holds the venv, you answered no, would overwrite) |
| 4 | nothing to do: no matching venvs, or nothing needed changing |

Every error prints `✗ <what happened>` and then `  → <what to do next>`.

### Commands (grouped as in `venvctl --help`)

| Group | Command | Flags | What it does |
|---|---|---|---|
| | `venvctl` | | Overview: active venv, nearest `.venv`, registry count and size, hook state per shell, and the 5 most common commands |
| Use | `on [name\|path]` (`activate`) | | Activate: the nearest venv upward (following redirects), a name, or a path. Needs the hook. |
| Use | `off` (`deactivate`) | | Restore PATH and prompt. Also cleans up shells activated in the pyGo era. |
| Use | `cd <name\|path>` | | Go to the project folder; for a linked central env that's the linked project |
| Use | `create [name]` | `-p/--python 3.12\|3.12.3\|path`, `-l/--local`, `-v/--verbose`, `-f/--force`, `--no-redirect` | A central env, linked to the current folder by a `.venv` redirect; or `.\.venv` with `--local`, refused in cloud folders without `--force` |
| Use | `link <name\|path>` | `-f/--force` | Write or update this folder's `.venv` redirect |
| Use | `unlink` | | Remove this folder's `.venv` redirect (never a `.venv` folder) |
| Find | `list` (`ls`) | `--sort name\|size\|last-used\|version`, `--filter missing\|broken\|central\|local`, `--min-size 500MB`, `--older-than 90d`, `--paths` | Table with `*` marking the active venv, right-aligned sizes, `~` for estimated last use, and `project → venv` |
| Find | `scan [root...]` | `-n/--dry-run` | Register venvs and redirect files, unregister vanished ones; live counter on consoles |
| Maintain | `nuke <name\|path>...` (`rm`) | `-f/--force`, `-n/--dry-run` | Delete, after showing and (with your OK) stopping lockers; editors and shells are never stopped |
| Maintain | `prune` | `--older-than`, `--min-size`, `--missing`, `--broken`, `--central`/`--local`, `-n/--dry-run`, `-y/--yes` | Bulk delete through the nuke path; needs at least one criterion; skips the active venv |
| Maintain | `repair [name\|path]` | `-n/--dry-run` | Rewrite a moved venv's old path in `Scripts\`, at path boundaries |
| Maintain | `doctor` | | Read-only checks (section 9) |
| Setup | `init [pwsh\|powershell\|bash\|cmd]` | `--install`, `-n/--dry-run`, `--remove` | No argument: update every shell, removing WinVenv/pyGo hooks first. `pwsh`/`bash`: print the hook code (`--install` installs that shell). `cmd`: install the CMD hook. |
| Setup | `allow [dir]` | `--list` | Trust a project root for auto-activate (default: the current folder's root) |
| Setup | `deny [dir]` | | Stop trusting it |
| Setup | `config [key [value]]` | | Show settings; `config auto-activate on\|off` |
| Setup | `version` | | Version, commit, build date, Go version, exe path |
| Setup | `completion powershell\|bash` | | Tab-completion script (works through the shell function) |
| hidden | `__shell <sh> on\|off\|cd\|auto ...` | | Called by the hooks |
| hidden | `bench-hook` | `--runs` | Times the PowerShell hook's no-spawn path |

### Resolving a name or path

1. **No argument.** Walk up from the current folder for `.venv` or `venv`. A `.venv` file is a redirect and is followed.
2. **A venv folder, or a redirect file.**
3. **A name under the central folder.**
4. **A registered name, case-insensitive.** If several venvs share it (you have `gravity-render` 3 times), venvctl lists them. In a console it asks "which one? [1-3]". Otherwise it fails with exit 2 and prints the exact `venvctl <verb> "<path>"` commands.

Entries built from disk are merged with registry data (project link, last use, cached size). `on`, `cd`, `nuke`, `repair` and `link` all use this.

---

## 6. How activation works

### 6.1 The core trick

`venvctl init` defines a `venvctl` function in each shell. For `on`, `off` and `cd`, the function runs `venvctl.exe __shell <shell> <verb> ...`. That prints a script, and the function evaluates it in the current shell. Every other verb passes straight through. Running the exe directly for `on` explains that the hook is missing and how to load it.

### 6.2 ExecutionPolicy

Policy governs `.ps1` files, not strings passed to `Invoke-Expression`, so `venvctl on` works even under `Restricted`. That was tested. `$PROFILE` itself is a `.ps1`, though, so under `Restricted`/`AllSigned` the hook does not load. Doctor names the deciding scope. For Group Policy scopes (MachinePolicy, UserPolicy) it suggests `powershell -NoExit -Command "venvctl init pwsh | Out-String | iex"`.

### 6.3 Per shell

| Step | PowerShell | CMD | Git Bash |
|---|---|---|---|
| Save PATH once | `$env:_VENVCTL_OLD_PATH` | `_VENVCTL_OLD_PATH` | `_VENVCTL_OLD_PATH` |
| Set | `VIRTUAL_ENV`, `VIRTUAL_ENV_PROMPT`, PATH = `Scripts;` + saved | same | same, with POSIX paths |
| Clear | `PYTHONHOME` | same | same |
| Prompt | wraps `prompt` with `(name)`, unless oh-my-posh or starship is active | `PROMPT=(name) <old>` | `PS1` prefix, unless oh-my-posh or starship is active |
| Auto mark | `_VENVCTL_AUTO=1` only when auto-activate did it; a manual `on` clears it | same | same |

**PowerShell details.**
- The wrapper forces UTF-8 around the exe call, so PS 5.1 doesn't garble non-ASCII paths. That was tested with an Arabic and accented folder.
- Prompt functions are read and written as `${function:global:name}`. `Get-Item` and `Remove-Item` silently ignore a `function:global:` prefix, which is why `off` never restored the prompt in WinVenv and pyGo. It was fixed in the rename and tested with a round trip.

**CMD details.**
- The doskey macro is `venvctl="<exe>" __shell cmd $* $T call "<state>\venvctl_env.cmd"`.
- Macros only work at an interactive prompt.
- There's no recursion guard. The hook is one doskey line, and a guard variable would break `venvctl` in child CMD windows.

**Compatibility.** A shell activated before the rename still has `_PYGO_OLD_*` and `_pygo_old_prompt`. `on` and `off` adopt those values, then clear them.

---

## 7. Auto-activate on cd (opt-in)

Turn it on with `venvctl config auto-activate on`, open a new terminal, then run `venvctl allow` in each project you want.

**Trust.**
- Putting a folder's `Scripts` on PATH runs its code the moment you type `python`. So only roots in `trust.json` auto-activate.
- An untrusted root with a venv prints, once per session: `venvctl: <root> has a venv; run 'venvctl allow' to auto-activate`.

**The root rule.** Go `FindRoot`, the PowerShell hook, the bash hook and the Clink script all implement the same rule:
- walk up at most 12 folders, stopping after a folder that contains `.git`
- the root is the closest folder with a venv: a `.venv` redirect file, `.venv\pyvenv.cfg` or `venv\pyvenv.cfg`
- failing that, the closest folder with `pyproject.toml`

**Rules.**
- In a monorepo, the closest `.venv` wins.
- Moving within one root does nothing.
- Leaving a root deactivates only what auto-activate itself activated (`_VENVCTL_AUTO=1`).
- A venv you or VS Code activated (`VIRTUAL_ENV` without the mark) is never touched, whether entering or leaving.
- After `deny`, the venv stays until the next root change.

**Hooks.**

| Shell | Attached via | Cost before any spawn |
|---|---|---|
| PowerShell | wraps `prompt`; with oh-my-posh it uses `Set-PoshContext`, with starship `Invoke-Starship-PreCommand`; `VENVCTL_AUTO_HOOK=cd` opts into a zoxide-safe `cd` wrapper | pure .NET calls: path compare, `File.Exists` walk, `trust.json` read only when its timestamp changes |
| Git Bash | `PROMPT_COMMAND` | builtins only; on a root change, one `pwd -W` subshell to get the Windows path |
| CMD | Clink `clink.onbeginedit` in `%LOCALAPPDATA%\clink\venvctl.lua`; plain CMD has no per-prompt hook, so it's skipped with a note | Lua file checks; `io.popen` only on a trusted root change |

`venvctl.exe __shell <sh> auto <root>` is spawned only when the root changed and is trusted, or when leaving an auto-activated venv.

**Measured** with `venvctl bench-hook` on PowerShell 5.1, 1,000 runs each:

| Case | p50 | p99 |
|---|---|---|
| folder unchanged | 0.085 ms | 0.43 ms (target was under 2 ms) |
| folder changed, same root (5-level walk) | 0.73 ms | 1.73 ms |

**Ordering caveat.** The hook checks for oh-my-posh, starship and zoxide when the profile runs. Keep the `venvctl` line after their init lines in `$PROFILE` and `.bashrc`.

---

## 8. Windows internals

### 8.1 Restart Manager and lockers

`rstrtmgr.dll` is called directly: `RmStartSession`, then `RmRegisterResources` with every `.exe`, `.dll` and `.pyd` in the venv, then `RmGetList` (retried on `ERROR_MORE_DATA`), then `RmEndSession`. It needs no admin. Each locker gets:
- the exe path, via `QueryFullProcessImageName`
- its parent, from a Toolhelp process snapshot ("SPAWNED BY")
- its **open files**: the venv modules it has loaded, from a Toolhelp module snapshot

### 8.2 Nuke and prune

```
resolve all targets first (a typo deletes nothing)
for each: gone? -> unregister
  lockers? -> table PID / NAME / SPAWNED BY / OPEN FILES
    editor or shell (Code, Cursor, PyCharm, IntelliJ, devenv, Explorer, Terminal, pwsh, powershell, cmd, bash) -> refuse (exit 3)
    --force -> terminate | console -> "terminate N process(es)? [y/N]" (default No) | else refuse (exit 3)
  rename <dir> -> <dir>.<ns>.venvctl-trash   (backoff 0..800 ms; failure = untouched, "still locked")
  clear read-only, RemoveAll(trash)            (backoff; failure = "leftovers remain in <trash>")
  sweep older <dir>.*.{venvctl,pygo,wv}-trash leftovers
  unregister; registry saved even if a later target fails
```

### 8.3 Repair

The old path is read from `activate.bat`, in either the venv or the uv format. Every file in `Scripts\` is then rewritten. uv launchers (`UVSC`/`UVPY`/`UVUV` trailer) get the length-prefixed path replaced and the length fixed up. Everything else gets a **boundary-aware** replace in both slash forms: the old path must be followed by `\ / " ' ) ;`, whitespace, end of line or end of data. So `C:\x\proj` never rewrites `C:\x\proj2`; that's unit-tested. `--dry-run` lists the files that would change. `pyvenv.cfg` is left alone.

### 8.4 Other

- **Store alias.** The stub is detected by reparse tag `0x8000001B` through `FindFirstFile`.
- **PEP 514.** venvctl reads HKCU, HKLM and WOW6432Node `Software\Python\<Company>\<Tag>`, sorted newest first with `slices.Compare`.
- **Scan.**
  - It runs one goroutine per folder, with 64 directory reads at a time, and stops descending at a `pyvenv.cfg`.
  - It skips the system folders, `AppData`, `node_modules`, `.git` and similar, symlinks, and trash folders.
  - It also returns `.venv` redirect files, and roots are deduplicated.
- **Cloud roots.** `OneDrive*` environment variables, plus `Dropbox`, `Google Drive` and `iCloudDrive` under your profile. Doctor and `create --local` share the check.

---

## 9. Doctor checks

| Check | Warns / fails when | Fix it prints |
|---|---|---|
| python on PATH | the first `python.exe` is the Store stub (fail), or none on PATH (warn) | aliases / PATH order |
| Store app execution alias | stubs exist (warn) | disable aliases |
| registered interpreters (PEP 514) | none (warn) | uv-managed still work |
| PowerShell ExecutionPolicy | deciding scope is Restricted/AllSigned (fail), naming the scope | `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`, or the Group Policy workaround |
| LongPathsEnabled | disabled (warn) | admin `reg add` |
| VC++ 2015-2022 runtime | `vcruntime140.dll` missing (fail) | vc_redist link |
| uv | missing (warn) | install command |
| venvctl shell hooks | a shell missing, stale (calls another exe) or removed (warn); only `venvctl.exe` lines count | `venvctl init` |
| legacy hook found (WinVenv/pyGo) | one per shell with old lines (warn) | `venvctl init` (`--dry-run` previews) |
| venvctl CMD hook removed (AutoRun was rewritten, probably by VS Code) | the hook file exists but AutoRun no longer calls it (warn) | `venvctl init cmd` |
| old state folders | `%LOCALAPPDATA%\pygo` or `\winvenv` still present (warn) | delete after checking `venvctl list` |
| venvs in cloud-synced folders | registered venvs under OneDrive/Dropbox/... (warn) | move to a central env |
| IDE discovery of central envs | a central env has no project redirect, and no VS Code/Cursor/VSCodium settings list the central folder (warn) | `venvctl link`, or the `python.venvFolders` snippet |

Warnings exit 0; only failures exit 1. Doctor never changes anything.

### Your machine (dry runs from the new build against a temp state folder)

| Check | Result |
|---|---|
| python on PATH | ✓ `C:\ProgramData\anaconda3\python.exe` |
| Store alias | ! stubs present |
| interpreters | ✓ 3.13.7, 3.12.3, 3.11.7, 3.10.0, PythonCore/3.11 |
| ExecutionPolicy | ✓ RemoteSigned (CurrentUser) |
| LongPathsEnabled | ! disabled |
| hooks | ! none yet; the legacy WinVenv hook is found in PowerShell 5.1, CMD and Git Bash |
| old state folders | ! `pygo` and `winvenv` (when run against the real `%LOCALAPPDATA%`) |

---

## 10. Bugs found and fixed

| When | Bug | Fix |
|---|---|---|
| first smoke test | `repair` could not read uv venvs | accepts uv's `for %%i in ("...")` form |
| first smoke test | warnings shown as failures | warn wins |
| first smoke test | blank interpreter row | company/tag fallback |
| research brief | `repair` corrupted uv console-script launchers (`UVSC` unknown) | all three magics, test |
| Phase 0 | doctor counted old `wv` hooks as installed | compares the exe each hook calls |
| Phase 0 | `repair` rewrote `C:\x\proj2` when moving `C:\x\proj` | boundary-aware replace, test |
| Phase 0 | duplicate names were a dead end | picker / exact commands |
| Phase 0 | suggested commands doubled backslashes (`%q`) | plain quotes |
| Phase 1 | styled table headers misaligned (ANSI counted as width) | width-aware table helper |
| rename | `off` never restored the PowerShell prompt (since WinVenv) | `${function:global:...}` syntax, tested |
| Phase 3 | `cd` of a linked env went to the venv, not the project | registry merge in Resolve, test |
| Phase 3 | a redirect found by scan renamed the central env after the project | Upsert keeps the env's name |
| Phase 4 | Git Bash trust check failed for MSYS paths (`/tmp/...`) | `pwd -W` on root change |
| Phase 4 | Git Bash activated with a lower-cased path | keep original case |
| docs pass | `--json link` exited 0 when already linked; `config` emitted `null` for an empty list | exit 4; `[]` |

Editing mishaps along the way, none of which reached a commit:
- Python and heredoc escaping dropped backslashes or put real newlines into Go strings.
- A raw BOM landed in source.
- `sed` treated `\u` as uppercase.

Since then, every edit with backslashes goes through the Edit and Write tools.

---

## 11. Ponytail audit: applied

| # | Item | Status |
|---|---|---|
| 1 | `versionLess` to `slices.Compare` | done in Phase 1 (`discovery.CompareVersions`) |
| 2 | `Venv.Home`/`Version`, `Scan` returns paths | done in Phase 1 |
| 3 | `NotFoundError` type | done in Phase 7 (`fmt.Errorf`) |
| 4 | duplicate backoff loops | done in Phase 1 (`retry`) |
| 5 | duplicated `$PROFILE` lookup | done in Phase 0 (`shell.Profiles`, no spawn) |
| 6 | `list` mutex | done in Phase 1 |
| 7 | `EvalSymlinks` | done in Phase 1 |
| 8 | `orNone` | done in Phase 7 |
| 9 | `interpreters()` OK flip | done in Phase 7 |
| 10 | Group Policy detection by fix text | done in Phase 7 (`gpo` flag) |
| 11 | `shell.hookDir` duplicate | done in the rename (`envmgr.Dir`) |
| 12 | drop lipgloss | skipped by decision (spec) |

---

## 11a. History: the rename from WinVenv/wv to pyGo/pygo

> **Superseded.** This records the WinVenv-to-pyGo rename as it happened. Do not run the paste-in snippet below: `venvctl init` now removes WinVenv and pyGo hooks itself (section 11b).

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

## 11b. Rename from pyGo to venvctl

**Why.** `pygo` collides with an existing ReiterAdam/pygo venv CLI on GitHub, the PyGo Framework CLI, pygo-mcp, and pygo-tools on PyPI. `*ctl` signals a tool that manages things it didn't create: uv creates, venvctl manages.

| Thing | pyGo | venvctl |
|---|---|---|
| Product name | pyGo | venvctl |
| Command / binary | `pygo` / `pygo.exe` | `venvctl` / `venvctl.exe` |
| Go module | `pygo` | `venvctl` |
| Entry folder | `cmd/pygo` | `cmd/venvctl` |
| State folder | `%LOCALAPPDATA%\pygo` | `%LOCALAPPDATA%\venvctl` (or `VENVCTL_HOME`) |
| Hook files | `pygo_hook.cmd`, `pygo_env.cmd` | `venvctl_hook.cmd`, `venvctl_env.cmd` |
| Env vars | `_PYGO_OLD_PATH`, `_PYGO_OLD_PROMPT`, `_PYGO_OLD_PS1` | `_VENVCTL_OLD_PATH`, `_VENVCTL_OLD_PROMPT`, `_VENVCTL_OLD_PS1` |
| Trash suffix | `.pygo-trash` | `.venvctl-trash` |
| Profile comment | `# pyGo shell hook` | `# venvctl shell hook` |
| Shell function / doskey macro | `pygo` | `venvctl` |

There is no `pygo` alias or shim. The old name stops existing.

### Backward compatibility (deliberately small)

- **Shells activated before the rename.** `venvctl off` (and `on`) adopt `_PYGO_OLD_PATH`, `_PYGO_OLD_PROMPT`, `_PYGO_OLD_PS1` and PowerShell's `_pygo_old_prompt` when the venvctl ones are unset, then clear the old ones. Such a shell therefore deactivates to its original PATH and prompt. Tested in a throwaway PowerShell.
- **Old trash folders.** `*.pygo-trash` and `*.wv-trash` count as trash, like `*.venvctl-trash`. Scan skips them, and nuke and prune sweep any left next to a venv they delete.

### Legacy hook cleanup in `venvctl init`

For each shell it installs into, `venvctl init` removes every WinVenv and pyGo hook before adding its own:

- **Profile lines.** This covers each PowerShell `$PROFILE` and `~/.bashrc`. It removes a line containing `# WinVenv shell hook` or `# pyGo shell hook` along with the line after it, plus any line invoking `wv.exe`, `pygo.exe`, `winvenv\bin` or `pygo\bin`. A venvctl hook line calling a different exe is removed as stale. Every other line is kept byte-for-byte. PowerShell profiles keep their encoding (UTF-8, UTF-8 with BOM, UTF-16) and line endings. `.bashrc` is written with LF and no BOM.
- **CMD AutoRun.** The value is split on ` & `. Segments referencing `wv_hook.cmd`, `pygo_hook.cmd`, `winvenv\` or `pygo\` are dropped. Every other segment, such as VS Code's `vscode-python.bat`, keeps its order. The venvctl segment is kept in place or appended. The value type, REG_SZ or REG_EXPAND_SZ, is preserved.
- **Report.** Each shell gets one line, for example `PowerShell 5.1: removed 1 legacy line (WinVenv), added venvctl hook`.
- **Flags.** `venvctl init --dry-run` prints the report and writes nothing. `venvctl init --remove` removes venvctl and legacy hooks and adds nothing; it is the uninstall step.

On this machine `venvctl init --dry-run` reports one WinVenv line to remove in each of PowerShell 5.1, CMD and Git Bash. Your current hooks still date from WinVenv, because the pyGo hook migration snippet was never run.

### Doctor

- The hook check only counts lines invoking a `venvctl.exe`, and it must be the running one.
- A "legacy hook found (WinVenv/pyGo)" warning appears for each shell that still has old lines.
- An "old state folders" warning appears while `%LOCALAPPDATA%\pygo` or `%LOCALAPPDATA%\winvenv` exists.

### First-run state migration

venvctl migrates on any command when all three of these hold:
- `%LOCALAPPDATA%\venvctl\registry.json` does not exist
- no `migrated_from` marker is set
- `%LOCALAPPDATA%\pygo\registry.json` exists

It then:

1. copies `registry.json` and `config.json` into `%LOCALAPPDATA%\venvctl`
2. renames each folder in `pygo\envs\` to `venvctl\envs\<name>`, which is atomic on one volume, then runs repair on it, because central envs embed their absolute path in launchers and `activate.bat`
3. leaves a locked env in place, with its registry entry still pointing at the old path, and reports it; a failed repair is reported with the `venvctl repair <path>` fix
4. points registry entries for moved envs at their new location
5. prints a one-time summary on stderr
6. writes `migrated_from: "pygo"` and `migrated_at` into `config.json`, keeping every other key

It never deletes anything in the old folder. The check uses `registry.json`, not the folder itself, so installing `venvctl\bin` first does not block it.

`VENVCTL_HOME` overrides the state folder. The legacy folders are then looked for as its siblings. That's how the migration test runs against a temp folder, using a real uv venv that it moves, repairs and runs.

### Local install done for you

- `venvctl.exe` built (unstripped) and copied to `%LOCALAPPDATA%\venvctl\bin\`.
- In your user PATH, `C:\Users\Piyush\AppData\Local\pygo\bin` was replaced by `C:\Users\Piyush\AppData\Local\venvctl\bin`. There was no `winvenv\bin` entry. The system PATH was not touched. .NET's `SetEnvironmentVariable` broadcasts `WM_SETTINGCHANGE`.
- `%LOCALAPPDATA%\pygo\bin\pygo.exe` was left in place.
- **Not done: the real first-run migration.** Claude Code's safety check blocked running `venvctl` against your real `%LOCALAPPDATA%`, so the migration happens the first time you run `venvctl` yourself.

---

## 12. Release engineering

- **`build.ps1`.**
  - **Builds.** It reads the version from `git describe`, or `-Version`. It builds amd64 and arm64 with `-trimpath` and `-ldflags -X` for the version and build date, and never strips with `-s -w`.
  - **Resources.** It embeds `go-winres` resources when the tool is installed, otherwise warns.
  - **Output.** It writes `dist\venvctl-<ver>-windows-<arch>\venvctl.exe`, a zip of each, and `dist\SHA256SUMS`.
  - **Signing.** `--sign` (or `-Sign`) signs with `signtool` when `VENVCTL_SIGN_CERT` is set, with `VENVCTL_SIGN_PASSWORD` and `VENVCTL_SIGN_TIMESTAMP` optional. Without the variable it prints the exact commands.
  - **Verified.** A local build showed ProductName venvctl, `longPathAware` in the manifest, the version embedded, and checksums written.
- **`winres\winres.json`** holds the version info, the placeholder icons and the manifest (`win10` minimum, `longPathAware`).
- **`packaging\`** has winget (singleton draft) and Scoop drafts, with `<...>` placeholders for publisher, URLs and hashes.
- **`.github\workflows\ci.yml`** runs gofmt, vet, test and an unsigned build of both architectures on `windows-latest`, then uploads the artifacts. There's no release step yet.
- **`.gitattributes`** forces LF, because gofmt rejects CRLF and Windows runners check out with autocrlf.

---

## 13. Known issues

- **Hook ordering.** Auto-activate looks for oh-my-posh, starship and zoxide when the profile runs. If venvctl's line comes before theirs, the wrong attachment is used. Keep venvctl's line last.
- **First prompt after activation.** In prompt-wrapper mode it has no `(name)` prefix yet, because the prompt was replaced while rendering. The next prompt shows it.
- **Foreign venvs.** Switching away from a VS Code-activated venv keeps that venv's `Scripts` in the saved PATH, so after `off` it is back on PATH while `VIRTUAL_ENV` is unset.
- **Clink script untested.** Clink isn't installed here. The script renders and follows the same rule, but `os.unsetenv` and the `io.popen` quoting have never run.
- **ANSI-encoded PowerShell profiles.** A profile in a legacy code page with a non-ASCII exe path would get a UTF-8 path. UTF-8 and UTF-16 profiles are handled.
- **One project link per env.** `project` holds the last linked project. A central env linked from two projects lists only the latest.
- **Repair's uv-trampoline rewrite** keeps absolute paths. Whether uv's launcher accepts relative paths is unverified.
- **Old trash sweeping** only happens next to venvs being deleted. Stray trash elsewhere stays until you nuke it by path.
- **Duplicate names** need a path, or the console picker. Git Bash under mintty is not a console, so you get the printed commands instead.
- **Migration** of the real `%LOCALAPPDATA%\pygo` happens on your first `venvctl` run. I did not run it.

---

## 14. Testing

| Area | How |
|---|---|
| Unit and table tests | `go test ./...`: CLI flags and exit codes (31 cases), list filters on fake venvs, link/unlink, auto transitions, foreign-venv note, prune flow, repair rewrite and boundary, migration with a real uv venv moved, repaired and run, FindRoot, trust, profile/AutoRun filters (pyGo-only, mixed, VS Code blocks, stale, remove, idempotent), encodings, template rendering, VS Code settings coverage, JSONC |
| PowerShell, real | throwaway `powershell -NoProfile`, temp `VENVCTL_HOME`: on/off prompt round trip, pyGo-era `off`, `Restricted` policy, non-ASCII path, `testdata\autocd.ps1` (14 transitions, all pass), `bench-hook` |
| Git Bash, real | `bash --noprofile --norc` with the init code: enter, nested, untrusted hint, leave, PATH restored |
| Locks, real | a live `python.exe` holding a venv: dry run, refusal (exit 3, intact), `--force` |
| Create/redirect, real | uv create in a temp home: redirect bytes, refusal to repoint, `on` follows it, doctor IDE check |
| Build | `build.ps1` both arches, version resource, manifest, checksums, sign dry run |
| Your machine, read-only | `init --dry-run`, doctor, list (77 venvs, 15.8 GB) and prune dry run against a temp copy of your registry |

**Not tested:**
- the real `venvctl init` (not allowed to edit your profile, AutoRun or `.bashrc`)
- the real first-run migration (blocked by the safety classifier, and not to be run during testing)
- interactive prompts, which need a console
- Clink
- oh-my-posh and starship attachment
- a live VS Code rewriting AutoRun
- signing
- CI on GitHub (no remote)
- winget and Scoop installs

---

## 15. Decisions

All of them, with the reasoning, are in `floated.md`: entries 1–16 for WinVenv and pyGo, 17 for the rename to venvctl, and 18–25 for this run.

---

## 16. Not built yet

| Item | Note |
|---|---|
| Real signing and publishing | `build.ps1 --sign` is ready; needs a certificate (Azure Trusted Signing / SignPath) |
| Release workflow | CI builds artifacts but does not publish |
| Real icon | placeholders in `winres\` |
| README | placeholder; you are pasting the draft |
| Windows Terminal fragment / cached init text | only for Group Policy-locked or antivirus-slowed machines |
| Junction support | deliberately not built (OneDrive sync breaks) |
| Multiple projects per central env | registry keeps one `project` |
| `venvctl doctor --fix` | doctor stays read-only by design |

---

## 17. Cheat sheet

```powershell
venvctl                                # overview
venvctl scan                           # register venvs on all fixed drives
venvctl list --sort size               # biggest first; --filter broken|missing|central|local
venvctl prune --older-than 180d --dry-run
venvctl create api -p 3.12             # central env, linked here via .venv redirect
venvctl create --local                 # .\.venv
venvctl link api / venvctl unlink
venvctl on / venvctl on api / venvctl off / venvctl cd api
venvctl nuke old --dry-run             # see lockers first
venvctl repair -n                      # after moving a project
venvctl config auto-activate on        # then: venvctl allow (in each project)
venvctl doctor / venvctl --json doctor
venvctl init --dry-run / venvctl init / venvctl init --remove
```

Build: `.\build.ps1` (release) or `go build -o venvctl.exe ./cmd/venvctl` (dev). After `venvctl init`, keep the exe where it is, or rerun `init`.

---

## 18. Changelog

| Commit | Change |
|---|---|
| `fa23970` | chore: import pyGo at 29 Oct 2026 |
| `f69b207` | fix: stale hook detection, boundary-aware repair, duplicate-name picker, scan root dedupe |
| `a1d6c08` | feat(cli): `--json`/`--quiet`/`--no-color`, exit codes, `Fail()`, overview, list/scan/nuke/create flags, grouped help, version, completion |
| `d34c1f7` | feat: prune |
| `850bb69` | refactor!: rename pyGo to venvctl (legacy hook cleanup, migration, `VENVCTL_HOME`, compatibility, prompt restore fix) |
| `84d65d4` | feat: PEP 832 redirect files (create links, link/unlink, scan, IDE doctor check) |
| `a35d042` | feat: auto-activate on cd with trust (PowerShell, Git Bash, Clink) |
| `5d614ec` | feat: coexist with VS Code Python Environments |
| `9230019` | build: resources, build.ps1, packaging drafts, CI |
| `8436211` | build: force LF line endings |
| `18d61bb` | refactor: remaining audit items |
| (this commit) | docs: json.md, venvctl-complete.md refresh, design.md, floated.md 18–25; `--json link` exit code; `config` `[]` |
