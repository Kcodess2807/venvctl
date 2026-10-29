package shell

import (
	"bytes"
	"strings"
	"testing"
)

const own = `& 'C:\Users\me\AppData\Local\venvctl\bin\venvctl.exe' init pwsh | Out-String | Invoke-Expression`

func TestFilterProfileOnlyPyGo(t *testing.T) {
	in := "# pyGo shell hook\r\n& 'C:\\Users\\me\\AppData\\Local\\pygo\\bin\\pygo.exe' init pwsh | Out-String | Invoke-Expression\r\n"
	r := filterProfile(in, own, "\r\n")
	want := "# venvctl shell hook\r\n" + own + "\r\n"
	if r.Text != want || r.Removed[genPyGo] != 1 || !r.Added {
		t.Fatalf("got %q removed=%v added=%v", r.Text, r.Removed, r.Added)
	}
}

func TestFilterProfileMixed(t *testing.T) {
	in := strings.Join([]string{
		"Import-Module posh-git",
		"# WinVenv shell hook",
		"& 'C:\\Users\\me\\AppData\\Local\\winvenv\\bin\\wv.exe' init pwsh | Out-String | Invoke-Expression",
		"$env:EDITOR = 'code'",
		"# pyGo shell hook",
		"& 'C:\\Users\\me\\AppData\\Local\\pygo\\bin\\pygo.exe' init pwsh | Out-String | Invoke-Expression",
		"& 'D:\\PROJECTS\\pygo\\pygo.exe' init pwsh | Out-String | Invoke-Expression", // no comment, still pyGo
		"",
		"Set-Alias ll Get-ChildItem",
		"",
	}, "\n")
	r := filterProfile(in, own, "\n")
	want := strings.Join([]string{
		"Import-Module posh-git",
		"$env:EDITOR = 'code'",
		"",
		"Set-Alias ll Get-ChildItem",
		"# venvctl shell hook",
		own,
		"",
	}, "\n")
	if r.Text != want {
		t.Fatalf("text\n got: %q\nwant: %q", r.Text, want)
	}
	if r.Removed[genWinVenv] != 1 || r.Removed[genPyGo] != 2 || !r.Added {
		t.Fatalf("removed=%v added=%v", r.Removed, r.Added)
	}
	got := Change{Shell: "PowerShell 5.1", Removed: r.Removed, Added: true}.String()
	if got != "PowerShell 5.1: removed 3 legacy lines (WinVenv, pyGo), added venvctl hook" {
		t.Errorf("report: %q", got)
	}
}

func TestFilterProfileIdempotentAndStale(t *testing.T) {
	once := filterProfile("Set-Alias ll Get-ChildItem\r\n", own, "\r\n")
	twice := filterProfile(once.Text, own, "\r\n")
	if twice.Text != once.Text || twice.Added || !twice.Kept || len(twice.Removed) != 0 {
		t.Fatalf("second run changed things: %q removed=%v", twice.Text, twice.Removed)
	}
	stale := "# venvctl shell hook\r\n& 'D:\\old\\venvctl.exe' init pwsh | Out-String | Invoke-Expression\r\nSet-Alias ll Get-ChildItem\r\n"
	r := filterProfile(stale, own, "\r\n")
	if r.Removed[genStale] != 1 || !r.Added || !strings.HasPrefix(r.Text, "Set-Alias ll Get-ChildItem\r\n") {
		t.Fatalf("stale: %q removed=%v", r.Text, r.Removed)
	}
	// A user line right after our comment is kept (only hook lines after our comment go).
	r = filterProfile("# venvctl shell hook\r\nSet-Alias ll Get-ChildItem\r\n", own, "\r\n")
	if !strings.Contains(r.Text, "Set-Alias ll Get-ChildItem") {
		t.Fatalf("user line after our comment was removed: %q", r.Text)
	}
}

func TestFilterProfileRemove(t *testing.T) {
	in := "Set-Alias ll Get-ChildItem\n# venvctl shell hook\n" + own + "\n# pyGo shell hook\neval \"$('/c/Users/me/AppData/Local/pygo/bin/pygo.exe' init bash)\"\n"
	r := filterProfile(in, "", "\n")
	if r.Text != "Set-Alias ll Get-ChildItem\n" || r.Removed[genOwn] != 1 || r.Removed[genPyGo] != 1 || r.Added {
		t.Fatalf("remove: %q removed=%v", r.Text, r.Removed)
	}
}

func TestFilterAutoRun(t *testing.T) {
	vscode := `if exist "C:\Users\me\AppData\Roaming\Code\User\globalStorage\ms-python.python\.cmdrc\vscode-python.bat" call "C:\Users\me\AppData\Roaming\Code\User\globalStorage\ms-python.python\.cmdrc\vscode-python.bat"`
	pygo := `if exist "C:\Users\me\AppData\Local\pygo\pygo_hook.cmd" call "C:\Users\me\AppData\Local\pygo\pygo_hook.cmd"`
	wv := `if exist "C:\Users\me\AppData\Local\winvenv\wv_hook.cmd" call "C:\Users\me\AppData\Local\winvenv\wv_hook.cmd"`
	mine := `if exist "C:\Users\me\AppData\Local\venvctl\venvctl_hook.cmd" call "C:\Users\me\AppData\Local\venvctl\venvctl_hook.cmd"`

	r := filterAutoRun(vscode+" & "+pygo, mine)
	if r.Text != vscode+" & "+mine || r.Removed[genPyGo] != 1 || !r.Added {
		t.Fatalf("vscode+pygo: %q removed=%v", r.Text, r.Removed)
	}
	r = filterAutoRun(wv, mine)
	if r.Text != mine || r.Removed[genWinVenv] != 1 {
		t.Fatalf("only wv: %q removed=%v", r.Text, r.Removed)
	}
	r = filterAutoRun(mine+" & "+vscode, mine) // ours kept in place, idempotent
	if r.Text != mine+" & "+vscode || r.Added || !r.Kept {
		t.Fatalf("ours first: %q", r.Text)
	}
	r = filterAutoRun(vscode+" & "+mine, "") // --remove
	if r.Text != vscode || r.Removed[genOwn] != 1 {
		t.Fatalf("remove: %q removed=%v", r.Text, r.Removed)
	}
	if r := filterAutoRun(wv+" & "+pygo, ""); r.Text != "" {
		t.Fatalf("remove legacy only: %q", r.Text)
	}
}

func TestEncodingRoundTrip(t *testing.T) {
	for _, enc := range []string{"utf8", "utf8bom", "utf16le", "utf16be"} {
		b := encode("Set-Alias ll Get-ChildItem\r\n# ünïcødé\r\n", enc)
		text, got := decode(b)
		if got != enc || !bytes.Equal(encode(text, got), b) {
			t.Errorf("%s: decoded as %s, round trip differs", enc, got)
		}
	}
}
