package winapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os/exec"
	"unicode/utf16"
)

// Lock sources, shown in nuke's locker table.
const (
	SourceRestartManager = "Restart Manager"
	SourceFileLocksmith  = "File Locksmith"
)

// LocksmithCLI returns the PowerToys File Locksmith CLI on PATH (PowerToys 0.97+), or "".
func LocksmithCLI() string {
	for _, n := range []string{"PowerToys.FileLocksmith.CLI.exe", "PowerToys.FileLocksmith.CLI", "FileLocksmithCLI.exe"} {
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
	if err := json.Unmarshal(decodeText(out), &r); err != nil {
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

// decodeText turns UTF-16LE output (with or without BOM) into UTF-8; UTF-8 passes through.
func decodeText(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	le := bytes.HasPrefix(b, []byte{0xFF, 0xFE})
	if !le && !(len(b) >= 2 && b[1] == 0 && b[0] != 0) {
		return b
	}
	if le {
		b = b[2:]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return []byte(string(utf16.Decode(u)))
}
