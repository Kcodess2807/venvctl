package envmgr

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---- helpers ----

// uvVersion returns the installed uv's version, or skips the test when uv is missing.
func uvVersion(t *testing.T) (string, [3]int) {
	t.Helper()
	out, err := exec.Command("uv", "--version").Output()
	if err != nil {
		t.Skip("uv not installed")
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Skipf("cannot parse %q", out)
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return strings.TrimSpace(string(out)), v
}

func run(t *testing.T, exe string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command(exe, args...).CombinedOutput()
	return string(out), err
}

// uvVenv creates a real venv at dir with the installed uv (pip seeded), optionally relocatable.
func uvVenv(t *testing.T, dir string, relocatable bool) {
	t.Helper()
	args := []string{"venv", "-q", "--seed", dir}
	if relocatable {
		args = append(args, "--relocatable")
	}
	if out, err := run(t, "uv", args...); err != nil {
		t.Fatalf("uv venv: %v\n%s", err, out)
	}
}

// uvInstall installs pkg into the venv with uv (cache first, then network) or skips.
func uvInstall(t *testing.T, venv, pkg string) {
	t.Helper()
	py := filepath.Join(venv, "Scripts", "python.exe")
	if _, err := run(t, "uv", "pip", "install", "-q", "--offline", "--python", py, pkg); err == nil {
		return
	}
	if out, err := run(t, "uv", "pip", "install", "-q", "--python", py, pkg); err != nil {
		t.Skipf("cannot install %s with uv (offline cache empty and no network?): %s", pkg, out)
	}
}

// pipReinstallPip makes pip write its own distlib launcher (Scripts\pip.exe) or skips.
func pipReinstallPip(t *testing.T, venv string) {
	t.Helper()
	py := filepath.Join(venv, "Scripts", "python.exe")
	if out, err := run(t, py, "-m", "pip", "install", "-q", "--force-reinstall", "--no-deps", "pip"); err != nil {
		t.Skipf("pip cannot reinstall itself (no network?): %s", out)
	}
}

func sum(t *testing.T, p string) [32]byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// fakeVenv writes a venv skeleton at dir whose activate.bat names oldDir as its location.
func fakeVenvAt(t *testing.T, dir, oldDir string) string {
	t.Helper()
	scripts := filepath.Join(dir, "Scripts")
	os.MkdirAll(scripts, 0o755)
	os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte("home = C:\\Python313\nversion = 3.13.7\n"), 0o644)
	os.WriteFile(filepath.Join(scripts, "activate.bat"), []byte("@echo off\r\nset \"VIRTUAL_ENV="+oldDir+"\"\r\n"), 0o644)
	return scripts
}

// someRealPE returns a copy-able PE from Windows that has no uv resource (for synthetic fixtures).
func someRealPE(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe"))
	if err != nil {
		t.Skip("no System32\\where.exe to build a synthetic launcher from")
	}
	return b
}

// ---- tests ----

func TestReplaceBounded(t *testing.T) {
	old, new := `C:\x\proj`, `D:\moved\proj`
	in := `set "VIRTUAL_ENV=C:\x\proj"` + "\r\n" + // quote boundary
		`C:\x\proj2\Scripts\python.exe` + "\n" + // proj2: must not change
		`PATH=C:\x\proj;C:\x\projects` + "\n" + // semicolon boundary; projects untouched
		`home = C:\x\proj`
	want := `set "VIRTUAL_ENV=D:\moved\proj"` + "\r\n" +
		`C:\x\proj2\Scripts\python.exe` + "\n" +
		`PATH=D:\moved\proj;C:\x\projects` + "\n" +
		`home = D:\moved\proj`
	if got := string(replaceBounded([]byte(in), []byte(old), []byte(new))); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestClassify(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "uv-trailer-0.8.23.exe")
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if k := Classify(fixture, b); k != KindUvTrailer {
		t.Errorf("old uv launcher fixture: %s, want %s", k, KindUvTrailer)
	}

	dir := t.TempDir()
	pe := someRealPE(t)
	other := filepath.Join(dir, "other.exe")
	os.WriteFile(other, pe, 0o644)
	if k := Classify(other, pe); k != KindOtherPE {
		t.Errorf("plain PE: %s, want %s", k, KindOtherPE)
	}

	rc := filepath.Join(dir, "rc.exe")
	os.WriteFile(rc, pe, 0o644)
	if err := writeUvResource(rc, `C:\old\.venv\Scripts\python.exe`); err != nil {
		t.Fatalf("stamping UV_PYTHON_PATH: %v", err)
	}
	rb, _ := os.ReadFile(rc)
	if k := Classify(rc, rb); k != KindUvRcdata {
		t.Errorf("PE with UV_PYTHON_PATH: %s, want %s", k, KindUvRcdata)
	}
	if v, _ := readUvResource(rc); v != `C:\old\.venv\Scripts\python.exe` {
		t.Errorf("resource read back as %q", v)
	}

	distlib := append(append([]byte{}, pe...), []byte("#!C:\\old\\.venv\\Scripts\\python.exe\r\nPK\x03\x04zipdata")...)
	if k := Classify(filepath.Join(dir, "d.exe"), distlib); k != KindDistlib {
		t.Errorf("PE + shebang + zip: %s, want %s", k, KindDistlib)
	}
	if k := Classify("activate.bat", []byte("@echo off\r\nset VIRTUAL_ENV=C:\\x\r\n")); k != KindText {
		t.Errorf("activate.bat: %s, want %s", k, KindText)
	}
}

// Old-format uv launcher copied into a fake venv that "moved": the trailer path and its length
// field must change together, and nothing else in the file.
func TestRepairOldTrailerFixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "uv-trailer-0.8.23.exe"))
	s, end, ok := uvTrailer(b)
	if !ok {
		t.Fatal("fixture has no uv trailer")
	}
	oldPy := string(b[s:end]) // <old venv>\Scripts\python.exe
	oldVenv := filepath.Dir(filepath.Dir(oldPy))
	newVenv := filepath.Join(t.TempDir(), "a much longer folder name than before", ".venv")
	scripts := fakeVenvAt(t, newVenv, oldVenv)
	os.WriteFile(filepath.Join(scripts, "pyflakes.exe"), b, 0o644)

	r, err := Repair(newVenv, RepairOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := os.ReadFile(filepath.Join(scripts, "pyflakes.exe"))
	ns, nend, ok := uvTrailer(nb)
	want := filepath.Join(newVenv, "Scripts", "python.exe")
	if !ok || string(nb[ns:nend]) != want || !bytes.Equal(nb[:ns], b[:s]) {
		t.Fatalf("trailer not rewritten cleanly: ok=%v path=%q", ok, nb[ns:nend])
	}
	if len(r.Fixed) != 2 { // activate.bat + pyflakes.exe
		t.Errorf("fixed %d files, want 2: %+v", len(r.Fixed), r.Fixed)
	}
}

// A new-format (uv >= 0.9.9) launcher, built here by stamping UV_PYTHON_PATH on a real PE the way
// uv does, becomes relocatable: the resource is set to the relative python.exe.
func TestRepairUvRcdataSynthetic(t *testing.T) {
	oldVenv, newVenv := `C:\old place\.venv`, filepath.Join(t.TempDir(), "new place", ".venv")
	scripts := fakeVenvAt(t, newVenv, oldVenv)
	for name, cur := range map[string]string{"tool.exe": `C:\old place\.venv\Scripts\python.exe`, "gui.exe": `C:\old place\.venv\Scripts\pythonw.exe`} {
		p := filepath.Join(scripts, name)
		os.WriteFile(p, someRealPE(t), 0o644)
		if err := writeUvResource(p, cur); err != nil {
			t.Fatalf("stamping %s: %v", name, err)
		}
	}
	if _, err := Repair(newVenv, RepairOptions{Apply: true}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"tool.exe": "python.exe", "gui.exe": "pythonw.exe"} {
		if v, err := readUvResource(filepath.Join(scripts, name)); err != nil || v != want {
			t.Errorf("%s: UV_PYTHON_PATH = %q (%v), want %q", name, v, err, want)
		}
	}
}

// An unknown PE that mentions the old path is reported and left byte-for-byte untouched.
func TestRepairSkipsUnknownPE(t *testing.T) {
	oldVenv, newVenv := `C:\old\.venv`, filepath.Join(t.TempDir(), "renamed-longer", ".venv")
	scripts := fakeVenvAt(t, newVenv, oldVenv)
	p := filepath.Join(scripts, "mystery.exe")
	os.WriteFile(p, append(someRealPE(t), []byte(`C:\old\.venv\Scripts\python.exe`)...), 0o644)
	before := sum(t, p)
	r, err := Repair(newVenv, RepairOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum(t, p) != before {
		t.Fatal("unknown PE was modified")
	}
	if len(r.Skipped) != 1 || r.Skipped[0].Kind != KindOtherPE {
		t.Fatalf("skipped = %+v, want the unknown PE", r.Skipped)
	}
}

// End to end with the installed uv: create, install a console script, move to a path of a
// different length, repair, run the launchers. Exercises whichever launcher format this uv writes.
func TestRepairEndToEndCurrentUv(t *testing.T) {
	ver, v := uvVersion(t)
	root := t.TempDir()
	venv := filepath.Join(root, "v")
	uvVenv(t, venv, false)
	uvInstall(t, venv, "pyflakes")
	kind := func(name string) string {
		p := filepath.Join(venv, "Scripts", name)
		b, _ := os.ReadFile(p)
		return Classify(p, b)
	}
	format := kind("pyflakes.exe")
	t.Logf("%s writes %s launchers", ver, format)
	if (v[0] > 0 || v[1] > 9 || (v[1] == 9 && v[2] >= 9)) && format != KindUvRcdata {
		t.Errorf("uv %d.%d.%d should write %s launchers, got %s", v[0], v[1], v[2], KindUvRcdata, format)
	}

	moved := filepath.Join(root, "moved to a folder with a longer name", "v")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	if err := os.Rename(venv, moved); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, filepath.Join(moved, "Scripts", "pyflakes.exe"), "--version"); err == nil {
		t.Logf("note: launcher still ran before repair: %s", out)
	}
	r, err := Repair(moved, RepairOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Fixed {
		t.Logf("fixed %-10s %s: %s -> %s", f.Kind, filepath.Base(f.Path), f.From, f.To)
	}
	for _, exe := range []string{"pyflakes.exe", "pip.exe"} {
		if out, err := run(t, filepath.Join(moved, "Scripts", exe), "--version"); err != nil {
			t.Errorf("%s after repair: %v\n%s", exe, err, out)
		}
	}
	if len(r.Skipped) != 0 {
		t.Errorf("unexpected skips: %+v", r.Skipped)
	}
}

// R2: relocatable venvs are left alone; only pip-installed launchers with a now-missing
// absolute interpreter are fixed, relatively.
func TestRepairRelocatable(t *testing.T) {
	uvVersion(t)
	root := t.TempDir()
	venv := filepath.Join(root, "rel")
	uvVenv(t, venv, true)
	uvInstall(t, venv, "pyflakes")

	r, err := Repair(venv, RepairOptions{Apply: true})
	if err != nil || !r.Relocatable || len(r.Fixed) != 0 {
		t.Fatalf("unmoved relocatable venv: relocatable=%v fixed=%+v err=%v", r != nil && r.Relocatable, r, err)
	}
	bat, _ := os.ReadFile(filepath.Join(venv, "Scripts", "activate.bat"))

	moved := filepath.Join(root, "somewhere else entirely", "rel")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	if err := os.Rename(venv, moved); err != nil {
		t.Fatal(err)
	}
	r, err = Repair(moved, RepairOptions{Apply: true})
	if err != nil || !r.Relocatable || len(r.Fixed) != 0 {
		t.Fatalf("moved relocatable venv: fixed=%+v err=%v", r, err)
	}
	if after, _ := os.ReadFile(filepath.Join(moved, "Scripts", "activate.bat")); !bytes.Equal(after, bat) {
		t.Fatal("repair hard-coded a path into a relocatable venv's activate.bat")
	}
	if out, err := run(t, filepath.Join(moved, "Scripts", "pyflakes.exe"), "--version"); err != nil {
		t.Fatalf("relocatable uv launcher should survive the move: %v\n%s", err, out)
	}

	// Now pip writes an absolute-path launcher into it, and the venv moves again.
	pipReinstallPip(t, moved)
	again := filepath.Join(root, "and once more, longer", "rel")
	os.MkdirAll(filepath.Dir(again), 0o755)
	if err := os.Rename(moved, again); err != nil {
		t.Fatal(err)
	}
	r, err = Repair(again, RepairOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	fixedPip := false
	for _, f := range r.Fixed {
		t.Logf("fixed %-10s %s: %s -> %s", f.Kind, filepath.Base(f.Path), f.From, f.To)
		if strings.EqualFold(filepath.Base(f.Path), "pip.exe") && strings.HasPrefix(f.To, launcherDirToken) {
			fixedPip = true
		}
	}
	if !fixedPip {
		t.Errorf("pip's absolute launcher was not made relative: %+v", r.Fixed)
	}
	if out, err := run(t, filepath.Join(again, "Scripts", "pip.exe"), "--version"); err != nil {
		t.Errorf("pip.exe after relative repair: %v\n%s", err, out)
	}
}

// C1: create asks uv for a relocatable venv unless opted out.
func TestCreateCommandRelocatable(t *testing.T) {
	if _, ok := UvVersion(); !ok {
		t.Skip("uv not installed")
	}
	has := func(args []string) bool {
		for _, a := range args {
			if a == "--relocatable" {
				return true
			}
		}
		return false
	}
	on, _ := CreateCommand(`C:\x\.venv`, "", true)
	off, _ := CreateCommand(`C:\x\.venv`, "", false)
	if UvAtLeast(0, 2, 31) && !has(on) {
		t.Errorf("relocatable requested but missing: %v", on)
	}
	if has(off) {
		t.Errorf("--no-relocatable still passes --relocatable: %v", off)
	}
	if !UvAtLeast(0, 0, 1) || UvAtLeast(999, 0, 0) {
		t.Error("UvAtLeast comparison is wrong")
	}
}
