// Package winapi wraps the Win32 Restart Manager (rstrtmgr.dll) to find processes
// holding locks on files, plus App Execution Alias (Store stub) detection.
package winapi

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	rstrtmgr                = windows.NewLazySystemDLL("rstrtmgr.dll")
	procRmStartSession      = rstrtmgr.NewProc("RmStartSession")
	procRmRegisterResources = rstrtmgr.NewProc("RmRegisterResources")
	procRmGetList           = rstrtmgr.NewProc("RmGetList")
	procRmEndSession        = rstrtmgr.NewProc("RmEndSession")
)

const (
	cchRmSessionKey = 32
	cchRmMaxAppName = 255
	cchRmMaxSvcName = 63
	errorMoreData   = 234
	// IO_REPARSE_TAG_APPEXECLINK marks the 0-byte stubs in %LOCALAPPDATA%\Microsoft\WindowsApps.
	ioReparseTagAppExecLink = 0x8000001B
)

type rmUniqueProcess struct {
	ProcessID        uint32
	ProcessStartTime windows.Filetime
}

type rmProcessInfo struct {
	Process          rmUniqueProcess
	AppName          [cchRmMaxAppName + 1]uint16
	ServiceShortName [cchRmMaxSvcName + 1]uint16
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

// Locker is a process holding an open handle on a file.
type Locker struct {
	PID    uint32
	Name   string
	Exe    string
	Parent string // exe name of the parent process, e.g. "Code.exe"
	// OpenFiles are the venv files the process has mapped (its exe, .dll and .pyd modules under
	// the venv), from a module snapshot. Empty when the snapshot is denied.
	OpenFiles []string
	Source    string // SourceRestartManager or SourceFileLocksmith
}

// modulesUnder lists pid's loaded modules (32- and 64-bit) whose path is inside dir.
func modulesUnder(pid uint32, dir string) []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	prefix := strings.ToLower(filepath.Clean(dir)) + `\`
	var out []string
	var me windows.ModuleEntry32
	me.Size = uint32(unsafe.Sizeof(me))
	for err = windows.Module32First(snap, &me); err == nil; err = windows.Module32Next(snap, &me) {
		if p := windows.UTF16ToString(me.ExePath[:]); strings.HasPrefix(strings.ToLower(p), prefix) {
			out = append(out, p)
		}
	}
	return out
}

// LockingProcesses returns processes holding handles on .exe/.dll/.pyd files under dir.
func LockingProcesses(dir string) ([]Locker, error) {
	var files []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".exe", ".dll", ".pyd":
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return nil, nil
	}
	// ponytail: python.exe is the lock 95% of the time; register everything anyway, RM handles thousands fine.

	var session uint32
	var key [cchRmSessionKey + 1]uint16
	if r, _, _ := procRmStartSession.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0]))); r != 0 {
		return nil, syscall.Errno(r)
	}
	defer procRmEndSession.Call(uintptr(session))

	ptrs := make([]*uint16, len(files))
	for i, f := range files {
		ptrs[i], _ = windows.UTF16PtrFromString(f)
	}
	if r, _, _ := procRmRegisterResources.Call(uintptr(session), uintptr(len(ptrs)), uintptr(unsafe.Pointer(&ptrs[0])), 0, 0, 0, 0); r != 0 {
		return nil, syscall.Errno(r)
	}

	// Two-call dance; the lock list can grow between calls, so retry on ERROR_MORE_DATA.
	var infos []rmProcessInfo
	var needed, count, reason uint32
	for attempt := 0; ; attempt++ {
		var buf uintptr
		count = uint32(len(infos))
		if count > 0 {
			buf = uintptr(unsafe.Pointer(&infos[0]))
		}
		r, _, _ := procRmGetList.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), buf, uintptr(unsafe.Pointer(&reason)))
		if r == 0 {
			break
		}
		if r != errorMoreData || attempt == 5 {
			return nil, syscall.Errno(r)
		}
		infos = make([]rmProcessInfo, needed+4)
	}
	parents := processTable()
	out := make([]Locker, 0, count)
	for _, pi := range infos[:count] {
		pid := pi.Process.ProcessID
		l := Locker{PID: pid, Name: windows.UTF16ToString(pi.AppName[:]), Exe: exePath(pid), OpenFiles: modulesUnder(pid, dir), Source: SourceRestartManager}
		if p, ok := parents[pid]; ok {
			l.Parent = parents[p.ppid].name
		}
		out = append(out, l)
	}
	return out, nil
}

type procEntry struct {
	ppid uint32
	name string
}

// processTable maps PID to parent PID and exe name via a Toolhelp snapshot.
func processTable() map[uint32]procEntry {
	m := map[uint32]procEntry{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return m
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		m[pe.ProcessID] = procEntry{ppid: pe.ParentProcessID, name: windows.UTF16ToString(pe.ExeFile[:])}
	}
	return m
}

// protected are editors and shells venvctl never terminates; the user closes them.
var protected = map[string]bool{
	"code.exe": true, "code - insiders.exe": true, "cursor.exe": true, "pycharm64.exe": true,
	"idea64.exe": true, "devenv.exe": true, "explorer.exe": true, "windowsterminal.exe": true,
	"pwsh.exe": true, "powershell.exe": true, "cmd.exe": true, "bash.exe": true,
}

// Protected reports whether venvctl must not kill this locker.
func (l Locker) Protected() bool {
	return protected[strings.ToLower(filepath.Base(l.Exe))] || protected[strings.ToLower(l.Name)]
}

func exePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// Kill terminates a process by PID.
func Kill(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
