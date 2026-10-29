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

// VS Code's shellStartup activation writes its own block into $PROFILE; it must survive init.
func TestFilterProfileKeepsVSCodeBlock(t *testing.T) {
	vscode := strings.Join([]string{
		"#region vscode python",
		"# >>> vscode python",
		"if ($env:VSCODE_PYTHON_PWSH_ACTIVATE) { . $env:VSCODE_PYTHON_PWSH_ACTIVATE }",
		"# <<< vscode python",
		"#endregion",
	}, "\r\n")
	in := vscode + "\r\n# pyGo shell hook\r\n& 'C:\\Users\\me\\AppData\\Local\\pygo\\bin\\pygo.exe' init pwsh | Out-String | Invoke-Expression\r\n"
	r := filterProfile(in, own, "\r\n")
	if !strings.HasPrefix(r.Text, vscode+"\r\n") || r.Removed[genPyGo] != 1 || !r.Added {
		t.Fatalf("VS Code block not preserved:\n%q", r.Text)
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

// K1: the PATH saved on `on` must drop a foreign VIRTUAL_ENV's Scripts folder, in every shell,
// and the script must still render (no fmt verbs) and save only once.
func TestActivateStripsForeignVenvFromSavedPath(t *testing.T) {
	for sh, want := range map[string][]string{
		"pwsh": {`if (-not $env:_VENVCTL_OLD_PATH) {`, `$env:VIRTUAL_ENV.TrimEnd('\') + '\Scripts'`, `-ne $s`},
		"cmd":  {`if not defined _VENVCTL_OLD_PATH set "_VENVCTL_NEW=1"`, `%%_VENVCTL_OLD_PATH:;%VIRTUAL_ENV%\Scripts;=;%%`, `%%_VENVCTL_OLD_PATH:~1,-1%%`},
		"bash": {`if [ -z "${_VENVCTL_OLD_PATH:-}" ]; then`, `_s=":${VIRTUAL_ENV%/}/Scripts:"`, `${_VENVCTL_OLD_PATH//"$_s"/:}`},
	} {
		s := Activate(sh, `C:\proj\.venv`, "proj")
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s activate script lacks %q:\n%s", sh, w, s)
			}
		}
		if strings.Contains(s, "%!") {
			t.Errorf("%s activate script has an unrendered fmt verb:\n%s", sh, s)
		}
		if strings.Index(s, "VIRTUAL_ENV") > strings.Index(s, `C:\proj`) && sh != "bash" {
			t.Errorf("%s: PATH must be saved before VIRTUAL_ENV is overwritten", sh)
		}
	}
}

// The hook templates are fmt strings full of shell syntax; a stray % would render as %!x(...).
func TestAutoTemplatesRender(t *testing.T) {
	t.Setenv("VENVCTL_HOME", t.TempDir())
	exe := `C:\Users\O'Neil\AppData\Local\venvctl\bin\venvctl.exe`
	for name, s := range map[string]string{
		"pwsh":  AutoInit("pwsh", exe),
		"bash":  AutoInit("bash", exe),
		"clink": clinkLua(exe),
	} {
		if strings.Contains(s, "%!") || strings.Contains(s, "%[") {
			t.Errorf("%s template has an unrendered verb:\n%s", name, s)
		}
		if !strings.Contains(s, "trust.json") || !strings.Contains(s, "12") {
			t.Errorf("%s template is missing the trust file or depth", name)
		}
	}
	if !strings.Contains(AutoInit("pwsh", exe), "O''Neil") {
		t.Error("pwsh template does not escape ' in the exe path")
	}
	if !strings.Contains(AutoInit("bash", exe), `O'\''Neil`) {
		t.Error("bash template does not escape ' in the exe path")
	}
}
