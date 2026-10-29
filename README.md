# venvctl

Placeholder: the README draft is coming separately and will replace this file. When it does, keep
the wording below. It comes from the competitive research (`reports\Windows venv tools versus venvctl.md`).

---

## Required wording for the final README

**Name.** Put this near the top:

> Not related to the `venvctl` package on PyPI.

**Activation.** This replaces any claim that ExecutionPolicy "doesn't matter" or is "bypassed":

> Uses the same `on`/`off` in every shell and never runs Activate.ps1. PowerShell still needs to load
> your profile; if your policy blocks that, `venvctl doctor` explains the fix, and on Group-Policy-locked
> machines use the Windows Terminal profile it can generate.

⚠ **Not built yet:** venvctl cannot generate that Windows Terminal profile today. Build it before
publishing the last clause, or end the sentence after "explains the fix."

**Repair scope.**

> `venvctl repair` fixes venvs made by pip/venv and older uv after their folder moved; new uv venvs made
> with `--relocatable` (the default for `venvctl create`) don't need it.

**CMD auto-activate.**

> Auto-activate on `cd` works in PowerShell and Git Bash. In CMD it needs Clink and is experimental (untested).

**Comparison table.** Add this row, honestly:

| Tool | Finds lockers | How | Delete | Protects editors/shells | Needs |
|---|---|---|---|---|---|
| PowerToys File Locksmith | yes, more of them than venvctl | full handle + module scan; CLI with `--json`, `--kill` (kills every locker, no filter) | no | no | PowerToys install |
| venvctl `nuke` | yes | Restart Manager (`.exe`/`.dll`/`.pyd`), File Locksmith as read-only fallback | rename-first, never half-deleted | yes, plus [y/N] | nothing extra |
