@echo off
rem Fake PowerToys File Locksmith CLI for venvctl's tests. It records its arguments in
rem %FAKE_LOCKSMITH_ARGS% (so tests can prove --kill is never passed) and prints the JSON in
rem %FAKE_LOCKSMITH_JSON%, shaped like the real CLI's output:
rem   {"processes":[{"pid":N,"name":"...","user":"...","files":["..."]}]}
rem (microsoft/PowerToys src/modules/FileLocksmith/FileLocksmithCLI/CLILogic.cpp, get_json).
echo %*>"%FAKE_LOCKSMITH_ARGS%"
type "%FAKE_LOCKSMITH_JSON%"
