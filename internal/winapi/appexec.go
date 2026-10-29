package winapi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// AppExecLink is what an App Execution Alias (a 0-byte exe in %LOCALAPPDATA%\Microsoft\WindowsApps)
// points at.
type AppExecLink struct {
	Package string // package family name, e.g. PythonSoftwareFoundation.PythonManager_3847v3x7pw1km
	AppID   string // app user model ID
	Target  string // the real exe the alias starts
}

// ReadAppExecLink reads the IO_REPARSE_TAG_APPEXECLINK data of an alias file.
func ReadAppExecLink(path string) (AppExecLink, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return AppExecLink{}, err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return AppExecLink{}, err
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0, &buf[0], uint32(len(buf)), &n, nil); err != nil {
		return AppExecLink{}, err
	}
	return ParseAppExecLink(buf[:n])
}

// ParseAppExecLink parses a REPARSE_DATA_BUFFER holding an App Execution Alias:
// ULONG tag (0x8000001B), USHORT data length, USHORT reserved, then ULONG version (3) followed by
// NUL-terminated UTF-16 strings: package family name, app user model ID, target exe path (newer
// Windows builds append an app type string, which is ignored).
func ParseAppExecLink(b []byte) (AppExecLink, error) {
	if len(b) < 12 {
		return AppExecLink{}, errors.New("reparse buffer too short")
	}
	if tag := binary.LittleEndian.Uint32(b); tag != ioReparseTagAppExecLink {
		return AppExecLink{}, fmt.Errorf("not an app execution alias (reparse tag %#x)", tag)
	}
	data := b[8:]
	if l := int(binary.LittleEndian.Uint16(b[4:])); l < len(data) {
		data = data[:l]
	}
	if v := binary.LittleEndian.Uint32(data); v != 3 {
		return AppExecLink{}, fmt.Errorf("unsupported app execution alias version %d", v)
	}
	var strs []string
	u := make([]uint16, 0, (len(data)-4)/2)
	for i := 4; i+1 < len(data); i += 2 {
		c := binary.LittleEndian.Uint16(data[i:])
		if c == 0 {
			strs = append(strs, string(utf16.Decode(u)))
			u = u[:0]
			continue
		}
		u = append(u, c)
	}
	if len(strs) < 3 {
		return AppExecLink{}, fmt.Errorf("app execution alias has %d strings, want 3", len(strs))
	}
	return AppExecLink{Package: strs[0], AppID: strs[1], Target: strs[2]}, nil
}
