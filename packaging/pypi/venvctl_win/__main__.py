"""`python -m venvctl_win ...` runs the venvctl.exe that pip installed for this interpreter."""

import os
import shutil
import subprocess
import sys
import sysconfig


def find_exe() -> str:
    # pip puts .data/scripts files in the scheme's scripts dir: the interpreter's Scripts\ (or the
    # user scheme's for --user). Fall back to PATH.
    candidates = [sysconfig.get_path("scripts")]
    if "nt_user" in sysconfig.get_scheme_names():
        candidates.append(sysconfig.get_path("scripts", "nt_user"))
    for d in candidates:
        p = os.path.join(d, "venvctl.exe")
        if os.path.exists(p):
            return p
    return shutil.which("venvctl") or ""


def main() -> None:
    exe = find_exe()
    if not exe:
        sys.exit("venvctl.exe not found next to this interpreter; reinstall with: pip install --force-reinstall venvctl-win")
    sys.exit(subprocess.call([exe, *sys.argv[1:]]))


if __name__ == "__main__":
    main()
