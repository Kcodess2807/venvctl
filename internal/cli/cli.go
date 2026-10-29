// Package cli wires the cobra commands.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"

	"pygo/internal/discovery"
	"pygo/internal/doctor"
	"pygo/internal/envmgr"
	"pygo/internal/shell"
	"pygo/internal/winapi"
)

var (
	okMark   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
	badMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗")
	warnMark = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("!")
	dim      = lipgloss.NewStyle().Faint(true)
	bold     = lipgloss.NewStyle().Bold(true)
)

// Execute runs pygo with args and prints any error.
func Execute(args []string) error {
	root := newRoot()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, badMark, err)
		return err
	}
	return nil
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "pygo",
		Short:         "pyGo: fast Windows-native Python virtual environment manager",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true

	needHook := func(c *cobra.Command, _ []string) error {
		return fmt.Errorf("`pygo %s` needs the shell hook; run `pygo init`, restart your shell, then retry", c.Name())
	}
	root.AddCommand(
		&cobra.Command{Use: "on [name|path]", Aliases: []string{"activate"}, Short: "Activate a venv (nearest .venv, a central name, or a path)", Args: cobra.MaximumNArgs(1), RunE: needHook},
		&cobra.Command{Use: "off", Aliases: []string{"deactivate"}, Short: "Deactivate the current venv", Args: cobra.NoArgs, RunE: needHook},
		&cobra.Command{Use: "cd <name|path>", Short: "Jump to a venv's project directory", Args: cobra.ExactArgs(1), RunE: needHook},
		initCmd(), createCmd(), listCmd(), scanCmd(), nukeCmd(), repairCmd(), doctorCmd(), shellCmd(),
	)
	return root
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "pygo.exe"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [pwsh|cmd|bash]",
		Short: "Install shell hooks (no arg: all detected shells; with a shell: print its hook to eval)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				s, err := shell.Init(args[0], exePath())
				if err != nil {
					return err
				}
				fmt.Print(s)
				return nil
			}
			done, err := shell.Install(exePath())
			for _, d := range done {
				fmt.Println(okMark, d)
			}
			if len(done) > 0 {
				fmt.Println(dim.Render("restart your shell, then: pygo on"))
			}
			return err
		},
	}
}

func createCmd() *cobra.Command {
	var py string
	var local bool
	c := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a venv (uv if installed, else python -m venv); central by default, --local for .\\.venv",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if py == "" {
				py = envmgr.LoadConfig().DefaultPython
			}
			e, err := envmgr.Create(name, py, local)
			if err != nil {
				return err
			}
			reg := envmgr.LoadRegistry()
			reg.Upsert(e)
			if err := reg.Save(); err != nil {
				return err
			}
			fmt.Println(okMark, bold.Render(e.Name), dim.Render(e.Path), dim.Render("python "+e.Version))
			fmt.Println(dim.Render("activate with: pygo on " + e.Name))
			return nil
		},
	}
	c.Flags().StringVarP(&py, "python", "p", "", "Python version (e.g. 3.12) or interpreter path")
	c.Flags().BoolVarP(&local, "local", "l", false, "create .\\.venv in the current directory")
	return c
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List known venvs with size and status",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			reg := envmgr.LoadRegistry()
			if len(reg.Envs) == 0 {
				fmt.Println(dim.Render("no environments known yet; run `pygo scan` or `pygo create <name>`"))
				return nil
			}
			sizes := make([]string, len(reg.Envs))
			var wg sync.WaitGroup
			var total int64
			var mu sync.Mutex
			for i, e := range reg.Envs {
				wg.Add(1)
				go func(i int, e envmgr.Entry) {
					defer wg.Done()
					if _, err := os.Stat(filepath.Join(e.Path, "pyvenv.cfg")); err != nil {
						sizes[i] = "missing"
						return
					}
					n := envmgr.Size(e.Path)
					mu.Lock()
					total += n
					mu.Unlock()
					sizes[i] = human(n)
					if e.Python != "" {
						if _, err := os.Stat(filepath.Join(e.Python, "python.exe")); err != nil {
							sizes[i] += " broken-base"
						}
					}
				}(i, e)
			}
			wg.Wait()
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, bold.Render("NAME\tPYTHON\tSIZE\tLAST USED\tPATH"))
			for i, e := range reg.Envs {
				where := e.Path
				if e.Central {
					where = dim.Render("central")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Name, e.Version, sizes[i], age(e.LastUsed), where)
			}
			tw.Flush()
			fmt.Println(dim.Render(fmt.Sprintf("%d environments, %s", len(reg.Envs), human(total))))
			return nil
		},
	}
}

func scanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan [root...]",
		Short: "Find every .venv on disk and register it (default: config scan_roots or all fixed drives)",
		RunE: func(_ *cobra.Command, args []string) error {
			roots := args
			if len(roots) == 0 {
				roots = envmgr.LoadConfig().ScanRoots
			}
			if len(roots) == 0 {
				roots = discovery.FixedDrives()
			}
			roots = append(roots, envmgr.CentralDir()) // AppData is skipped during the walk
			fmt.Fprintln(os.Stderr, dim.Render("scanning "+strings.Join(roots, " ")))
			start := time.Now()
			found := discovery.Scan(roots)

			reg := envmgr.LoadRegistry()
			known := map[string]bool{}
			for _, e := range reg.Envs {
				known[strings.ToLower(filepath.Clean(e.Path))] = true
			}
			added := 0
			for _, v := range found {
				e, ok := envmgr.FromDir(v.Path)
				if !ok {
					continue
				}
				if !known[strings.ToLower(filepath.Clean(e.Path))] {
					added++
					fmt.Println(okMark, e.Name, dim.Render(e.Path))
				}
				reg.Upsert(e)
			}
			removed := 0
			for _, e := range append([]envmgr.Entry(nil), reg.Envs...) {
				if _, err := os.Stat(filepath.Join(e.Path, "pyvenv.cfg")); err != nil {
					reg.Remove(e.Path)
					removed++
					fmt.Println(badMark, e.Name, dim.Render(e.Path+" (gone, unregistered)"))
				}
			}
			if err := reg.Save(); err != nil {
				return err
			}
			fmt.Println(dim.Render(fmt.Sprintf("%d venvs found, %d new, %d removed, in %s", len(found), added, removed, time.Since(start).Round(time.Millisecond))))
			return nil
		},
	}
}

func nukeCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:     "nuke <name|path>...",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete venvs; shows processes locking files (Restart Manager), --force kills them first",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) (err error) {
			reg := envmgr.LoadRegistry()
			defer func() {
				if serr := reg.Save(); err == nil {
					err = serr
				}
			}()
			for _, arg := range args {
				e, err := envmgr.Resolve(reg, arg)
				if err != nil {
					return err
				}
				if _, err := os.Stat(e.Path); err != nil {
					reg.Remove(e.Path)
					fmt.Println(okMark, e.Name, dim.Render("already gone, unregistered"))
					continue
				}
				lockers, err := winapi.LockingProcesses(e.Path)
				if err != nil {
					fmt.Fprintln(os.Stderr, warnMark, "lock detection failed:", err)
				}
				if len(lockers) > 0 {
					if err := killLockers(e.Path, lockers, force); err != nil {
						return err
					}
				}
				size := envmgr.Size(e.Path)
				if err := envmgr.Delete(e.Path); err != nil {
					return fmt.Errorf("delete %s: %w", e.Path, err)
				}
				reg.Remove(e.Path)
				fmt.Println(okMark, "nuked", bold.Render(e.Name), dim.Render(e.Path), dim.Render("freed "+human(size)))
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&force, "force", "f", false, "terminate locking processes without asking")
	return c
}

// killLockers shows who holds files in dir, then kills them after confirmation (or --force).
// Editors and shells are never killed.
func killLockers(dir string, lockers []winapi.Locker, force bool) error {
	tw := tabwriter.NewWriter(os.Stderr, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, bold.Render("PID\tPROCESS\tSPAWNED BY\tEXE"))
	var blocked []string
	for _, l := range lockers {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", l.PID, l.Name, l.Parent, dim.Render(l.Exe))
		if l.Protected() {
			blocked = append(blocked, fmt.Sprintf("%s (PID %d)", filepath.Base(l.Exe), l.PID))
		}
	}
	tw.Flush()
	if len(blocked) > 0 {
		return fmt.Errorf("%s hold files in %s; pygo will not kill editors or shells, close them first", strings.Join(blocked, ", "), dir)
	}
	if !force {
		if !interactive() {
			return fmt.Errorf("%d process(es) hold files in %s; close them or rerun with --force", len(lockers), dir)
		}
		fmt.Fprintf(os.Stderr, "kill %d process(es)? [y/N] ", len(lockers))
		var ans string
		fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			return errors.New("aborted, nothing deleted")
		}
	}
	for _, l := range lockers {
		if err := winapi.Kill(l.PID); err != nil {
			fmt.Fprintln(os.Stderr, warnMark, "kill", l.PID, err)
		}
	}
	return nil
}

// interactive reports whether stdin is a console a person can answer on.
func interactive() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}

func repairCmd() *cobra.Command {
	var dry bool
	c := &cobra.Command{
		Use:   "repair [path]",
		Short: "Fix a moved/renamed venv: rewrite embedded paths in Scripts\\ and update the registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			reg := envmgr.LoadRegistry()
			e, err := envmgr.Resolve(reg, target)
			if err != nil {
				return err
			}
			old, files, err := envmgr.Repair(e.Path, !dry)
			if err != nil {
				return err
			}
			if dry {
				fmt.Println(dim.Render(fmt.Sprintf("would rewrite %s -> %s in:", old, e.Path)))
				for _, f := range files {
					fmt.Println("  " + f)
				}
				return nil
			}
			if old != "" && !envmgr.SamePath(old, e.Path) {
				reg.Remove(old)
			}
			reg.Upsert(e)
			if err := reg.Save(); err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Println(okMark, e.Name, dim.Render("already points at "+e.Path))
				return nil
			}
			fmt.Println(okMark, e.Name, dim.Render(fmt.Sprintf("%s -> %s, %d files rewritten", old, e.Path, len(files))))
			return nil
		},
	}
	c.Flags().BoolVarP(&dry, "dry-run", "n", false, "list the files that would be rewritten, change nothing")
	return c
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose PATH, Store stubs, ExecutionPolicy, long paths, VC++ runtime, uv, hooks",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fails := 0
			for _, c := range doctor.Run() {
				mark := okMark
				switch {
				case c.Warn:
					mark = warnMark
				case !c.OK:
					mark = badMark
					fails++
				}
				fmt.Printf("%s %s  %s\n", mark, bold.Render(c.Name), dim.Render(c.Detail))
				if c.Fix != "" {
					fmt.Printf("    %s\n", c.Fix)
				}
			}
			if fails > 0 {
				return fmt.Errorf("%d check(s) failed", fails)
			}
			return nil
		},
	}
}

// shellCmd is called by the shell wrappers. For pwsh/bash it prints a script to eval.
// For cmd it always writes the env file the doskey macro calls, and runs non-hook commands itself.
func shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "__shell <pwsh|cmd|bash> [args...]",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			sh, rest := args[0], args[1:]
			script, handled, err := hookScript(sh, rest)
			if sh != "cmd" {
				if err != nil {
					return err
				}
				fmt.Print(script)
				return nil
			}
			os.MkdirAll(envmgr.Dir(), 0o755)
			if werr := os.WriteFile(shell.EnvFile(), []byte(script), 0o644); werr != nil && err == nil {
				err = werr
			}
			if handled || err != nil {
				return err
			}
			r := newRoot()
			r.SetArgs(rest)
			return r.Execute()
		},
	}
}

func hookScript(sh string, args []string) (script string, handled bool, err error) {
	if len(args) == 0 || !shell.Handled(args[0]) {
		return "", false, nil
	}
	target := ""
	if len(args) > 1 {
		target = args[1]
	}
	reg := envmgr.LoadRegistry()
	switch args[0] {
	case "off", "deactivate":
		return shell.Deactivate(sh), true, nil
	case "cd":
		e, err := envmgr.Resolve(reg, target)
		if err != nil {
			return "", true, err
		}
		dir := e.Path
		if b := strings.ToLower(filepath.Base(dir)); b == ".venv" || b == "venv" {
			dir = filepath.Dir(dir)
		}
		return shell.Cd(sh, dir), true, nil
	default:
		e, err := envmgr.Resolve(reg, target)
		if err != nil {
			return "", true, err
		}
		e.LastUsed = time.Now()
		reg.Upsert(e)
		reg.Save()
		return shell.Activate(sh, e.Path, e.Name), true, nil
	}
}

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
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
