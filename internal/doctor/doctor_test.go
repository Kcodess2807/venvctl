package doctor

import (
	"strings"
	"testing"

	"venvctl/internal/winapi"
)

func TestCovers(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\me`)
	dir := `C:\Users\me\AppData\Local\venvctl\envs`
	cases := map[string]bool{
		`{"python.venvFolders": ["AppData\\Local\\venvctl\\envs"]}`:                                                   true, // relative to home
		`{"python.venvFolders": ["C:\\Users\\me\\AppData\\Local\\venvctl\\envs"]}`:                                    true,
		`{"python-envs.globalSearchPaths": ["${userHome}/AppData/Local/venvctl/**"]}`:                                 true, // ** covers below
		`{"python-envs.workspaceSearchPaths": ["./**/.venv"]}`:                                                        false,
		`{"python.venvFolders": ["envs"]}`:                                                                            false,
		"{\n  // comment, with \"quotes\"\n  \"python.venvFolders\": [\"~/AppData/Local/venvctl/envs\",], /* x */\n}": true,
		`not json`: false,
	}
	for in, want := range cases {
		if got := covers([]byte(in), dir); got != want {
			t.Errorf("covers(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	in := `{"url": "http://x//y", "a": "/* not a comment */"} // tail`
	want := `{"url": "http://x//y", "a": "/* not a comment */"} ` + "\n"
	if got := string(stripJSONC([]byte(in))); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyLink(t *testing.T) {
	cases := map[string]winapi.AppExecLink{
		"stub": {Package: "Microsoft.DesktopAppInstaller_8wekyb3d8bbwe",
			Target: `C:\Program Files\WindowsApps\Microsoft.DesktopAppInstaller_1.29.380.0_x64__8wekyb3d8bbwe\AppInstallerPythonRedirector.exe`},
		"python": {Package: "PythonSoftwareFoundation.PythonManager_3847v3x7pw1km",
			Target: `C:\Program Files\WindowsApps\PythonSoftwareFoundation.PythonManager_26.4.0.0_x64__3847v3x7pw1km\python.exe`},
		"unknown": {Package: "Contoso.PythonThing_abc", Target: `C:\x\python.exe`},
	}
	for want, l := range cases {
		if got, _ := classifyLink(l); got != want {
			t.Errorf("%s: got %s, want %s", l.Package, got, want)
		}
	}
	store := winapi.AppExecLink{Package: "PythonSoftwareFoundation.Python.3.12_qbz5n2kfra8p0", Target: `C:\...\python3.12.exe`}
	if got, label := classifyLink(store); got != "python" || !strings.Contains(label, "Store CPython") {
		t.Errorf("Store CPython: %s %s", got, label)
	}
	// A stub is recognised by its redirector target even under an unexpected package name.
	odd := winapi.AppExecLink{Package: "Something.Else_1", Target: `C:\y\AppInstallerPythonRedirector.exe`}
	if got, _ := classifyLink(odd); got != "stub" {
		t.Errorf("redirector target not treated as stub: %s", got)
	}
}
