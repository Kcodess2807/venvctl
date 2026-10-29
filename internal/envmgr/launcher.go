package envmgr

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// File kinds found in a venv's Scripts\ folder. Only the launcher kinds below have a known
// structure; repair never edits any other PE file.
const (
	KindUvRcdata  = "uv-rcdata"  // uv >= 0.9.9: interpreter path in RT_RCDATA "UV_PYTHON_PATH"
	KindUvTrailer = "uv-trailer" // uv < 0.9.9: <path><u32 len><UVSC|UVPY|UVUV> at the end
	KindDistlib   = "distlib"    // pip/distlib: PE stub, "#!<python>\r\n", then a zip
	KindText      = "text"       // activate, activate.bat, activate.fish, ...
	KindOtherPE   = "other-pe"   // any other PE (python.exe copies, unknown launchers)
	KindBinary    = "binary"     // non-PE binary data
)

const uvPathResource = "UV_PYTHON_PATH"

// Classify reports the kind of a Scripts\ file whose bytes are b.
func Classify(path string, b []byte) string {
	if isPE(b) {
		if _, err := readUvResource(path); err == nil {
			return KindUvRcdata
		}
		if _, _, ok := uvTrailer(b); ok {
			return KindUvTrailer
		}
		if _, _, ok := shebangSpan(b); ok {
			return KindDistlib
		}
		return KindOtherPE
	}
	if bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
		return KindBinary
	}
	return KindText
}

func isPE(b []byte) bool {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return false
	}
	off := int(binary.LittleEndian.Uint32(b[0x3c:]))
	return off > 0 && off+4 <= len(b) && string(b[off:off+4]) == "PE\x00\x00"
}

// isSigned reports whether a PE file carries an Authenticode signature (a certificate table).
func isSigned(b []byte) bool {
	f, err := pe.NewFile(bytes.NewReader(b))
	if err != nil {
		return false
	}
	defer f.Close()
	const security = 4 // IMAGE_DIRECTORY_ENTRY_SECURITY
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return len(h.DataDirectory) > security && h.DataDirectory[security].Size > 0
	case *pe.OptionalHeader32:
		return len(h.DataDirectory) > security && h.DataDirectory[security].Size > 0
	}
	return false
}

// readUvResource returns the UV_PYTHON_PATH resource of a uv >= 0.9.9 launcher. It loads the
// file as a data/resource image only, so nothing in it runs, and releases it before returning.
func readUvResource(path string) (string, error) {
	h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_AS_DATAFILE|windows.LOAD_LIBRARY_AS_IMAGE_RESOURCE)
	if err != nil {
		return "", err
	}
	defer windows.FreeLibrary(h)
	res, err := windows.FindResource(h, uvPathResource, windows.RT_RCDATA)
	if err != nil {
		return "", err
	}
	data, err := windows.LoadResourceData(h, res)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(data, "\x00")), nil // copy out before FreeLibrary
}

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procBeginUpdateResource = kernel32.NewProc("BeginUpdateResourceW")
	procUpdateResource      = kernel32.NewProc("UpdateResourceW")
	procEndUpdateResource   = kernel32.NewProc("EndUpdateResourceW")
)

// writeUvResource replaces UV_PYTHON_PATH (RT_RCDATA, language neutral, raw UTF-8 like uv
// writes it). EndUpdateResource is retried with backoff because antivirus often holds the file.
func writeUvResource(path, value string) error {
	return retry(func() error {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		name, _ := windows.UTF16PtrFromString(uvPathResource)
		h, _, e := procBeginUpdateResource.Call(uintptr(unsafe.Pointer(p)), 0)
		if h == 0 {
			return fmt.Errorf("BeginUpdateResource: %w", e)
		}
		data := []byte(value)
		r, _, e := procUpdateResource.Call(h, uintptr(windows.RT_RCDATA), uintptr(unsafe.Pointer(name)), 0,
			uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
		if r == 0 {
			procEndUpdateResource.Call(h, 1) // discard
			return fmt.Errorf("UpdateResource: %w", e)
		}
		if r, _, e := procEndUpdateResource.Call(h, 0); r == 0 {
			return fmt.Errorf("EndUpdateResource: %w", e)
		}
		return nil
	})
}

// uvTrailer finds the interpreter path of an old uv launcher: <path><u32 LE len><magic>.
func uvTrailer(b []byte) (start, end int, ok bool) {
	n := len(b)
	if n <= 8 || !isUvMagic(b[n-4:]) {
		return 0, 0, false
	}
	l := int(binary.LittleEndian.Uint32(b[n-8 : n-4]))
	if start = n - 8 - l; start < 0 || l == 0 {
		return 0, 0, false
	}
	return start, n - 8, true
}

func isUvMagic(m []byte) bool {
	s := string(m)
	return s == "UVSC" || s == "UVPY" || s == "UVUV"
}

// shebangSpan finds the "#!..." line of a distlib launcher: it must end right before the zip
// that follows the PE stub ("\nPK\x03\x04"), and hold no NUL bytes. start..end excludes the
// line ending.
func shebangSpan(b []byte) (start, end int, ok bool) {
	z := bytes.Index(b, []byte("\nPK\x03\x04"))
	if z < 0 {
		return 0, 0, false
	}
	s := bytes.LastIndex(b[:z], []byte("#!"))
	if s < 0 || z-s > 32<<10 || bytes.IndexByte(b[s:z], 0) >= 0 {
		return 0, 0, false
	}
	end = z
	if end > s && b[end-1] == '\r' {
		end--
	}
	return s, end, true
}

// splitShebang splits "#!<interp> <args>" into the interpreter (quotes removed) and the rest.
func splitShebang(line string) (interp, rest string) {
	line = strings.TrimPrefix(line, "#!")
	if strings.HasPrefix(line, `"`) {
		if i := strings.Index(line[1:], `"`); i >= 0 {
			return line[1 : i+1], line[i+2:]
		}
	}
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], line[i:]
	}
	return line, ""
}

// launcherDirToken is distlib's placeholder for "the folder this launcher is in" (distlib >= 0.3.0).
const launcherDirToken = "<launcher_dir>"

// relativeInterp is the relocatable form of an interpreter path: python.exe or pythonw.exe,
// resolved by uv's trampoline against its own folder.
func relativeInterp(cur string) string {
	if strings.HasSuffix(strings.ToLower(cur), "pythonw.exe") {
		return "pythonw.exe"
	}
	return "python.exe"
}

// UnderDir reports whether p is dir itself or inside it (case-insensitive, path boundary).
func UnderDir(p, dir string) bool {
	p, dir = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(dir))
	return p == dir || strings.HasPrefix(p, dir+`\`)
}
