package envmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindRoot(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	touch(t, repo, ".venv", "pyvenv.cfg")
	os.MkdirAll(filepath.Join(repo, "pkg", "a"), 0o755)
	os.MkdirAll(filepath.Join(repo, "venv"), 0o755) // a "venv" folder without pyvenv.cfg does not count

	nested := filepath.Join(repo, "libs", "core")
	touch(t, nested, ".venv", "pyvenv.cfg")
	os.MkdirAll(filepath.Join(nested, "src"), 0o755)

	redirect := filepath.Join(tmp, "redirected")
	touch(t, redirect, ".venv") // PEP 832 redirect file
	os.MkdirAll(filepath.Join(redirect, "sub"), 0o755)

	outer := filepath.Join(tmp, "outer")
	touch(t, outer, ".venv", "pyvenv.cfg")
	os.MkdirAll(filepath.Join(outer, "inner", ".git"), 0o755) // a separate repo inside a venv project
	os.MkdirAll(filepath.Join(outer, "inner", "src"), 0o755)
	touch(t, outer, "inner2", "pyproject.toml")
	os.MkdirAll(filepath.Join(outer, "inner2", ".git"), 0o755)

	deep := filepath.Join(repo, "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12")
	os.MkdirAll(deep, 0o755)

	cases := map[string]string{
		filepath.Join(repo, "pkg", "a"):      repo,                           // walks up to the venv
		repo:                                 repo,                           // the root itself
		filepath.Join(nested, "src"):         nested,                         // closest .venv wins in a monorepo
		filepath.Join(redirect, "sub"):       redirect,                       // redirect files count
		filepath.Join(outer, "inner", "src"): "",                             // stops at the inner .git
		filepath.Join(outer, "inner2"):       filepath.Join(outer, "inner2"), // pyproject.toml fallback
		deep:                                 "",                             // more than 12 levels up
		filepath.Join(deep, ".."):            repo,                           // exactly 12 levels up
	}
	for dir, want := range cases {
		if got := FindRoot(dir); got != filepath.Clean(want) && !(want == "" && got == "") {
			t.Errorf("FindRoot(%s) = %q, want %q", strings.TrimPrefix(dir, tmp), got, want)
		}
	}
}

func TestTrust(t *testing.T) {
	t.Setenv("VENVCTL_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "R&D Project")
	os.MkdirAll(dir, 0o755)
	if changed, err := SetTrust(dir, true); !changed || err != nil {
		t.Fatalf("allow: %v %v", changed, err)
	}
	if changed, _ := SetTrust(strings.ToUpper(dir), true); changed {
		t.Error("allowing the same folder in other case changed trust.json")
	}
	if !IsTrusted(strings.ToUpper(dir)) {
		t.Error("trust is not case-insensitive")
	}
	b, _ := os.ReadFile(TrustFile())
	key := `"` + strings.ToLower(filepath.ToSlash(dir)) + `"`
	if !strings.Contains(string(b), key) {
		t.Errorf("trust.json must hold %s literally (the hooks substring-search it):\n%s", key, b)
	}
	if changed, _ := SetTrust(dir, false); !changed || IsTrusted(dir) {
		t.Error("deny did not remove the root")
	}
}
