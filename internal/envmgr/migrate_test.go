package envmgr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateFromPygo runs the first-run migration against a temp state root: a fake pyGo
// registry and one real venv in pygo\envs, which must be moved and repaired end to end.
func TestMigrateFromPygo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VENVCTL_HOME", filepath.Join(root, "venvctl"))
	old, _ := LegacyDirs()
	if old != filepath.Join(root, "pygo") {
		t.Fatalf("legacy dir %s is not a sibling of VENVCTL_HOME", old)
	}
	src := filepath.Join(old, "envs", "small")
	mkvenv(t, src)
	local := filepath.Join(root, "proj", ".venv") // a non-central entry: must be left alone

	reg, _ := json.Marshal(Registry{Envs: []Entry{
		{Name: "small", Path: src, Central: true, Version: "3.x"},
		{Name: "proj", Path: local},
	}})
	os.WriteFile(filepath.Join(old, "registry.json"), reg, 0o644)
	os.WriteFile(filepath.Join(old, "config.json"), []byte(`{"scan_roots":["D:\\x"],"my_key":1}`), 0o644)

	if !NeedsMigration() {
		t.Fatal("NeedsMigration false before migrating")
	}
	m, err := MigrateFromPygo()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(CentralDir(), "small")
	if len(m.Moved) != 1 || m.Moved[0] != dst || len(m.LeftBehind)+len(m.RepairFailed) != 0 || len(m.Copied) != 2 {
		t.Fatalf("migration result: %+v", m)
	}
	// Registry points at the new place; the local entry is untouched.
	got := map[string]string{}
	for _, e := range LoadRegistry().Envs {
		got[e.Name] = e.Path
	}
	if got["small"] != dst || got["proj"] != local {
		t.Fatalf("registry after migration: %v", got)
	}
	// Repair ran: activate.bat names the new folder, and the moved interpreter reports it.
	bat, _ := os.ReadFile(filepath.Join(dst, "Scripts", "activate.bat"))
	if !strings.Contains(string(bat), dst) || strings.Contains(string(bat), src) {
		t.Fatalf("activate.bat not repaired:\n%s", bat)
	}
	out, err := exec.Command(filepath.Join(dst, "Scripts", "python.exe"), "-c", "import sys; print(sys.prefix)").Output()
	if err != nil || !SamePath(strings.TrimSpace(string(out)), dst) {
		t.Fatalf("moved venv python: %q, %v", out, err)
	}
	// Marker written, user keys kept, runs once, old folder left in place.
	cfg := LoadConfig()
	if cfg.MigratedFrom != "pygo" || cfg.MigratedAt == "" || len(cfg.ScanRoots) != 1 {
		t.Fatalf("config after migration: %+v", cfg)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(), "config.json")); !strings.Contains(string(b), "my_key") {
		t.Errorf("unknown config key dropped: %s", b)
	}
	if NeedsMigration() {
		t.Error("NeedsMigration still true after migrating")
	}
	if _, err := os.Stat(filepath.Join(old, "registry.json")); err != nil {
		t.Errorf("old pyGo folder was modified: %v", err)
	}
}

// mkvenv creates a real, small venv at dir with uv, or python -m venv --without-pip.
func mkvenv(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(dir), 0o755)
	var cmd *exec.Cmd
	if uv, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command(uv, "venv", "-q", dir)
	} else if py, err := exec.LookPath("python"); err == nil {
		cmd = exec.Command(py, "-m", "venv", "--without-pip", dir)
	} else {
		t.Skip("needs uv or python to create a real venv")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create venv: %v\n%s", err, out)
	}
}
