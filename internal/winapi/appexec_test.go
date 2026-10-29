package winapi

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// reparse builds an IO_REPARSE_TAG_APPEXECLINK buffer the way NTFS returns it.
func reparse(strs ...string) []byte {
	data := binary.LittleEndian.AppendUint32(nil, 3) // version
	for _, s := range strs {
		for _, c := range utf16.Encode([]rune(s)) {
			data = binary.LittleEndian.AppendUint16(data, c)
		}
		data = binary.LittleEndian.AppendUint16(data, 0)
	}
	b := binary.LittleEndian.AppendUint32(nil, ioReparseTagAppExecLink)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(data)))
	b = binary.LittleEndian.AppendUint16(b, 0)
	return append(b, data...)
}

func TestParseAppExecLink(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
		want AppExecLink
	}{
		{"store stub", reparse("Microsoft.DesktopAppInstaller_8wekyb3d8bbwe",
			"Microsoft.DesktopAppInstaller_8wekyb3d8bbwe!PythonRedirector",
			`C:\Program Files\WindowsApps\Microsoft.DesktopAppInstaller_1.0_x64__8wekyb3d8bbwe\AppInstallerPythonRedirector.exe`, "0"),
			AppExecLink{"Microsoft.DesktopAppInstaller_8wekyb3d8bbwe", "Microsoft.DesktopAppInstaller_8wekyb3d8bbwe!PythonRedirector",
				`C:\Program Files\WindowsApps\Microsoft.DesktopAppInstaller_1.0_x64__8wekyb3d8bbwe\AppInstallerPythonRedirector.exe`}},
		{"pymanager", reparse("PythonSoftwareFoundation.PythonManager_3847v3x7pw1km",
			"PythonSoftwareFoundation.PythonManager_3847v3x7pw1km!Python",
			`C:\Program Files\WindowsApps\PythonSoftwareFoundation.PythonManager_26.4.0.0_x64__3847v3x7pw1km\python.exe`),
			AppExecLink{"PythonSoftwareFoundation.PythonManager_3847v3x7pw1km", "PythonSoftwareFoundation.PythonManager_3847v3x7pw1km!Python",
				`C:\Program Files\WindowsApps\PythonSoftwareFoundation.PythonManager_26.4.0.0_x64__3847v3x7pw1km\python.exe`}},
		{"store cpython", reparse("PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0",
			"PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0!Python",
			`C:\Program Files\WindowsApps\PythonSoftwareFoundation.Python.3.12_3.12.2032.0_x64__qbz5n2kfra8p0\python3.12.exe`),
			AppExecLink{"PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0", "PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0!Python",
				`C:\Program Files\WindowsApps\PythonSoftwareFoundation.Python.3.12_3.12.2032.0_x64__qbz5n2kfra8p0\python3.12.exe`}},
	}
	for _, c := range cases {
		got, err := ParseAppExecLink(c.buf)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v", c.name, got, err)
		}
	}
	if _, err := ParseAppExecLink(reparse("only", "two")); err == nil {
		t.Error("accepted a buffer with two strings")
	}
	bad := reparse("a", "b", "c")
	binary.LittleEndian.PutUint32(bad, 0xA000000C) // symlink tag
	if _, err := ParseAppExecLink(bad); err == nil {
		t.Error("accepted a non-AppExecLink tag")
	}
}

// Reads this machine's real alias, if any, so the package prefixes can be checked by eye (-v).
func TestReadAppExecLinkThisMachine(t *testing.T) {
	for _, n := range []string{"python.exe", "python3.exe", "pymanager.exe", "py.exe"} {
		p := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", n)
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		l, err := ReadAppExecLink(p)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		t.Logf("%s: package=%s target=%s", n, l.Package, l.Target)
	}
}
