package envmgr

import (
	"os/exec"
	"regexp"
	"strconv"
	"sync"
)

var (
	uvOnce sync.Once
	uvVer  [3]int
	uvOK   bool
)

// UvVersion is the version of the uv on PATH (asked once per run); ok is false without uv.
func UvVersion() (v [3]int, ok bool) {
	uvOnce.Do(func() {
		out, err := exec.Command("uv", "--version").Output()
		if err != nil {
			return
		}
		m := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(string(out))
		if m == nil {
			return
		}
		for i := range uvVer {
			uvVer[i], _ = strconv.Atoi(m[i+1])
		}
		uvOK = true
	})
	return uvVer, uvOK
}

// UvAtLeast reports whether the uv on PATH is at least major.minor.patch.
func UvAtLeast(major, minor, patch int) bool {
	v, ok := UvVersion()
	if !ok {
		return false
	}
	for i, want := range [3]int{major, minor, patch} {
		if v[i] != want {
			return v[i] > want
		}
	}
	return true
}
