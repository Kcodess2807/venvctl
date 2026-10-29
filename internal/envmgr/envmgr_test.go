package envmgr

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRewritePath(t *testing.T) {
	old, new := `C:\old\.venv`, `D:\proj\renamed\.venv`

	// distlib (pip) launcher: shebang before a zip; plain byte replace.
	pip := []byte("MZ...stub...#!" + old + `\Scripts\python.exe` + "\r\nPK\x03\x04zip")
	got := RewritePath(pip, old, new)
	if !bytes.Contains(got, []byte("#!"+new+`\Scripts\python.exe`)) || bytes.Contains(got, []byte(old)) {
		t.Fatalf("pip launcher not rewritten: %q", got)
	}

	// uv trampoline: <path><u32 len>UVUV; length must be fixed up.
	path := []byte(old + `\Scripts\python.exe`)
	uv := append([]byte("MZ...stub..."), path...)
	uv = binary.LittleEndian.AppendUint32(uv, uint32(len(path)))
	uv = append(uv, "UVSC"...) // what current uv writes for console scripts
	got = RewritePath(uv, old, new)
	n := len(got)
	wantPath := []byte(new + `\Scripts\python.exe`)
	l := int(binary.LittleEndian.Uint32(got[n-8 : n-4]))
	if l != len(wantPath) || !bytes.Equal(got[n-8-l:n-8], wantPath) || string(got[n-4:]) != "UVSC" {
		t.Fatalf("uv trampoline trailer wrong: len=%d tail=%q", l, got[n-8-l:])
	}

	// activate (bash) uses forward slashes.
	sh := []byte("VIRTUAL_ENV='C:/old/.venv'")
	if got := RewritePath(sh, old, new); string(got) != "VIRTUAL_ENV='D:/proj/renamed/.venv'" {
		t.Fatalf("slash form not rewritten: %q", got)
	}
}
