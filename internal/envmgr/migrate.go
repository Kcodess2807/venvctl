package envmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"venvctl/internal/discovery"
)

// LegacyDirs are the state folders of the two earlier generations: pyGo (%LOCALAPPDATA%\pygo)
// and WinVenv (%LOCALAPPDATA%\winvenv). With VENVCTL_HOME set they are its siblings, so tests
// can point the whole thing at a temp folder.
func LegacyDirs() (pygo, winvenv string) {
	base := os.Getenv("LOCALAPPDATA")
	if h := os.Getenv("VENVCTL_HOME"); h != "" {
		base = filepath.Dir(filepath.Clean(h))
	}
	return filepath.Join(base, "pygo"), filepath.Join(base, "winvenv")
}

// Migration reports what MigrateFromPygo did.
type Migration struct {
	From         string
	Copied       []string // state files copied into Dir()
	Moved        []string // central envs moved and repaired (new paths)
	LeftBehind   []string // "<old path>: <reason>", registry still points there
	RepairFailed []string // "<new path>: <reason>"
}

// NeedsMigration: venvctl has no registry yet and no migration marker, and pyGo's folder has one.
func NeedsMigration() bool {
	if FileExists(filepath.Join(Dir(), "registry.json")) || LoadConfig().MigratedFrom != "" {
		return false
	}
	old, _ := LegacyDirs()
	return FileExists(filepath.Join(old, "registry.json"))
}

// MigrateFromPygo copies pyGo's registry.json and config.json, moves each central env from
// pygo\envs to venvctl\envs (rename, so it is atomic on one volume) and repairs it, points the
// registry at the new locations, and writes a migrated_from marker so it runs once.
// It never deletes anything in the old folder.
func MigrateFromPygo() (*Migration, error) {
	old, _ := LegacyDirs()
	m := &Migration{From: old}
	if err := os.MkdirAll(CentralDir(), 0o755); err != nil {
		return m, err
	}
	for _, f := range []string{"registry.json", "config.json"} {
		src := filepath.Join(old, f)
		if !FileExists(src) {
			continue
		}
		if err := copyFile(src, filepath.Join(Dir(), f)); err != nil {
			return m, fmt.Errorf("copy %s: %w", src, err)
		}
		m.Copied = append(m.Copied, f)
	}
	reg := LoadRegistry()
	oldEnvs := filepath.Join(old, "envs")
	ents, _ := os.ReadDir(oldEnvs)
	for _, d := range ents {
		if !d.IsDir() || discovery.IsTrash(d.Name()) {
			continue
		}
		src, dst := filepath.Join(oldEnvs, d.Name()), filepath.Join(CentralDir(), d.Name())
		if FileExists(dst) {
			m.LeftBehind = append(m.LeftBehind, src+": "+dst+" already exists")
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			m.LeftBehind = append(m.LeftBehind, fmt.Sprintf("%s: %v", src, err))
			continue
		}
		m.Moved = append(m.Moved, dst)
		if _, err := Repair(dst, RepairOptions{Apply: true}); err != nil {
			m.RepairFailed = append(m.RepairFailed, fmt.Sprintf("%s: %v", dst, err))
		}
		for i := range reg.Envs {
			if SamePath(reg.Envs[i].Path, src) {
				reg.Envs[i].Path, reg.Envs[i].Central = dst, true
			}
		}
	}
	if err := reg.Save(); err != nil {
		return m, err
	}
	if err := SetConfig("migrated_from", "pygo"); err != nil {
		return m, err
	}
	return m, SetConfig("migrated_at", time.Now().Format(time.RFC3339))
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src) // registry.json / config.json: small
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// FileExists reports whether p exists (file or folder).
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// SweepTrash deletes leftover trash folders next to dir from earlier interrupted deletes:
// <dir>.<n>.venvctl-trash, and the pyGo/WinVenv forms .pygo-trash and .wv-trash.
// It returns the folders removed and the bytes they held.
func SweepTrash(dir string) (removed []string, freed int64) {
	parent, base := filepath.Dir(dir), strings.ToLower(filepath.Base(dir))+"."
	ents, _ := os.ReadDir(parent)
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if !e.IsDir() || !strings.HasPrefix(n, base) || !discovery.IsTrash(n) {
			continue
		}
		p := filepath.Join(parent, e.Name())
		size := Size(p)
		if os.RemoveAll(p) == nil {
			removed, freed = append(removed, p), freed+size
		}
	}
	return removed, freed
}
