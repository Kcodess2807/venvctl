# `.venv` redirect files (PEP 832)

venvctl can keep a project's venv outside the project folder, in its central store
(`%LOCALAPPDATA%\venvctl\envs\<name>`), and still have editors and tools treat it as the
project's environment. It does this with a `.venv` *redirect file*.

## What a redirect file is

[PEP 832](https://peps.python.org/pep-0832/) proposes that a project root may contain a `.venv`
**file** instead of a `.venv` folder. The file holds one line: the path to the real venv.

```
D:\PROJECTS\api\.venv          ← a 1-line text file
C:\Users\me\AppData\Local\venvctl\envs\api
```

venvctl writes it as UTF-8 without a BOM, one line, an absolute path, ending in `\n`. When it reads
a redirect, a relative path resolves against the folder holding the file, a BOM is tolerated, and
the content is treated as path data only. It is never run or evaluated.

## Status: draft

PEP 832 is a **draft** that targets Python 3.15. It is not accepted yet. Support across tools is
uneven and moving:

- **virtualenv 21.12.0+** writes redirect files next to the envs it creates.
- **VS Code's environment discovery** has a pull request adding redirect support (microsoft/python-environment-tools #433). It was unverified when this was written.
- **uv** is listed as a supporter. No release note shows it reading redirects yet.
- **Hatch** opposes the PEP.

A tool that expects `.venv` to be a folder may ignore the file or report an error. If that bites,
`venvctl unlink` removes the file. The venv itself stays where it is.

## How venvctl uses it

| Command | What happens |
|---|---|
| `venvctl create <name>` | Creates the central env and writes `.venv` in the current folder pointing at it. `--no-redirect` skips this. |
| `venvctl link <name\|path>` | Writes or updates `.venv` here for an existing env. |
| `venvctl unlink` | Removes the `.venv` redirect file here. It never removes a `.venv` folder. |
| `venvctl on` (no argument) | Follows a redirect in this folder or a parent. |
| `venvctl cd <name>` | Goes to the linked project folder, not the central env folder. |
| `venvctl scan` | Registers redirect files it finds, so `list` shows `<project> → <venv>`. |
| `venvctl doctor` | "IDE discovery" warns when a central env has no project redirect and no VS Code setting lists the central folder. |

Safety rules, the same for `create` and `link`:

- **A `.venv` folder blocks it.** That folder is a real project-local venv, and venvctl will not replace it.
- **A redirect pointing at a different venv is only replaced after you agree.** In a console venvctl asks; without one it needs `--force`.
- **Pointing it at the same venv again changes nothing** and exits 4.

## If your editor does not follow redirects yet

Tell VS Code where central envs live instead. This goes in `settings.json`, as a path relative to your home folder:

```json
"python.venvFolders": ["AppData\\Local\\venvctl\\envs"]
```

The central envs then show up in the interpreter picker for every project. `venvctl doctor` stops
warning once the central folder is listed in `python.venvFolders`,
`python-envs.workspaceSearchPaths` or `python-envs.globalSearchPaths`. It checks VS Code, VS Code
Insiders, Cursor and VSCodium settings, and never edits them.
