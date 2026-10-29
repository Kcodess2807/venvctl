# venvctl (`venvctl`) design

Windows-native Python venv manager. Go 1.22+, cobra, lipgloss, `x/sys/windows`.

## Problem to command map

| Pain | Command | Mechanism |
|---|---|---|
| Activation differs per shell, ExecutionPolicy blocks `Activate.ps1` | `venvctl on` / `off` / `cd` | Shell wrapper evals a script `venvctl` prints. No `.ps1` file is run, so policy does not apply. |
| A binary cannot change its parent's env | same | Wrapper function in the parent shell does the `eval`. |
| `Access is denied` on delete | `venvctl nuke` | Restart Manager lists lockers, `--force` kills, then retry delete. |
| Forgotten venvs eating disk | `venvctl scan`, `venvctl list` | Concurrent walk for `pyvenv.cfg`, registry file, sizes. |
| Renamed folder breaks venv | `venvctl repair` | Rewrite old absolute path inside `Scripts\`. |
| Misconfigured machine | `venvctl doctor` | Checks listed below. |

## Layout

```
cmd/venvctl/main.go        entry, calls cli.Execute
internal/cli          cobra commands, output styling
internal/discovery    pyvenv.cfg scan, PEP 514 registry finder
internal/envmgr       registry.json, config.json, create, delete, repair
internal/shell        wrapper/activate/deactivate/cd scripts, profile install
internal/winapi       Restart Manager, kill, App Execution Alias detection
internal/doctor       diagnostics
```

Dependencies point one way: `cli` -> `{doctor, envmgr, shell, winapi, discovery}`; `envmgr` -> `discovery`; `doctor` -> `{discovery, shell, winapi}`. No cycles.

## State

`%LOCALAPPDATA%\venvctl\`
- `registry.json`: every known env (name, path, base python dir, version, central flag, last used).
- `config.json`: `scan_roots`, `default_python`. Optional, read-only for now.
- `envs\<name>`: central environments.
- `venvctl_hook.cmd`, `venvctl_env.cmd`: CMD hook and its per-call output file.

## Activation flow

```
user types: venvctl on foo
  shell function venvctl  (installed by `venvctl init`)
    -> venvctl.exe __shell <pwsh|bash|cmd> on foo      (hidden command)
         resolve foo -> path, stamp last_used, print script
    <- script text
    -> Invoke-Expression / eval
         sets VIRTUAL_ENV, prepends Scripts to PATH, wraps prompt
```

`_VENVCTL_OLD_PATH` stores the pre-activation PATH so `off` restores it and re-activating does not stack PATH entries.

CMD cannot eval output, so the doskey macro runs `venvctl __shell cmd ...` which writes `venvctl_env.cmd`, then `call`s it. For non-hook commands in CMD, `__shell cmd` runs the normal command itself.

## Resolution order (`envmgr.Resolve`)

1. No argument: walk up from cwd for `.venv` or `venv`.
2. Argument is a venv directory.
3. Argument is a name under the central dir.
4. Argument matches a registered name. Ambiguous matches are an error.

A `.venv` that is a file is a PEP 832 redirect: its one line is the venv path, relative to the file's folder.

## Nuke flow

```
resolve -> Restart Manager on Scripts/*.exe, *.dll, *.pyd -> table: PID, name, parent, exe
  editor/shell in list -> refuse, user closes it
  else --force or [y/N] -> TerminateProcess each
-> rename dir to *.venvctl-trash (fails = still locked, nothing touched)
-> RemoveAll trash with backoff -> unregister
```

## Repair flow

Old path comes from `activate.bat`. Every file in `Scripts\` is rewritten: uv launchers (`UVSC`/`UVPY`/`UVUV` trailer) get their length-prefixed path replaced, everything else a byte replace in both slash forms. `--dry-run` lists files only.

## Doctor checks

python on PATH (Store stub first?), App Execution Alias stubs (reparse tag `0x8000001B`), PEP 514 interpreters, ExecutionPolicy with the deciding scope, `LongPathsEnabled`, `vcruntime140.dll`, `uv`, shell hooks installed, venvs inside OneDrive/Dropbox/Google Drive.

## Out of scope for now

Python version installs, lockfiles, dependency management (that is uv's job), Linux/macOS.
