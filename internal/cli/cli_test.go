package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeVenv makes a folder that looks like a venv (pyvenv.cfg whose home has python.exe).
func fakeVenv(t *testing.T, dir string, withBase bool) {
	t.Helper()
	home := filepath.Join(filepath.Dir(dir), "base")
	if withBase {
		os.MkdirAll(home, 0o755)
		os.WriteFile(filepath.Join(home, "python.exe"), nil, 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "Scripts"), 0o755)
	os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte("home = "+home+"\nversion = 3.12.3\n"), 0o644)
}

func TestFlagsAndExitCodes(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	t.Setenv("NO_COLOR", "")
	t.Chdir(t.TempDir()) // no .venv anywhere above

	empty := filepath.Join(la, "venvctl")
	os.MkdirAll(empty, 0o755)

	cases := []struct {
		name       string
		args       []string
		code       int
		stdout     string // substring expected on stdout
		stderr     string // substring expected on stderr
		jsonStdout bool   // stdout must parse as JSON
	}{
		{"help lists exit codes", []string{"--help"}, 0, "Exit codes:", "", false},
		{"help groups", []string{"--help"}, 0, "Maintain:", "", false},
		{"unknown command", []string{"bogus"}, 2, "", "unknown command", false},
		{"unknown flag", []string{"list", "--bogus"}, 2, "", "→ see `venvctl list --help`", false},
		{"bad sort", []string{"list", "--sort", "nope"}, 2, "", "use one of: name, size", false},
		{"bad filter", []string{"list", "--filter", "nope"}, 2, "", "unknown --filter", false},
		{"bad min-size", []string{"list", "--min-size", "5XB"}, 2, "", "invalid --min-size", false},
		{"bad older-than", []string{"list", "--older-than", "soon"}, 2, "", "invalid --older-than", false},
		{"empty list is nothing to do", []string{"list"}, 4, "", "no environments known yet", false},
		{"empty json list", []string{"--json", "list"}, 4, "[]", "", true},
		{"quiet empty list prints nothing", []string{"-q", "list"}, 4, "", "", false},
		{"version", []string{"version"}, 0, "venvctl dev", "", false},
		{"json version", []string{"--json", "version"}, 0, `"go":`, "", true},
		{"quiet version", []string{"--quiet", "version"}, 0, "", "", false},
		{"nuke needs args", []string{"nuke"}, 2, "", "requires at least 1 arg", false},
		{"nuke unknown env", []string{"nuke", "ghost"}, 1, "", "→ run `venvctl list`", false},
		{"json error on stderr", []string{"--json", "nuke", "ghost"}, 1, "", `"exit_code":1`, false},
		{"create central needs name", []string{"create"}, 2, "", "needs a name", false},
		{"create rejects path names", []string{"create", `a\b`}, 2, "", "not a valid env name", false},
		{"create rejects missing interpreter", []string{"create", "x", "-p", `C:\nope\python.exe`}, 2, "", "no interpreter at", false},
		{"repair without venv", []string{"repair", "--dry-run"}, 1, "", "no .venv or venv", false},
		{"on without hook", []string{"on"}, 1, "", "needs the venvctl shell hook", false},
		{"cd needs arg", []string{"cd"}, 2, "", "accepts 1 arg", false},
		{"completion bash", []string{"completion", "bash"}, 0, "venvctl", "", false},
		{"completion powershell", []string{"completion", "powershell"}, 0, "Register-ArgumentCompleter", "", false},
		{"completion rejects zsh", []string{"completion", "zsh"}, 2, "", "invalid argument", false},
		{"init rejects fish", []string{"init", "fish"}, 2, "", "invalid argument", false},
		{"init pwsh prints hook", []string{"init", "pwsh"}, 0, "function global:venvctl", "", false},
		{"__shell off script", []string{"__shell", "pwsh", "off"}, 0, "_VENVCTL_OLD_PATH", "", false},
		{"__shell passes through", []string{"__shell", "bash", "version"}, 0, "", "", false},
		{"scan json", []string{"--json", "scan", "--dry-run", la}, 0, `"dry_run": true`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(tc.args, &out, &errOut, strings.NewReader(""))
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, out.String(), errOut.String())
			}
			if !strings.Contains(out.String(), tc.stdout) {
				t.Errorf("stdout missing %q:\n%s", tc.stdout, out.String())
			}
			if !strings.Contains(errOut.String(), tc.stderr) {
				t.Errorf("stderr missing %q:\n%s", tc.stderr, errOut.String())
			}
			if tc.stdout == "" && tc.args[0] == "-q" && out.Len() > 0 {
				t.Errorf("--quiet printed to stdout: %s", out.String())
			}
			if tc.jsonStdout && !json.Valid(out.Bytes()) {
				t.Errorf("stdout is not JSON:\n%s", out.String())
			}
			if strings.Contains(out.String()+errOut.String(), "\x1b[") {
				t.Errorf("ANSI codes in non-terminal output")
			}
		})
	}
}

func TestListFiltersOnFakeVenvs(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	work := t.TempDir()
	good, broken := filepath.Join(work, "good", ".venv"), filepath.Join(work, "broken", ".venv")
	fakeVenv(t, good, true)
	fakeVenv(t, broken, false)
	gone := filepath.Join(work, "gone", ".venv")

	reg, _ := json.Marshal(map[string]any{"envs": []map[string]any{
		{"name": "good", "path": good, "python": filepath.Join(filepath.Dir(good), "base"), "version": "3.12.3"},
		{"name": "broken", "path": broken, "python": filepath.Join(filepath.Dir(broken), "base"), "version": "3.9.1"},
		{"name": "gone", "path": gone, "version": "3.10.0"},
	}})
	os.MkdirAll(filepath.Join(la, "venvctl"), 0o755)
	os.WriteFile(filepath.Join(la, "venvctl", "registry.json"), reg, 0o644)

	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := Run(args, &out, &errOut, strings.NewReader(""))
		return code, strings.TrimSpace(out.String())
	}
	if code, out := run("list", "--paths", "--filter", "missing"); code != 0 || out != gone {
		t.Errorf("--filter missing: exit %d, %q", code, out)
	}
	if code, out := run("list", "--paths", "--filter", "broken"); code != 0 || out != broken {
		t.Errorf("--filter broken: exit %d, %q", code, out)
	}
	if code, out := run("list", "--paths", "--sort", "version"); code != 0 || out != strings.Join([]string{good, gone, broken}, "\n") {
		t.Errorf("--sort version: exit %d, %q", code, out)
	}
	if code, _ := run("list", "--filter", "central"); code != 4 {
		t.Errorf("--filter central: exit %d, want 4", code)
	}
	code, out := run("--json", "list")
	var rows []rowJSON
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 3 {
		t.Fatalf("--json list: exit %d, %s", code, out)
	}
	status := map[string]string{}
	for _, r := range rows {
		status[r.Name] = r.Status
	}
	if status["good"] != "ok" || status["broken"] != "broken" || status["gone"] != "missing" {
		t.Errorf("statuses: %v", status)
	}
	t.Setenv("VIRTUAL_ENV", good)
	if _, out := run("list", "--no-color"); !strings.Contains(out, "*  good") {
		t.Errorf("active venv not marked with *:\n%s", out)
	}
}

func TestParseSizeAndAge(t *testing.T) {
	for in, want := range map[string]int64{"500MB": 500 << 20, "1.5gb": 3 << 29, "2048": 2048, "1K": 1024} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "5XB", "-1MB", "MB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
	if d, err := parseAge("90d"); err != nil || d.Hours() != 90*24 {
		t.Errorf("parseAge(90d) = %v, %v", d, err)
	}
	if _, err := parseAge("soon"); err == nil {
		t.Errorf("parseAge(soon) accepted")
	}
}

func TestPrune(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	work := t.TempDir()
	good, broken := filepath.Join(work, "good", ".venv"), filepath.Join(work, "broken", ".venv")
	fakeVenv(t, good, true)
	fakeVenv(t, broken, false)
	gone := filepath.Join(work, "gone", ".venv")
	reg, _ := json.Marshal(map[string]any{"envs": []map[string]any{
		{"name": "good", "path": good, "python": filepath.Join(filepath.Dir(good), "base")},
		{"name": "broken", "path": broken, "python": filepath.Join(filepath.Dir(broken), "base")},
		{"name": "gone", "path": gone},
	}})
	os.MkdirAll(filepath.Join(la, "venvctl"), 0o755)
	os.WriteFile(filepath.Join(la, "venvctl", "registry.json"), reg, 0o644)

	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := Run(args, &out, &errOut, strings.NewReader(""))
		return code, out.String(), errOut.String()
	}
	steps := []struct {
		args []string
		code int
		want string // substring of stdout+stderr
	}{
		{[]string{"prune"}, 2, "at least one criterion"},
		{[]string{"prune", "--missing", "--central", "--local"}, 2, "exclude each other"},
		{[]string{"prune", "--min-size", "1TB"}, 4, "nothing to prune"},
		{[]string{"prune", "--missing", "--dry-run"}, 0, "gone"},
		{[]string{"prune", "--missing"}, 3, "rerun with --yes"}, // no console, no --yes
		{[]string{"prune", "--missing", "--yes"}, 0, "already gone"},
		{[]string{"prune", "--missing", "--dry-run"}, 4, "nothing to prune"}, // gone was unregistered
	}
	for _, s := range steps {
		code, out, errOut := run(s.args...)
		if code != s.code || !strings.Contains(out+errOut, s.want) {
			t.Fatalf("%v: exit %d (want %d), missing %q\nstdout: %s\nstderr: %s", s.args, code, s.code, s.want, out, errOut)
		}
	}
	code, out, _ := run("--json", "prune", "--broken", "--yes")
	var res pruneJSON
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || len(res.Results) != 1 || res.Results[0].Status != "deleted" {
		t.Fatalf("--json prune --broken --yes: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Errorf("broken venv still on disk")
	}
	if _, err := os.Stat(good); err != nil {
		t.Errorf("good venv was touched: %v", err)
	}
}

func TestLinkUnlink(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	work := t.TempDir()
	a, b := filepath.Join(work, "envs", "a"), filepath.Join(work, "envs", "b")
	fakeVenv(t, a, true)
	fakeVenv(t, b, true)
	reg, _ := json.Marshal(map[string]any{"envs": []map[string]any{
		{"name": "a", "path": a}, {"name": "b", "path": b},
	}})
	os.MkdirAll(filepath.Join(la, "venvctl"), 0o755)
	os.WriteFile(filepath.Join(la, "venvctl", "registry.json"), reg, 0o644)
	proj := filepath.Join(work, "proj")
	os.MkdirAll(proj, 0o755)
	t.Chdir(proj)

	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := Run(args, &out, &errOut, strings.NewReader(""))
		return code, out.String() + errOut.String()
	}
	steps := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"unlink"}, 4, "no .venv redirect"},
		{[]string{"link", "a"}, 0, "linked"},
		{[]string{"link", "a"}, 4, "already points at"},
		{[]string{"link", "b"}, 3, "--force to repoint"}, // no console to ask
		{[]string{"link", "b", "--force"}, 0, "linked"},
		{[]string{"list", "--paths", "--filter", "local"}, 0, b},
		{[]string{"unlink"}, 0, "removed"},
		{[]string{"unlink"}, 4, "no .venv redirect"},
	}
	for _, s := range steps {
		code, out := run(s.args...)
		if code != s.code || !strings.Contains(out, s.want) {
			t.Fatalf("%v: exit %d (want %d), missing %q in:\n%s", s.args, code, s.code, s.want, out)
		}
		if s.args[0] == "link" && code == 0 {
			got, _ := os.ReadFile(filepath.Join(proj, ".venv"))
			want := map[bool]string{true: b, false: a}[len(s.args) == 3] + "\n"
			if string(got) != want {
				t.Fatalf("redirect file %q, want %q (UTF-8, no BOM, one line, \n)", got, want)
			}
		}
	}
	// list shows "<project> → <venv>" while linked, and cd goes to the project, not the venv.
	run("link", "a")
	if _, out := run("list", "--no-color"); !strings.Contains(out, proj+" → "+a) {
		t.Errorf("list does not show project → venv:\n%s", out)
	}
	if _, out := run("__shell", "pwsh", "cd", "a"); !strings.Contains(out, "'"+proj+"'") {
		t.Errorf("cd of a linked env should go to its project:\n%s", out)
	}
	// A real .venv folder is never replaced or removed.
	os.Remove(filepath.Join(proj, ".venv"))
	os.MkdirAll(filepath.Join(proj, ".venv"), 0o755)
	if code, out := run("link", "a"); code != 3 || !strings.Contains(out, "is a folder") {
		t.Errorf("link over a .venv folder: exit %d\n%s", code, out)
	}
	if code, _ := run("unlink"); code != 3 {
		t.Errorf("unlink of a .venv folder: exit %d, want 3", code)
	}
	if _, err := os.Stat(filepath.Join(proj, ".venv")); err != nil {
		t.Errorf(".venv folder was removed")
	}
}
