# venvctl design

Windows-native Python venv manager. Go, cobra, lipgloss, `x/sys/windows`. uv creates, venvctl manages.

## Problem to command map

| Pain | Command | Mechanism |
|---|---|---|
| Activation differs per shell; ExecutionPolicy blocks `Activate.ps1` | `on` / `off` / `cd` | A shell function evaluates a script that `venvctl` prints; no `.ps1` runs |
| A binary cannot change its parent's env | same | `init` installs the function in `$PROFILE`, CMD AutoRun (via doskey) and `.bashrc` |
| Typing `on` in every terminal | `config auto-activate on`, `allow` | A prompt hook that spawns only when a trusted root changes |
| `Access is denied` on delete | `nuke`, `prune` | Restart Manager finds lockers, you confirm, they're terminated, then a rename-first delete |
| Forgotten venvs eating disk | `scan`, `list`, `prune` | Concurrent `pyvenv.cfg` walk, registry, sizes, estimated last activity |
| Renamed folder breaks venv | `repair` | Boundary-aware path rewrite in `Scripts\`, aware of uv's launcher trailer |
| Central venvs invisible to editors | `create`, `link`, `unlink` | PEP 832 `.venv` redirect file |
| Misconfigured machine | `doctor` | Read-only checks |

## Layout

```
cmd/venvctl/main.go     os.Exit(cli.Run(...))
internal/cli            cobra commands, output modes, exit codes, Fail, migration trigger
internal/discovery      scan (venvs + redirect files, trash skipped), PEP 514, versions
internal/envmgr         registry, config, trust, FindRoot, resolve, create, delete, repair, redirect, migration
internal/shell          hook code, activate/deactivate scripts, init (filtering + legacy cleanup), auto-activate hooks
internal/winapi         Restart Manager, process/module snapshots, kill, Store alias
internal/doctor         diagnostics
```

Dependencies point one way:
- `cli` imports everything
- `doctor` imports `discovery`, `envmgr`, `shell` and `winapi`
- `shell` imports `envmgr`, which imports `discovery`

There are no cycles and no interfaces.

## State

The state folder is `%LOCALAPPDATA%\venvctl\`, or `VENVCTL_HOME`. It holds `registry.json`, `config.json`, `trust.json`, `envs\<name>`, `venvctl_hook.cmd`, `venvctl_env.cmd` and `bin\venvctl.exe`.

The legacy folders `pygo` and `winvenv` are siblings of it. On the first run venvctl copies pyGo's state, and moves and repairs pyGo's central envs. It never deletes anything.

## Activation flow

```
venvctl on foo                       (shell function from `venvctl init`)
  -> venvctl.exe __shell <pwsh|bash|cmd> on foo
       resolve foo (dir, central name, registry name; picker on duplicates)
       print: adopt _PYGO_OLD_* if present; save PATH once in _VENVCTL_OLD_PATH;
              VIRTUAL_ENV, VIRTUAL_ENV_PROMPT, PATH, prompt; clear _VENVCTL_AUTO
  <- eval in the current shell
```

## Auto-activate flow

```
before each prompt (prompt wrapper | Set-PoshContext | Invoke-Starship-PreCommand | PROMPT_COMMAND | clink.onbeginedit)
  same folder?                    -> return                     (string compare)
  root = FindRoot(cwd)            12 levels, stop at .git, closest .venv/venv, else pyproject.toml
  same root?                      -> return
  trusted root (trust.json substring) or _VENVCTL_AUTO=1?
    -> venvctl.exe __shell <sh> auto <root>
         trusted venv, nothing active        -> activate + _VENVCTL_AUTO=1
         someone else's venv active          -> nothing
         auto venv active, root left/denied  -> deactivate
  untrusted root with a venv      -> one-time hint per session
```

## Resolution order (`envmgr.Resolve`)

1. No argument: walk up for `.venv` (folder or redirect file) or `venv`.
2. A venv folder or a redirect file.
3. A name under the central folder.
4. A registered name. Several hits return an `AmbiguousError`, which becomes a picker or the exact commands to run.

Entries built from disk are merged with registry data (project, last use, size).

## Nuke flow

```
resolve all targets → lockers (RM) → refuse editors/shells → --force | [y/N] | refuse
→ rename to <dir>.<ns>.venvctl-trash (fails = untouched) → RemoveAll with backoff
→ sweep old *.venvctl-trash / *.pygo-trash / *.wv-trash siblings → unregister
```

`prune` selects by age, size or status, and runs each venv through the same path.

## Init flow

`init` updates each PowerShell profile, `.bashrc` and the CMD AutoRun value:
- It filters out WinVenv, pyGo and stale venvctl hooks. The filtering is pure and tested on strings.
- It keeps everything else, including the file's encoding, its line endings and VS Code's segments.
- It adds the venvctl hook, or keeps an existing one in place.

`--dry-run` only reports. `--remove` uninstalls.

## Doctor checks

- python on PATH
- Store alias
- PEP 514 interpreters
- ExecutionPolicy, naming the deciding scope
- LongPathsEnabled
- VC++ runtime
- uv
- venvctl hooks, including stale and removed hooks
- legacy hooks, per shell
- CMD hook removed by VS Code
- old state folders
- cloud-synced venvs
- IDE discovery of central envs

## Out of scope

Installing Pythons, lockfiles and dependencies (that's uv's job), Linux and macOS, junctions, and editing editor settings.
