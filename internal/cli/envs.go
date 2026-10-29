package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"venvctl/internal/discovery"
	"venvctl/internal/envmgr"
	"venvctl/internal/winapi"
)

// ---- resolving and prompting ----

// resolve wraps envmgr.Resolve for command verb. When several venvs share the name it lists
// them on stderr and asks which one; without a console it fails with the exact commands to run.
func resolve(reg *envmgr.Registry, arg, verb string) (envmgr.Entry, error) {
	e, err := envmgr.Resolve(reg, arg)
	var amb *envmgr.AmbiguousError
	var nf *envmgr.NotFoundError
	switch {
	case err == nil:
		return e, nil
	case errors.Is(err, envmgr.ErrNoLocalVenv):
		return e, Fail(exitFail, err.Error(), "cd into a project with a .venv, or pass a name or path (see `venvctl list`)")
	case errors.As(err, &nf):
		return e, Fail(exitFail, err.Error(), "run `venvctl list` to see names, or `venvctl scan` to register new venvs")
	case !errors.As(err, &amb):
		return e, Fail(exitFail, err.Error(), "pass the venv folder path instead")
	}
	var rows [][]string
	for i, h := range amb.Hits {
		rows = append(rows, []string{strconv.Itoa(i + 1), h.Name, h.Version, age(envmgr.Activity(h)), h.Path})
	}
	if !opt.json {
		fmt.Fprintf(stderr, "%q matches %d environments:\n", amb.Name, len(amb.Hits))
		table(stderr, []string{"#", "NAME", "PYTHON", "LAST USED", "PATH"}, rows, 0)
	}
	if interactive() {
		fmt.Fprintf(stderr, "which one? [1-%d] ", len(amb.Hits))
		var n int
		if _, err := fmt.Fscanln(stdin, &n); err == nil && n >= 1 && n <= len(amb.Hits) {
			return amb.Hits[n-1], nil
		}
		return envmgr.Entry{}, Fail(exitRefused, "no valid choice, nothing done", "rerun and pick a number, or pass the path")
	}
	var cmds []string
	for _, h := range amb.Hits {
		cmds = append(cmds, fmt.Sprintf(`venvctl %s "%s"`, verb, h.Path))
	}
	return envmgr.Entry{}, Fail(exitUsage, amb.Error(), "pick one by path:\n      "+strings.Join(cmds, "\n      "))
}

// interactive reports whether a person can answer a prompt: stdin is a console and not --json.
func interactive() bool { return !opt.json && isConsole(stdin) }

// confirm asks a yes/no question on stderr; anything but y/yes is No.
func confirm(question string) bool {
	fmt.Fprintf(stderr, "%s [y/N] ", question)
	var ans string
	fmt.Fscanln(stdin, &ans)
	a := strings.ToLower(strings.TrimSpace(ans))
	return a == "y" || a == "yes"
}

// ---- create ----

func createCmd() *cobra.Command {
	var py string
	var local, verbose, force, noRedirect bool
	c := &cobra.Command{
		Use:     "create [name]",
		GroupID: "use",
		Short:   "Create a venv: central by default, or .\\.venv with --local (uses uv if installed)",
		Long: `Creates a venv with "uv venv" when uv is on PATH, else "python -m venv".
Central envs live in %LOCALAPPDATA%\venvctl\envs\<name>. --local refuses cloud-synced folders
(OneDrive, Dropbox, Google Drive, iCloudDrive) unless --force.

A central env is linked to the current folder: .venv is written there as a PEP 832 redirect
file holding the env's path, so editors find it and "venvctl on" here activates it. A .venv
folder blocks this; a redirect pointing elsewhere is replaced only after you agree (or --force).
--no-redirect skips it. See redirect.md.`,
		Example: `  venvctl create api
  venvctl create api --python 3.12
  venvctl create --local -p C:\Python313\python.exe
  venvctl create ml -p 3.11.7 --verbose
  venvctl create scratch --no-redirect`,
		Args: nargs(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			var dest string
			switch {
			case local:
				dest, _ = filepath.Abs(".venv")
				if name == "" {
					name = filepath.Base(filepath.Dir(dest))
				}
				if root := envmgr.CloudRoot(dest); root != "" && !force {
					return Fail(exitRefused, fmt.Sprintf("%s is inside %s, a cloud-synced folder", dest, root),
						"a venv is 5-25k files that would all sync; use a central env (`venvctl create <name>`), or pass --force")
				}
			case name == "":
				return Fail(exitUsage, "a central venv needs a name", "run `venvctl create <name>`, or `venvctl create --local` for .\\.venv here")
			case name != filepath.Base(name) || name == "." || name == "..":
				return Fail(exitUsage, fmt.Sprintf("%q is not a valid env name", name), "use a plain name like api or ml-3.12, without slashes")
			default:
				dest = filepath.Join(envmgr.CentralDir(), name)
			}
			if _, err := os.Stat(filepath.Join(dest, "pyvenv.cfg")); err == nil {
				return Fail(exitRefused, dest+" already exists", "activate it with `venvctl on "+name+"`, or pick another name")
			}
			cwd, _ := os.Getwd()
			link := !local && !noRedirect
			if link {
				if _, err := checkLink(cwd, dest, force); err != nil {
					return err // checked before creating, so a refusal leaves nothing behind
				}
			}
			if py == "" {
				py = envmgr.LoadConfig().DefaultPython
			}
			if envmgr.IsInterpreterPath(py) {
				if _, err := os.Stat(py); err != nil {
					return Fail(exitUsage, "no interpreter at "+py, "pass an existing python.exe, or a version like 3.12")
				}
			}
			cmdline, err := envmgr.CreateCommand(dest, py)
			if err != nil {
				return Fail(exitFail, err.Error(), "install it (`uv python install "+py+"` or python.org), or pass --python <path to python.exe>")
			}
			if verbose {
				note("%s %s", dim("running:"), quoteArgs(cmdline))
			}
			var buf bytes.Buffer
			var out io.Writer = stderr
			if opt.quiet || opt.json {
				out = &buf
			}
			e, err := envmgr.Create(dest, cmdline, out)
			if err != nil {
				return Fail(exitFail, err.Error()+tail(buf.String()), "rerun with --verbose to see the exact command, then run it yourself to debug")
			}
			e.Name = name
			redirect := ""
			if link {
				if err := envmgr.WriteRedirect(cwd, dest); err != nil {
					note("%s venv created, but %s could not be written: %v; run `venvctl link %s` later", warnMark(), filepath.Join(cwd, ".venv"), err, name)
				} else {
					redirect, e.Project = filepath.Join(cwd, ".venv"), cwd
				}
			}
			reg := envmgr.LoadRegistry()
			reg.Upsert(e)
			if err := reg.Save(); err != nil {
				return Fail(exitFail, "venv created but the registry could not be saved: "+err.Error(), "check that %LOCALAPPDATA%\\venvctl is writable, then run `venvctl scan`")
			}
			if opt.json {
				return emit(struct {
					Name       string   `json:"name"`
					Path       string   `json:"path"`
					PythonHome string   `json:"python_home"`
					Version    string   `json:"version"`
					Central    bool     `json:"central"`
					Command    []string `json:"command"`
					Redirect   string   `json:"redirect"`
				}{e.Name, e.Path, e.Python, e.Version, e.Central, cmdline, redirect})
			}
			say("%s %s  %s  %s", okMark(), bold(e.Name), dim(e.Path), dim("python "+e.Version))
			if redirect != "" {
				say("%s linked %s → %s %s", okMark(), redirect, e.Path, dim("(venvctl unlink removes it)"))
			}
			say(dim("activate with: venvctl on " + e.Name))
			return nil
		},
	}
	f := c.Flags()
	f.StringVarP(&py, "python", "p", "", "Python version (3.12, 3.12.3) or path to python.exe")
	f.BoolVarP(&local, "local", "l", false, "create .\\.venv in the current folder")
	f.BoolVarP(&verbose, "verbose", "v", false, "print the uv or python command that runs")
	f.BoolVarP(&force, "force", "f", false, "allow --local in a cloud-synced folder; replace a .venv redirect pointing elsewhere")
	f.BoolVar(&noRedirect, "no-redirect", false, "do not write a .venv redirect file in the current folder")
	return c
}

// checkLink decides whether dir\.venv may be pointed at venv. already is true when it already
// points there. It fails when .venv is a real folder, or a redirect elsewhere that the user did
// not agree to replace (prompt in a console, else --force).
func checkLink(dir, venv string, force bool) (already bool, err error) {
	d := envmgr.Inspect(dir)
	switch d.Kind {
	case "dir":
		return false, Fail(exitRefused, d.Path+" is a folder (a project-local venv), so no redirect can be written there",
			"run from another folder, or use that venv with `venvctl on`")
	case "file":
		if envmgr.SamePath(d.Target, venv) {
			return true, nil
		}
		if force || (interactive() && confirm(fmt.Sprintf("%s points at %s; point it at %s instead?", d.Path, d.Target, venv))) {
			return false, nil
		}
		return false, Fail(exitRefused, d.Path+" already points at "+d.Target, "rerun with --force to repoint it")
	}
	return false, nil
}

func linkCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "link <name|path>",
		GroupID: "use",
		Short:   "Point this folder's .venv at an existing venv (a PEP 832 redirect file)",
		Long: "Writes .venv in the current folder as a one-line redirect file holding the venv's absolute path\n" +
			"(PEP 832, draft). Editors and tools that understand redirects then find the central venv as\n" +
			"this project's environment, and `venvctl on` here activates it. See redirect.md.",
		Example: `  venvctl link api
  venvctl link C:\envs\ml --force`,
		Args: nargs(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			reg := envmgr.LoadRegistry()
			e, err := resolve(reg, args[0], "link")
			if err != nil {
				return err
			}
			if envmgr.SamePath(e.Path, filepath.Join(cwd, ".venv")) {
				return nothing("this folder's .venv is that venv itself; nothing to link")
			}
			already, err := checkLink(cwd, e.Path, force)
			if err != nil {
				return err
			}
			if !already {
				if err := envmgr.WriteRedirect(cwd, e.Path); err != nil {
					return Fail(exitFail, "could not write "+filepath.Join(cwd, ".venv")+": "+err.Error(), "check that this folder is writable")
				}
			}
			e.Project = cwd
			reg.Upsert(e)
			if err := reg.Save(); err != nil {
				return Fail(exitFail, "linked, but the registry could not be saved: "+err.Error(), "run `venvctl scan "+cwd+"` to resync it")
			}
			if opt.json {
				return emit(linkJSON{cwd, e.Path, filepath.Join(cwd, ".venv"), !already})
			}
			if already {
				return nothing(filepath.Join(cwd, ".venv") + " already points at " + e.Path)
			}
			say("%s linked %s → %s", okMark(), filepath.Join(cwd, ".venv"), e.Path)
			return nil
		},
	}
	c.Flags().BoolVarP(&force, "force", "f", false, "replace a .venv redirect that points at another venv")
	return c
}

type linkJSON struct {
	Project  string `json:"project"`
	Venv     string `json:"venv"`
	Redirect string `json:"redirect"`
	Changed  bool   `json:"changed"`
}

func unlinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unlink",
		GroupID: "use",
		Short:   "Remove this folder's .venv redirect file (never a real venv folder)",
		Example: `  venvctl unlink`,
		Args:    nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			cwd, _ := os.Getwd()
			d := envmgr.Inspect(cwd)
			switch d.Kind {
			case "none":
				return nothing("no .venv redirect in " + cwd)
			case "dir":
				return Fail(exitRefused, d.Path+" is a real venv folder, not a redirect",
					"unlink only removes redirect files; to delete a venv use `venvctl nuke`")
			}
			if err := envmgr.RemoveRedirect(cwd); err != nil {
				return Fail(exitFail, "could not remove "+d.Path+": "+err.Error(), "check that the file is not read-only")
			}
			reg := envmgr.LoadRegistry()
			for i := range reg.Envs {
				if envmgr.SamePath(reg.Envs[i].Project, cwd) {
					reg.Envs[i].Project = ""
				}
			}
			reg.Save()
			if opt.json {
				return emit(linkJSON{cwd, d.Target, d.Path, true})
			}
			say("%s removed %s %s", okMark(), d.Path, dim("(it pointed at "+d.Target+"; the venv itself is untouched)"))
			return nil
		},
	}
}

// quoteArgs renders a command line with paths quoted when they contain spaces.
func quoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		q[i] = a
	}
	return strings.Join(q, " ")
}

// tail returns the last line of captured tool output, prefixed for an error message.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return ": " + strings.TrimSpace(lines[len(lines)-1])
}

// ---- list ----

// row is a registry entry plus what list/prune compute from disk.
type row struct {
	envmgr.Entry
	status   string // ok, missing, broken (base interpreter gone)
	size     int64
	activity time.Time
	active   bool
}

// gather checks every entry on disk in parallel: status, size and last activity.
func gather(envs []envmgr.Entry) []row {
	rows := make([]row, len(envs))
	active := os.Getenv("VIRTUAL_ENV")
	var wg sync.WaitGroup
	for i, e := range envs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := row{Entry: e, status: "ok", active: active != "" && envmgr.SamePath(e.Path, active)}
			if _, err := os.Stat(filepath.Join(e.Path, "pyvenv.cfg")); err != nil {
				r.status = "missing"
				r.activity = e.LastUsed
				rows[i] = r
				return
			}
			if e.Python != "" {
				if _, err := os.Stat(filepath.Join(e.Python, "python.exe")); err != nil {
					r.status = "broken"
				}
			}
			r.size = envmgr.Size(e.Path)
			r.activity = envmgr.Activity(e)
			rows[i] = r
		}()
	}
	wg.Wait()
	return rows
}

// rowJSON is the --json shape of one venv (see docs/json.md).
type rowJSON struct {
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	Project      string  `json:"project"`
	PythonHome   string  `json:"python_home"`
	Version      string  `json:"version"`
	Central      bool    `json:"central"`
	Status       string  `json:"status"`
	SizeBytes    int64   `json:"size_bytes"`
	LastUsed     *string `json:"last_used"`
	LastActivity *string `json:"last_activity"`
	Active       bool    `json:"active"`
}

func (r row) json() rowJSON {
	iso := func(t time.Time) *string {
		if t.IsZero() {
			return nil
		}
		s := t.Format(time.RFC3339)
		return &s
	}
	return rowJSON{r.Name, r.Path, r.Project, r.Python, r.Version, r.Central, r.status, r.size, iso(r.LastUsed), iso(r.activity), r.active}
}

// filters are the selection flags shared by list and prune.
type filters struct {
	filter, minSize, olderThan string
	minBytes                   int64
	minAge                     time.Duration
}

func (f *filters) parse() error {
	switch f.filter {
	case "", "missing", "broken", "central", "local":
	default:
		return Fail(exitUsage, fmt.Sprintf("unknown --filter %q", f.filter), "use one of: missing, broken, central, local")
	}
	if f.minSize != "" {
		n, err := parseSize(f.minSize)
		if err != nil {
			return Fail(exitUsage, fmt.Sprintf("invalid --min-size %q", f.minSize), "use a number with B, KB, MB, GB or TB, like 500MB")
		}
		f.minBytes = n
	}
	if f.olderThan != "" {
		d, err := parseAge(f.olderThan)
		if err != nil {
			return Fail(exitUsage, fmt.Sprintf("invalid --older-than %q", f.olderThan), "use a number with h, d or w, like 90d")
		}
		f.minAge = d
	}
	return nil
}

func (f *filters) match(r row) bool {
	switch f.filter {
	case "missing":
		if r.status != "missing" {
			return false
		}
	case "broken":
		if r.status != "broken" {
			return false
		}
	case "central":
		if !r.Central {
			return false
		}
	case "local":
		if r.Central {
			return false
		}
	}
	if f.minBytes > 0 && r.size < f.minBytes {
		return false
	}
	if f.minAge > 0 && !r.activity.IsZero() && time.Since(r.activity) < f.minAge {
		return false
	}
	return true
}

func listCmd() *cobra.Command {
	var f filters
	var sortBy string
	var paths bool
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		GroupID: "find",
		Short:   "List known venvs with size, status and last use (* marks the active one)",
		Long: "Lists every registered venv. LAST USED is venvctl's own activation stamp; a value with ~ is\n" +
			"estimated from the venv's files (newest of the folder, pyvenv.cfg and site-packages).\n" +
			"Status: missing = folder gone, broken = its base Python is gone.",
		Example: `  venvctl list --sort size
  venvctl list --filter broken
  venvctl list --min-size 500MB --older-than 90d
  venvctl list --paths --filter local | ForEach-Object { venvctl repair -n $_ }`,
		Args: nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			if err := f.parse(); err != nil {
				return err
			}
			less, err := sorter(sortBy)
			if err != nil {
				return err
			}
			reg := envmgr.LoadRegistry()
			all := gather(reg.Envs)
			cacheSizes(reg, all)
			var rows []row
			var total int64
			for _, r := range all {
				if f.match(r) {
					rows = append(rows, r)
					total += r.size
				}
			}
			sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
			switch {
			case opt.json:
				out := []rowJSON{}
				for _, r := range rows {
					out = append(out, r.json())
				}
				if err := emit(out); err != nil {
					return err
				}
			case paths:
				for _, r := range rows {
					if !opt.quiet {
						fmt.Fprintln(stdout, r.Path)
					}
				}
			case len(rows) > 0:
				var cells [][]string
				for _, r := range rows {
					mark := " "
					if r.active {
						mark = paint(green, "*")
					}
					size := human(r.size)
					switch r.status {
					case "missing":
						size = paint(red, "missing")
					case "broken":
						size = paint(yellow, "broken ") + human(r.size)
					}
					last := age(r.LastUsed)
					if r.LastUsed.IsZero() && !r.activity.IsZero() {
						last = "~" + age(r.activity)
					}
					where := r.Path
					if r.Project != "" {
						where = r.Project + " → " + r.Path
					}
					if r.Central {
						where = dim(where)
					}
					cells = append(cells, []string{mark, r.Name, r.Version, size, last, where})
				}
				if !opt.quiet {
					table(stdout, []string{" ", "NAME", "PYTHON", "SIZE", "LAST USED", "PATH"}, cells, 3)
				}
				say(dim(fmt.Sprintf("%d of %d environments, %s", len(rows), len(all), human(total))))
			}
			if len(rows) == 0 {
				if len(all) == 0 {
					return nothing("no environments known yet; run `venvctl scan` or `venvctl create <name>`")
				}
				return nothing("no environments match")
			}
			return nil
		},
	}
	fl := c.Flags()
	fl.StringVar(&sortBy, "sort", "name", "sort by name, size, last-used or version")
	fl.StringVar(&f.filter, "filter", "", "only missing, broken, central or local venvs")
	fl.StringVar(&f.minSize, "min-size", "", "only venvs at least this big, like 500MB")
	fl.StringVar(&f.olderThan, "older-than", "", "only venvs unused for this long, like 90d")
	fl.BoolVar(&paths, "paths", false, "print one path per line, for piping")
	return c
}

// sorter returns the ordering for --sort.
func sorter(by string) (func(a, b row) bool, error) {
	switch by {
	case "name":
		return func(a, b row) bool {
			if x, y := strings.ToLower(a.Name), strings.ToLower(b.Name); x != y {
				return x < y
			}
			return strings.ToLower(a.Path) < strings.ToLower(b.Path)
		}, nil
	case "size":
		return func(a, b row) bool { return a.size > b.size }, nil
	case "last-used":
		return func(a, b row) bool { return a.activity.After(b.activity) }, nil
	case "version":
		return func(a, b row) bool { return discovery.CompareVersions(a.Version, b.Version) > 0 }, nil
	}
	return nil, Fail(exitUsage, fmt.Sprintf("unknown --sort %q", by), "use one of: name, size, last-used, version")
}

// cacheSizes stores computed sizes in the registry so `venvctl` (no args) can show a total fast.
func cacheSizes(reg *envmgr.Registry, rows []row) {
	for _, r := range rows {
		if r.status != "missing" {
			e := r.Entry
			e.Size = r.size
			reg.Upsert(e)
		}
	}
	reg.Save()
}

// ---- scan ----

func scanCmd() *cobra.Command {
	var dry bool
	c := &cobra.Command{
		Use:     "scan [root...]",
		GroupID: "find",
		Short:   "Find venvs on disk, register new ones and unregister vanished ones",
		Long: "Walks the given folders (default: config.json scan_roots, else every fixed drive) for pyvenv.cfg.\n" +
			"Skips Windows, Program Files, ProgramData, AppData, node_modules, .git and similar.\n" +
			"The central env folder is always included.",
		Example: `  venvctl scan
  venvctl scan D:\PROJECTS "D:\GRAVITY AI"
  venvctl scan --dry-run`,
		RunE: func(_ *cobra.Command, args []string) error {
			roots := args
			if len(roots) == 0 {
				roots = envmgr.LoadConfig().ScanRoots
			}
			if len(roots) == 0 {
				roots = discovery.FixedDrives()
			}
			roots = dedupeRoots(append(roots, envmgr.CentralDir()))
			note(dim("scanning " + strings.Join(roots, ", ")))
			start := time.Now()
			var p discovery.Progress
			stop := liveCounter(&p)
			found := discovery.Scan(roots, &p)
			stop()

			reg := envmgr.LoadRegistry()
			known := map[string]bool{}
			for _, e := range reg.Envs {
				known[strings.ToLower(filepath.Clean(e.Path))] = true
			}
			type item struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			added, pruned := []item{}, []item{}
			for _, path := range found {
				e, ok := envmgr.FromDir(path)
				if !ok {
					continue
				}
				if !known[strings.ToLower(filepath.Clean(e.Path))] {
					added = append(added, item{e.Name, e.Path})
				}
				if !dry {
					reg.Upsert(e)
				}
			}
			for _, e := range append([]envmgr.Entry(nil), reg.Envs...) {
				if _, err := os.Stat(filepath.Join(e.Path, "pyvenv.cfg")); err != nil {
					pruned = append(pruned, item{e.Name, e.Path})
					if !dry {
						reg.Remove(e.Path)
					}
				}
			}
			if !dry {
				if err := reg.Save(); err != nil {
					return Fail(exitFail, "could not save the registry: "+err.Error(), "check that %LOCALAPPDATA%\\venvctl is writable")
				}
			}
			took := time.Since(start)
			if opt.json {
				return emit(struct {
					Roots       []string `json:"roots"`
					DirsScanned int64    `json:"dirs_scanned"`
					Found       int      `json:"found"`
					Added       []item   `json:"added"`
					Pruned      []item   `json:"pruned"`
					DryRun      bool     `json:"dry_run"`
					DurationMS  int64    `json:"duration_ms"`
				}{roots, p.Dirs.Load(), len(found), added, pruned, dry, took.Milliseconds()})
			}
			verb, gone := "registered", "unregistered"
			if dry {
				verb, gone = "would register", "would unregister"
			}
			for _, a := range added {
				say("%s %s %s  %s", okMark(), verb, a.Name, dim(a.Path))
			}
			for _, x := range pruned {
				say("%s %s %s  %s", badMark(), gone, x.Name, dim(x.Path+" (folder gone)"))
			}
			say(dim(fmt.Sprintf("scanned %s dirs in %s: %d venvs found, %d new, %d pruned%s",
				commas(p.Dirs.Load()), took.Round(time.Millisecond), len(found), len(added), len(pruned),
				map[bool]string{true: " (dry run, registry unchanged)"}[dry])))
			return nil
		},
	}
	c.Flags().BoolVarP(&dry, "dry-run", "n", false, "show what would be registered or pruned, change nothing")
	return c
}

// liveCounter redraws "scanned N dirs, found M venvs" on stderr while a scan runs, only when
// stdout and stderr are consoles. The returned func stops it and ends the line.
func liveCounter(p *discovery.Progress) func() {
	if opt.quiet || opt.json || !isConsole(stdout) || !isConsole(stderr) {
		return func() {}
	}
	quit, finished := make(chan struct{}), make(chan struct{})
	draw := func() {
		fmt.Fprintf(stderr, "\rscanned %s dirs, found %d venvs ", commas(p.Dirs.Load()), p.Found.Load())
	}
	go func() {
		defer close(finished)
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				draw()
			case <-quit:
				draw()
				fmt.Fprintln(stderr)
				return
			}
		}
	}()
	return func() { close(quit); <-finished }
}

// dedupeRoots makes roots absolute and drops repeats, comparing case-insensitively.
func dedupeRoots(roots []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			abs = r
		}
		if k := strings.ToLower(filepath.Clean(abs)); !seen[k] {
			seen[k] = true
			out = append(out, abs)
		}
	}
	return out
}

// ---- nuke ----

// nukeResult is the outcome for one venv (see docs/json.md).
type nukeResult struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	Status     string       `json:"status"` // deleted, would-delete, unregistered, would-unregister, refused, failed
	FreedBytes int64        `json:"freed_bytes"`
	Lockers    []lockerJSON `json:"lockers"`
	Trash      string       `json:"trash"`
	SweptTrash []string     `json:"swept_trash"` // older leftover trash folders removed alongside
	Error      string       `json:"error"`
	err        *exitError
}

type lockerJSON struct {
	PID       uint32   `json:"pid"`
	Name      string   `json:"name"`
	Parent    string   `json:"parent"`
	Exe       string   `json:"exe"`
	OpenFiles []string `json:"open_files"`
	Protected bool     `json:"protected"`
}

func nukeCmd() *cobra.Command {
	var force, dry bool
	c := &cobra.Command{
		Use:     "nuke <name|path>...",
		Aliases: []string{"rm", "remove"},
		GroupID: "maintain",
		Short:   "Delete venvs, first finding and (with your OK) stopping processes that lock them",
		Long: "Finds processes holding the venv's .exe/.dll/.pyd files (Windows Restart Manager), shows them,\n" +
			"and asks before terminating them. Editors and shells (VS Code, PyCharm, PowerShell, ...) are never\n" +
			"terminated. The venv is renamed away before deleting, so a locked venv is never half-deleted.",
		Example: `  venvctl nuke old-api
  venvctl nuke D:\PROJECTS\x\.venv --dry-run
  venvctl nuke a b c --force`,
		Args: nargs(cobra.MinimumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			reg := envmgr.LoadRegistry()
			var targets []envmgr.Entry
			for _, a := range args {
				e, err := resolve(reg, a, "nuke")
				if err != nil {
					return err
				}
				targets = append(targets, e)
			}
			return finishNuke(reg, targets, force, dry)
		},
	}
	c.Flags().BoolVarP(&force, "force", "f", false, "terminate locking processes without asking (never editors or shells)")
	c.Flags().BoolVarP(&dry, "dry-run", "n", false, "show lockers and what would be deleted, change nothing")
	return c
}

// finishNuke deletes targets, saves the registry, prints results and picks the exit code:
// 1 if any failed, else 3 if any were refused.
func finishNuke(reg *envmgr.Registry, targets []envmgr.Entry, force, dry bool) error {
	var results []nukeResult
	for _, e := range targets {
		r := nukeOne(reg, e, force, dry)
		results = append(results, r)
		if !opt.json {
			printNuke(r)
		}
	}
	if !dry {
		if err := reg.Save(); err != nil {
			printErr(&exitError{code: exitFail, msg: "could not save the registry: " + err.Error(), fix: "run `venvctl scan` to resync it"})
		}
	}
	code := exitOK
	for _, r := range results {
		if r.err == nil {
			continue
		}
		if r.err.code == exitFail || code == exitOK {
			code = r.err.code
		}
	}
	if opt.json {
		if results == nil {
			results = []nukeResult{}
		}
		if err := emit(struct {
			Results []nukeResult `json:"results"`
		}{results}); err != nil {
			return err
		}
	}
	return done(code)
}

func nukeOne(reg *envmgr.Registry, e envmgr.Entry, force, dry bool) nukeResult {
	r := nukeResult{Name: e.Name, Path: e.Path, Lockers: []lockerJSON{}, SweptTrash: []string{}}
	fail := func(code int, msg, fix string) nukeResult {
		r.err = &exitError{code: code, msg: msg, fix: fix}
		r.Error = msg
		return r
	}
	if _, err := os.Stat(e.Path); err != nil {
		r.Status = "would-unregister"
		if !dry {
			reg.Remove(e.Path)
			r.Status = "unregistered"
		}
		return r
	}
	lockers, err := winapi.LockingProcesses(e.Path)
	if err != nil {
		note("%s lock detection failed for %s: %v; trying to delete anyway", warnMark(), e.Path, err)
	}
	for _, l := range lockers {
		r.Lockers = append(r.Lockers, lockerJSON{l.PID, l.Name, l.Parent, l.Exe, l.OpenFiles, l.Protected()})
	}
	size := envmgr.Size(e.Path)
	if dry {
		r.Status, r.FreedBytes = "would-delete", size
		return r
	}
	if len(lockers) > 0 {
		if err := killLockers(e.Path, lockers, force); err != nil {
			var ee *exitError
			errors.As(err, &ee)
			r.Status = "refused"
			return fail(ee.code, ee.msg, ee.fix)
		}
	}
	if err := envmgr.Delete(e.Path); err != nil {
		var lo *envmgr.LeftoverError
		if errors.As(err, &lo) {
			reg.Remove(e.Path)
			r.Status, r.Trash, r.FreedBytes = "deleted", lo.Trash, size-envmgr.Size(lo.Trash)
			return fail(exitFail, "leftovers remain in "+lo.Trash, `close whatever still uses them, then run: venvctl nuke "`+lo.Trash+`"`)
		}
		r.Status = "failed"
		return fail(exitFail, e.Path+" is still locked; nothing was deleted",
			"close the program using it (`venvctl nuke --dry-run "+e.Name+"` lists lockers), then retry")
	}
	reg.Remove(e.Path)
	r.Status, r.FreedBytes = "deleted", size
	swept, freed := envmgr.SweepTrash(e.Path) // leftovers of earlier interrupted deletes, any generation
	r.SweptTrash, r.FreedBytes = append(r.SweptTrash, swept...), r.FreedBytes+freed
	return r
}

func printNuke(r nukeResult) {
	switch r.Status {
	case "deleted":
		say("%s nuked %s  %s  %s", okMark(), bold(r.Name), dim(r.Path), "freed "+human(r.FreedBytes))
		if r.Trash != "" {
			say("    leftovers: %s", r.Trash)
		}
		for _, t := range r.SweptTrash {
			say("    also removed old trash: %s", dim(t))
		}
	case "unregistered", "would-unregister":
		say("%s %s  %s", okMark(), r.Name, dim("folder already gone, "+strings.TrimPrefix(r.Status, "would-")+" "+r.Path))
	case "would-delete":
		say("would delete %s  %s  (%s)", bold(r.Name), dim(r.Path), human(r.FreedBytes))
		if len(r.Lockers) > 0 {
			lockerTable(stdout, r.Lockers)
			say(dim("these processes would need to stop first"))
		}
	}
	if r.err != nil {
		printErr(r.err)
	}
}

func lockerTable(w io.Writer, ls []lockerJSON) {
	var rows [][]string
	for _, l := range ls {
		files := strconv.Itoa(len(l.OpenFiles))
		if len(l.OpenFiles) > 0 {
			files += ": " + filepath.Base(l.OpenFiles[0])
			if len(l.OpenFiles) > 1 {
				files += fmt.Sprintf(" +%d", len(l.OpenFiles)-1)
			}
		}
		name := l.Name
		if l.Protected {
			name += paint(yellow, " (editor/shell)")
		}
		rows = append(rows, []string{strconv.Itoa(int(l.PID)), name, orDash(l.Parent), files})
	}
	table(w, []string{"PID", "NAME", "SPAWNED BY", "OPEN FILES"}, rows, 0)
}

// killLockers shows who holds files in dir, then terminates them after a [y/N] (default No),
// or straight away with force. Editors and shells are never terminated.
func killLockers(dir string, lockers []winapi.Locker, force bool) error {
	var ls []lockerJSON
	var blocked []string
	for _, l := range lockers {
		ls = append(ls, lockerJSON{l.PID, l.Name, l.Parent, l.Exe, l.OpenFiles, l.Protected()})
		if l.Protected() {
			blocked = append(blocked, fmt.Sprintf("%s (PID %d)", filepath.Base(l.Exe), l.PID))
		}
	}
	if !opt.json {
		fmt.Fprintf(stderr, "%d process(es) hold files in %s:\n", len(lockers), dir)
		lockerTable(stderr, ls)
	}
	if len(blocked) > 0 {
		return Fail(exitRefused, strings.Join(blocked, ", ")+" hold files in "+dir,
			"venvctl never terminates editors or shells; close them (or their Python terminal), then retry")
	}
	if !force {
		if !interactive() {
			return Fail(exitRefused, fmt.Sprintf("%d process(es) hold files in %s", len(lockers), dir),
				"close them, or rerun with --force to terminate them")
		}
		if !confirm(fmt.Sprintf("terminate %d process(es)?", len(lockers))) {
			return Fail(exitRefused, "not terminated, nothing deleted", "close the processes yourself, then retry")
		}
	}
	for _, l := range lockers {
		if err := winapi.Kill(l.PID); err != nil {
			note("%s could not terminate PID %d: %v", warnMark(), l.PID, err)
		}
	}
	return nil
}

// ---- prune ----

func pruneCmd() *cobra.Command {
	var f filters
	var missing, broken, central, local, dry, yes bool
	c := &cobra.Command{
		Use:     "prune",
		GroupID: "maintain",
		Short:   "Delete many venvs at once: old, big, missing or broken ones",
		Long: "Selects venvs matching ALL given criteria (--missing and --broken together mean either),\n" +
			"shows them with sizes and a total, then asks once before deleting. Each deletion goes through\n" +
			"the same path as `venvctl nuke`: lock detection, editor protection, rename-first delete.\n" +
			"Refused venvs are skipped and listed at the end. The active venv is never pruned.\n" +
			"--missing entries are only unregistered; their folders are already gone.",
		Example: `  venvctl prune --older-than 180d --dry-run
  venvctl prune --min-size 1GB --older-than 90d
  venvctl prune --missing --yes
  venvctl --json prune --broken --local --yes`,
		Args: nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			if central && local {
				return Fail(exitUsage, "--central and --local exclude each other", "pick one, or neither for both kinds")
			}
			if !missing && !broken && f.olderThan == "" && f.minSize == "" {
				return Fail(exitUsage, "prune needs at least one criterion",
					"add --older-than, --min-size, --missing or --broken (see `venvctl prune --help`)")
			}
			if central {
				f.filter = "central"
			} else if local {
				f.filter = "local"
			}
			if err := f.parse(); err != nil {
				return err
			}
			reg := envmgr.LoadRegistry()
			all := gather(reg.Envs)
			cacheSizes(reg, all)
			var picked []row
			var total int64
			for _, r := range all {
				statusOK := (!missing && !broken) || (missing && r.status == "missing") || (broken && r.status == "broken")
				if statusOK && f.match(r) && !r.active {
					picked = append(picked, r)
					total += r.size
				}
			}
			sort.SliceStable(picked, func(i, j int) bool { return picked[i].size > picked[j].size })

			candidates := []rowJSON{}
			var cells [][]string
			for _, r := range picked {
				candidates = append(candidates, r.json())
				size := human(r.size)
				if r.status != "ok" {
					size = r.status + " " + size
				}
				last := age(r.activity)
				if !r.activity.IsZero() && r.LastUsed.IsZero() {
					last = "~" + last
				}
				cells = append(cells, []string{r.Name, size, last, r.Path})
			}
			if len(picked) == 0 {
				if opt.json {
					emit(pruneJSON{Candidates: candidates, DryRun: dry, Results: []nukeResult{}})
				}
				return nothing("no venvs match; nothing to prune")
			}
			if !opt.json && !opt.quiet {
				table(stdout, []string{"NAME", "SIZE", "LAST USED", "PATH"}, cells, 1)
			}
			say(dim(fmt.Sprintf("%d venvs, %s", len(picked), human(total))))

			proceed := !dry
			if proceed && !yes {
				switch {
				case !interactive():
					if opt.json {
						emit(pruneJSON{Candidates: candidates, TotalBytes: total, DryRun: dry, Results: []nukeResult{}})
					}
					return Fail(exitRefused, fmt.Sprintf("%d venvs selected, none deleted", len(picked)), "rerun with --yes to delete them, or --dry-run to only list")
				case !confirm(fmt.Sprintf("delete %d venvs freeing %s?", len(picked), human(total))):
					return Fail(exitRefused, "nothing deleted", "rerun when you are sure, or narrow the criteria")
				}
			}
			results := []nukeResult{}
			code := exitOK
			var refused []string
			if proceed {
				for _, r := range picked {
					res := nukeOne(reg, r.Entry, false, false)
					results = append(results, res)
					if !opt.json {
						printNuke(res)
					}
					if res.err != nil {
						refused = append(refused, r.Name+" ("+res.Status+")")
						if res.err.code == exitFail || code == exitOK {
							code = res.err.code
						}
					}
				}
				if err := reg.Save(); err != nil {
					printErr(&exitError{code: exitFail, msg: "could not save the registry: " + err.Error(), fix: "run `venvctl scan` to resync it"})
					code = exitFail
				}
			}
			if opt.json {
				var freed int64
				for _, r := range results {
					freed += r.FreedBytes
				}
				if err := emit(pruneJSON{candidates, total, dry, results, freed}); err != nil {
					return err
				}
			} else if proceed {
				var freed int64
				for _, r := range results {
					freed += r.FreedBytes
				}
				say(dim(fmt.Sprintf("freed %s from %d of %d venvs", human(freed), len(picked)-len(refused), len(picked))))
				if len(refused) > 0 {
					say("%s skipped: %s", warnMark(), strings.Join(refused, ", "))
				}
			}
			return done(code)
		},
	}
	fl := c.Flags()
	fl.StringVar(&f.olderThan, "older-than", "", "unused for at least this long, like 90d")
	fl.StringVar(&f.minSize, "min-size", "", "at least this big, like 1GB")
	fl.BoolVar(&missing, "missing", false, "folder is gone (only unregisters)")
	fl.BoolVar(&broken, "broken", false, "base Python is gone")
	fl.BoolVar(&central, "central", false, "only central envs")
	fl.BoolVar(&local, "local", false, "only project-local envs")
	fl.BoolVarP(&dry, "dry-run", "n", false, "list candidates, delete nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "delete without the confirmation prompt")
	return c
}

// pruneJSON is the --json shape of `venvctl prune` (see docs/json.md).
type pruneJSON struct {
	Candidates []rowJSON    `json:"candidates"`
	TotalBytes int64        `json:"total_bytes"`
	DryRun     bool         `json:"dry_run"`
	Results    []nukeResult `json:"results"`
	FreedBytes int64        `json:"freed_bytes"`
}

// ---- repair ----

func repairCmd() *cobra.Command {
	var dry bool
	c := &cobra.Command{
		Use:     "repair [name|path]",
		GroupID: "maintain",
		Short:   "Fix a moved or renamed venv by rewriting its old path in Scripts\\",
		Long: "Reads the venv's previous location from Scripts\\activate.bat and rewrites it, at path\n" +
			"boundaries only, in every file in Scripts\\, including pip and uv launcher .exe files.\n" +
			"Rewriting .exe files can alert antivirus; use --dry-run to preview.",
		Example: `  venvctl repair
  venvctl repair --dry-run D:\PROJECTS\renamed\.venv
  venvctl repair api`,
		Args: nargs(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			reg := envmgr.LoadRegistry()
			e, err := resolve(reg, target, "repair")
			if err != nil {
				return err
			}
			old, files, err := envmgr.Repair(e.Path, !dry)
			if err != nil {
				return Fail(exitFail, err.Error(), "repair needs the Scripts\\activate.bat that venv or uv writes; if it is gone, recreate the venv")
			}
			if files == nil {
				files = []string{}
			}
			if !dry && len(files) > 0 {
				if old != "" && !envmgr.SamePath(old, e.Path) {
					reg.Remove(old)
				}
				reg.Upsert(e)
				if err := reg.Save(); err != nil {
					return Fail(exitFail, "repaired, but the registry could not be saved: "+err.Error(), "run `venvctl scan` to resync it")
				}
			}
			if opt.json {
				if err := emit(struct {
					Name    string   `json:"name"`
					Path    string   `json:"path"`
					OldPath string   `json:"old_path"`
					Files   []string `json:"files"`
					DryRun  bool     `json:"dry_run"`
					Changed bool     `json:"changed"`
				}{e.Name, e.Path, old, files, dry, !dry && len(files) > 0}); err != nil {
					return err
				}
			}
			switch {
			case len(files) == 0:
				return nothing(e.Name + " already points at " + e.Path)
			case dry:
				say("would rewrite %s → %s in:", old, e.Path)
				for _, f := range files {
					say("  %s", f)
				}
			default:
				say("%s %s  %s → %s, %d files rewritten", okMark(), e.Name, dim(old), e.Path, len(files))
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&dry, "dry-run", "n", false, "list the files that would be rewritten, change nothing")
	return c
}

// ---- formatting and parsing ----

func human(n int64) string {
	const kb = 1024
	switch {
	case n >= kb*kb*kb:
		return fmt.Sprintf("%.1f GB", float64(n)/(kb*kb*kb))
	case n >= kb*kb:
		return fmt.Sprintf("%.0f MB", float64(n)/(kb*kb))
	case n >= kb:
		return fmt.Sprintf("%.0f KB", float64(n)/kb)
	}
	return fmt.Sprintf("%d B", n)
}

func age(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month")
	}
	return plural(int(d.Hours()/24/365), "year")
}

// commas formats 12345 as 12,345.
func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// parseSize reads 500MB, 1.5GB, 2048 (bytes); units are 1024-based and case-insensitive.
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, u := range []struct {
		suffix string
		mul    float64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("bad size %q", s)
			}
			return int64(n * u.mul), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return n, nil
}

// parseAge reads 90d, 2w, 36h (and anything time.ParseDuration accepts).
func parseAge(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if num, ok := strings.CutSuffix(s, suffix); ok {
			n, err := strconv.ParseFloat(num, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("bad age %q", s)
			}
			return time.Duration(n * float64(unit)), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("bad age %q", s)
	}
	return d, nil
}
