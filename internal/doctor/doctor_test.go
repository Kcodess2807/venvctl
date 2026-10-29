package doctor

import "testing"

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
