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

	// uv trampoline: <path><u32 len>UVSC; length must be fixed up.
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

func TestRewritePathBoundary(t *testing.T) {
	old, new := `C:\x\proj`, `D:\moved\proj`
	in := `set "VIRTUAL_ENV=C:\x\proj"` + "\r\n" + // quote boundary
		`#!C:\x\proj\Scripts\python.exe` + "\r\n" + // backslash boundary
		`C:\x\proj2\Scripts\python.exe` + "\n" + // proj2: must not change
		`PATH=C:\x\proj;C:\x\projects` + "\n" + // semicolon boundary; projects untouched
		`home = C:\x\proj` + "\n" + // end of line
		`VIRTUAL_ENV='C:/x/proj' (C:/x/proj) C:/x/proj` // slash form: quote, paren, end of data
	want := `set "VIRTUAL_ENV=D:\moved\proj"` + "\r\n" +
		`#!D:\moved\proj\Scripts\python.exe` + "\r\n" +
		`C:\x\proj2\Scripts\python.exe` + "\n" +
		`PATH=D:\moved\proj;C:\x\projects` + "\n" +
		`home = D:\moved\proj` + "\n" +
		`VIRTUAL_ENV='D:/moved/proj' (D:/moved/proj) D:/moved/proj`
	if got := string(RewritePath([]byte(in), old, new)); got != want {
		t.Fatalf("boundary rewrite wrong\n got: %q\nwant: %q", got, want)
	}

	// uv trailer holding the over-match case must stay byte-identical.
	path := []byte(`C:\x\proj2\Scripts\python.exe`)
	uv := binary.LittleEndian.AppendUint32(append([]byte("MZ"), path...), uint32(len(path)))
	uv = append(uv, "UVSC"...)
	if got := RewritePath(uv, old, new); !bytes.Equal(got, uv) {
		t.Fatalf("uv trailer for proj2 was rewritten: %q", got)
	}
}
