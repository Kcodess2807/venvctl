package winapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

// Lock sources, shown in nuke's locker table.
const (
	SourceRestartManager = "Restart Manager"
	SourceFileLocksmith  = "File Locksmith"
)

// LocksmithCLI returns the PowerToys File Locksmith CLI on PATH (PowerToys 0.97+), or "".
func LocksmithCLI() string {
	// The extension-less name also finds a .cmd (PATHEXT), which the tests' fake CLI uses.
	for _, n := range []string{"PowerToys.FileLocksmith.CLI.exe", "PowerToys.FileLocksmith.CLI"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// LocksmithLockers asks File Locksmith which processes hold anything under dir. It scans every
// handle, so it sees what the Restart Manager cannot: open .py or log files, and folders a
// shell or Explorer window sits in. It runs `<cli> --json <dir>` only; never --kill (which
// terminates every locker unfiltered). The result goes through venvctl's own refusal list.
func LocksmithLockers(cli, dir string) ([]Locker, error) {
	out, err := exec.Command(cli, "--json", dir).Output()
	if len(bytes.TrimSpace(out)) == 0 {
		if err != nil {
			return nil, fmt.Errorf("File Locksmith: %w", err)
		}
		return nil, nil
	}
	var r struct {
		Processes []struct {
			PID   uint32   `json:"pid"`
			Name  string   `json:"name"`
			User  string   `json:"user"`
			Files []string `json:"files"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("File Locksmith output is not the expected JSON: %w", err)
	}
	table := processTable()
	var ls []Locker
	for _, p := range r.Processes {
		l := Locker{PID: p.PID, Name: p.Name, Exe: exePath(p.PID), OpenFiles: p.Files, Source: SourceFileLocksmith}
		if l.Exe == "" {
			l.Exe = p.Name // other user's or elevated process: no image path, keep the name
		}
		if e, ok := table[p.PID]; ok {
			l.Parent = table[e.ppid].name
		}
		ls = append(ls, l)
	}
	return ls, nil
}
