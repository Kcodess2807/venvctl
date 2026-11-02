"""Build the `venvctl-win` wheel: a box for venvctl.exe so `pip install venvctl-win` works.

    python packaging/pypi/build_wheel.py --version 0.1.0 --arch amd64 \
        --exe dist/venvctl-0.1.0-windows-amd64/venvctl.exe --out dist/wheels

The wheel holds the exe under `<name>-<ver>.data/scripts/`, which pip copies into the
interpreter's Scripts\ folder, plus a tiny `venvctl_win` package so `python -m venvctl_win`
works when Scripts\ is not on PATH. Standard library only; no build backend.
"""

import argparse
import base64
import hashlib
import pathlib
import zipfile

NAME = "venvctl-win"
PKG = "venvctl_win"
TAGS = {"amd64": "win_amd64", "arm64": "win_arm64"}
HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parents[1]

METADATA = """\
Metadata-Version: 2.1
Name: {name}
Version: {version}
Summary: Control your Python virtual environments on Windows (the venvctl CLI, as a pip-installable binary)
Home-page: https://github.com/Kcodess2807/venvctl
Author: Arush Karnatak
License: MIT
Project-URL: Source, https://github.com/Kcodess2807/venvctl
Project-URL: Issues, https://github.com/Kcodess2807/venvctl/issues
Keywords: venv,virtualenv,windows,powershell,uv
Classifier: Environment :: Console
Classifier: Operating System :: Microsoft :: Windows
Classifier: Programming Language :: Python :: 3
Classifier: License :: OSI Approved :: MIT License
Classifier: Topic :: Software Development
Requires-Python: >=3.8
Description-Content-Type: text/markdown

{readme}
"""

WHEEL = """\
Wheel-Version: 1.0
Generator: venvctl build_wheel.py
Root-Is-Purelib: false
Tag: py3-none-{tag}
"""


def record_line(path: str, data: bytes) -> str:
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
    return f"{path},sha256={digest},{len(data)}"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True, help="PEP 440 version, e.g. 0.1.0")
    ap.add_argument("--arch", choices=TAGS, required=True)
    ap.add_argument("--exe", required=True, type=pathlib.Path)
    ap.add_argument("--out", default="dist/wheels", type=pathlib.Path)
    a = ap.parse_args()

    tag = TAGS[a.arch]
    dist = f"{PKG}-{a.version}"
    a.out.mkdir(parents=True, exist_ok=True)
    wheel_path = a.out / f"{dist}-py3-none-{tag}.whl"

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    files: dict[str, bytes] = {
        f"{PKG}/__init__.py": (HERE / PKG / "__init__.py").read_text(encoding="utf-8").replace("0.0.0", a.version).encode(),
        f"{PKG}/__main__.py": (HERE / PKG / "__main__.py").read_bytes(),
        f"{dist}.data/scripts/venvctl.exe": a.exe.read_bytes(),
        f"{dist}.dist-info/METADATA": METADATA.format(name=NAME, version=a.version, readme=readme).encode(),
        f"{dist}.dist-info/WHEEL": WHEEL.format(tag=tag).encode(),
        f"{dist}.dist-info/LICENSE": (ROOT / "LICENSE").read_bytes(),
    }
    record = "\n".join(record_line(p, d) for p, d in files.items()) + f"\n{dist}.dist-info/RECORD,,\n"

    with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as z:
        for p, d in files.items():
            z.writestr(p, d)
        z.writestr(f"{dist}.dist-info/RECORD", record)
    print(wheel_path)


if __name__ == "__main__":
    main()
