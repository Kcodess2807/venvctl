# venvctl `--json` output

`--json` is a global flag: `venvctl --json <command>` or `venvctl <command> --json`.

## The contract

- **stdout** holds one JSON document, indented, UTF-8. Nothing else goes to stdout.
- **No styling and no prompts.** Where a prompt would appear, the command behaves as if there's no console: it refuses (exit 3) unless the flag that skips the prompt (`--yes`, `--force`) is given.
- **Errors** go to **stderr** as one compact JSON object per error:

  ```json
  {"error":"\"ghost\" is neither a venv folder nor a registered environment","fix":"run `venvctl list` to see names, or `venvctl scan` to register new venvs","exit_code":1}
  ```

  `nuke` and `prune` report per-venv failures inside their `results` instead, and print the error object for each one too.
- **Exit codes** are the same as in human mode:

  | Code | Meaning |
  |---|---|
  | 0 | ok |
  | 1 | failure |
  | 2 | usage error |
  | 3 | refused |
  | 4 | nothing to do |

  Exit 4 still prints the document, for example `[]` from `list`.
- **Field types.** Paths are Windows paths. Times are RFC 3339 / ISO 8601 with offset, or `null`. Sizes are bytes as integers.
- **Stability.** Fields are always present; there's no `omitempty`. New fields may be added. Existing fields don't change meaning without a major version bump.
- **First run.** The pyGo migration summary goes to stderr, so it never mixes with JSON on stdout.

## Venv object

Used by `list` (an array) and `prune` (`candidates`).

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Registered name. Project-local venvs are named after their project folder. |
| `path` | string | The venv folder (the one holding `pyvenv.cfg`) |
| `project` | string | Folder whose `.venv` redirect points here, or `""` |
| `python_home` | string | Base interpreter folder from `pyvenv.cfg` `home` |
| `version` | string | Python version from `pyvenv.cfg` |
| `central` | bool | Lives in `%LOCALAPPDATA%\venvctl\envs` |
| `status` | string | `ok`, `missing` (folder gone) or `broken` (base interpreter gone) |
| `size_bytes` | int | Total file size; 0 when missing |
| `last_used` | string\|null | venvctl's own activation stamp |
| `last_activity` | string\|null | Best estimate of last use: `last_used`, else the newest change to the folder, `pyvenv.cfg` or `site-packages` |
| `active` | bool | Equals the current `VIRTUAL_ENV` |

## `venvctl list`

An array of venv objects. Exit 4 when it's empty.

## `venvctl` (no command)

```json
{
  "version": "0.1.0",
  "active": "",
  "nearest": "D:\\PROJECTS\\api\\.venv",
  "envs": 77,
  "total_bytes": 16965442560,
  "hooks": [
    {"shell": "powershell", "state": "ok", "detail": "C:\\Users\\me\\Documents\\WindowsPowerShell\\Microsoft.PowerShell_profile.ps1"}
  ]
}
```

- `active` is `VIRTUAL_ENV`, or `""`.
- `nearest` is the venv `venvctl on` would pick here, or `""`.
- `total_bytes` uses the sizes cached by the last `list` or `prune`.
- `hooks[].state` is `ok`, `missing`, `stale` (the hook calls another exe) or `removed` (CMD AutoRun no longer calls the hook file).

## `venvctl doctor`

```json
{
  "checks": [
    {"name": "python on PATH", "status": "ok", "detail": "C:\\ProgramData\\anaconda3\\python.exe", "fix": ""},
    {"name": "LongPathsEnabled", "status": "warn", "detail": "disabled; ...", "fix": "admin: reg add ..."}
  ],
  "failed": 0
}
```

- `status` is `ok`, `info` (nothing to fix now, e.g. LongPathsEnabled off while uv handles long paths), `warn` or `fail`. The exit code is 1 only when `failed > 0`.
- "python on PATH" includes the App Execution Alias check: a PyManager or Store CPython alias passes, the Store redirector fails.
- Check names are stable. The list can grow, and per-shell checks such as `legacy hook found (WinVenv)` appear only when they apply.

## `venvctl scan`

```json
{
  "roots": ["D:\\PROJECTS", "C:\\Users\\me\\AppData\\Local\\venvctl\\envs"],
  "dirs_scanned": 12345,
  "found": 80,
  "added": [{"name": "api", "path": "D:\\PROJECTS\\api\\.venv"}],
  "pruned": [{"name": "old", "path": "D:\\gone\\.venv"}],
  "dry_run": false,
  "duration_ms": 2100
}
```

`found` counts venv folders plus `.venv` redirect files.

## `venvctl nuke`

```json
{
  "results": [
    {
      "name": "api",
      "path": "D:\\PROJECTS\\api\\.venv",
      "status": "deleted",
      "freed_bytes": 489366,
      "lockers": [
        {"pid": 1234, "name": "Python", "parent": "Code.exe", "exe": "D:\\...\\python.exe",
         "open_files": ["D:\\...\\Scripts\\python.exe"], "protected": false, "source": "Restart Manager"}
      ],
      "trash": "",
      "swept_trash": [],
      "error": ""
    }
  ]
}
```

- `status` is one of `deleted`, `would-delete` (`--dry-run`), `unregistered` / `would-unregister` (folder already gone), `refused` or `failed`.
- `protected` is true for editors and shells, which venvctl never terminates.
- `source` is `Restart Manager`, or `File Locksmith` for lockers found by the read-only PowerToys fallback (used when the rename fails and the Restart Manager named nobody).
- `trash` is set when leftovers remain after deleting.
- `swept_trash` lists older leftover trash folders, of any generation, that were removed alongside.
- Exit code: 1 if any result failed, else 3 if any was refused.

## `venvctl prune`

```json
{
  "candidates": [ /* venv objects */ ],
  "total_bytes": 489366,
  "dry_run": true,
  "results": [ /* nuke results; empty for --dry-run or when nothing was deleted */ ],
  "freed_bytes": 0
}
```

Without `--yes` in JSON mode, nothing is deleted: the document is printed and the command exits 3. With no candidates it exits 4.

## `venvctl create`

```json
{
  "name": "api",
  "path": "C:\\Users\\me\\AppData\\Local\\venvctl\\envs\\api",
  "python_home": "C:\\ProgramData\\anaconda3",
  "version": "3.11.5",
  "central": true,
  "command": ["C:\\Users\\me\\.local\\bin\\uv.exe", "venv", "C:\\...\\envs\\api"],
  "redirect": "D:\\PROJECTS\\api\\.venv"
}
```

`redirect` is the `.venv` redirect file written in the current folder, or `""` when none was written: with `--local`, `--no-redirect`, or after a failure.

## `venvctl repair`

```json
{
  "name": "api",
  "path": "D:\\new\\.venv",
  "old_path": "D:\\old\\.venv",
  "relocatable": false,
  "fixed": [
    {"path": "D:\\new\\.venv\\Scripts\\activate.bat", "kind": "text", "from": "D:\\old\\.venv", "to": "D:\\new\\.venv", "signed": false},
    {"path": "D:\\new\\.venv\\Scripts\\ruff.exe", "kind": "uv-rcdata", "from": "D:\\old\\.venv\\Scripts\\python.exe", "to": "python.exe", "signed": true}
  ],
  "skipped": [
    {"path": "D:\\new\\.venv\\Scripts\\tool.exe", "kind": "other-pe", "reason": "unknown launcher format; not modified (reinstall the package that owns it)"}
  ],
  "dry_run": false,
  "changed": true
}
```

- `kind` is one of:
  - `uv-rcdata` (uv ≥ 0.9.9; the path is in a PE resource)
  - `uv-trailer` (older uv)
  - `distlib` (pip)
  - `text`
  - `other-pe`
  - `binary`
- `from`/`to` are the interpreter path for launchers, or the old/new venv folder for text files.
- `to` is relative (`python.exe`, `<launcher_dir>\python.exe`) where repair made a launcher relocatable.
- `signed` means the launcher had an Authenticode signature, which the rewrite invalidates.
- `skipped` lists files that mention the old path but have no format venvctl edits safely.
- `old_path` is `""` and `relocatable` is true for relocatable venvs.
- It exits 4 with `"fixed": []` when nothing needs repair: the venv is already in place, or it's relocatable.

## `venvctl link` / `venvctl unlink`

```json
{"project": "D:\\PROJECTS\\api", "venv": "C:\\...\\envs\\api", "redirect": "D:\\PROJECTS\\api\\.venv", "changed": true}
```

`link` exits 4 with `"changed": false` when the redirect already points there.

## `venvctl init`

```json
{
  "dry_run": true,
  "changes": [
    {"shell": "PowerShell 5.1", "target": "C:\\...\\Microsoft.PowerShell_profile.ps1",
     "removed": {"WinVenv": 1}, "added": true, "changed": true,
     "report": "PowerShell 5.1: removed 1 legacy line (WinVenv), added venvctl hook"}
  ]
}
```

`removed` keys are `WinVenv`, `pyGo`, `venvctl (stale)` and `venvctl` (with `--remove`).

## `venvctl allow --list`

```json
{"trusted": ["d:/projects/api"]}
```

Keys are normalized: lower case, forward slashes. That's also the on-disk format of `trust.json`.

## `venvctl config`

```json
{"scan_roots": [], "default_python": "", "auto_activate": false, "migrated_from": "pygo", "migrated_at": "2026-10-29T16:00:00+05:30"}
```

`migrated_from` and `migrated_at` only appear after the pyGo migration.

## `venvctl version`

```json
{"version": "0.1.0", "commit": "18d61bbcd2a1", "build_date": "2026-10-29", "go": "go1.27.3", "exe": "C:\\...\\venvctl.exe"}
```

`commit` ends in `-dirty` for builds from a modified tree.
