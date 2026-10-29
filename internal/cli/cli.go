// Package cli wires the cobra commands, output modes (human, --json, --quiet) and exit codes.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"

	"venvctl/internal/doctor"
	"venvctl/internal/envmgr"
	"venvctl/internal/shell"
)

// Exit codes. Keep in sync with exitHelp.
const (
	exitOK      = 0
	exitFail    = 1
	exitUsage   = 2
	exitRefused = 3
	exitNothing = 4
)

const exitHelp = `Exit codes:
  0  success (doctor warnings included)
  1  failure
  2  usage error: unknown command, bad flag or argument
  3  refused: venvctl declined to act, e.g. an editor holds the venv or you answered no
  4  nothing to do: no matching venvs, or nothing needed changing`

// Set at build time: -ldflags "-X venvctl/internal/cli.version=1.2.3 -X venvctl/internal/cli.buildDate=2026-10-29"
var (
	version   = "dev"
	buildDate = ""
)

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	stdin  io.Reader = os.Stdin
	opt    options
	color  bool
)

type options struct{ json, quiet, noColor bool }

// Run executes venvctl with args and returns the process exit code.
func Run(args []string, out, errOut io.Writer, in io.Reader) int {
	stdout, stderr, stdin, opt, color = out, errOut, in, options{}, false
	root := newRoot()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetIn(in)
	return report(root.Execute())
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "venvctl",
		Short: "venvctl — control your Python virtual environments on Windows",
		Long: "venvctl — control your Python virtual environments on Windows\n\n" +
			"Activates, finds, deletes, repairs and diagnoses venvs in PowerShell, CMD and Git Bash.\n" +
			"Run `venvctl` with no arguments for a status overview.\n\n" + exitHelp,
		Example: `  venvctl                    status overview
  venvctl on                 activate the nearest .venv
  venvctl list --sort size   biggest venvs first
  venvctl --json doctor      machine-readable diagnosis`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			color = useColor()
			migrate()
		},
		RunE: func(*cobra.Command, []string) error { return overview() },
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&opt.json, "json", false, "machine-readable JSON on stdout; no styling, no prompts")
	pf.BoolVarP(&opt.quiet, "quiet", "q", false, "print errors only")
	pf.BoolVar(&opt.noColor, "no-color", false, "no colors (also when NO_COLOR is set or output is not a terminal)")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return Fail(exitUsage, err.Error(), "see `"+c.CommandPath()+" --help`")
	})
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddGroup(
		&cobra.Group{ID: "use", Title: "Use:"},
		&cobra.Group{ID: "find", Title: "Find:"},
		&cobra.Group{ID: "maintain", Title: "Maintain:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	root.SetHelpCommandGroupID("setup")
	root.AddCommand(
		onCmd(), offCmd(), cdCmd(), createCmd(), linkCmd(), unlinkCmd(),
		listCmd(), scanCmd(),
		nukeCmd(), pruneCmd(), repairCmd(), doctorCmd(),
		initCmd(), allowCmd(), denyCmd(), configCmd(), versionCmd(), completionCmd(root),
		benchHookCmd(),
		shellCmd(),
	)
	return root
}

// ---- errors and exit codes ----

type exitError struct {
	code     int
	msg, fix string
	info     bool // exit 4: msg is a note, not an error
	printed  bool // already reported (per-item errors, nested Run)
}

func (e *exitError) Error() string { return e.msg }

// Fail returns an error that prints "✗ msg" then "  → fix", and makes venvctl exit with code.
func Fail(code int, msg, fix string) error { return &exitError{code: code, msg: msg, fix: fix} }

// nothing ends a command with exit 4 and msg shown as a plain note.
func nothing(msg string) error { return &exitError{code: exitNothing, msg: msg, info: true} }

// done ends a command with code after its errors were already printed.
func done(code int) error {
	if code == exitOK {
		return nil
	}
	return &exitError{code: code, printed: true}
}

func report(err error) int {
	if err == nil {
		return exitOK
	}
	var e *exitError
	if !errors.As(err, &e) {
		e = classify(err)
	}
	switch {
	case e.printed:
	case e.info:
		note("%s", dim(e.msg))
	default:
		printErr(e)
	}
	return e.code
}

// printErr shows one error without ending the command: JSON on stderr in --json mode.
func printErr(e *exitError) {
	if opt.json {
		json.NewEncoder(stderr).Encode(struct {
			Error    string `json:"error"`
			Fix      string `json:"fix"`
			ExitCode int    `json:"exit_code"`
		}{e.msg, e.fix, e.code})
		return
	}
	fmt.Fprintln(stderr, badMark()+" "+e.msg)
	if e.fix != "" {
		fmt.Fprintln(stderr, "  → "+e.fix)
	}
}

// classify turns errors that did not go through Fail (cobra's own) into an exitError.
func classify(err error) *exitError {
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "invalid argument",
		"accepts ", "requires at least", "requires exactly", "flag needs an argument"} {
		if strings.HasPrefix(msg, p) {
			return &exitError{code: exitUsage, msg: msg, fix: "see `venvctl --help`"}
		}
	}
	return &exitError{code: exitFail, msg: msg, fix: "unexpected error; rerun, and if it repeats report it with the command and this output"}
}

// nargs makes a cobra argument validator fail with exit 2 and a help pointer.
func nargs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, a []string) error {
		if err := v(c, a); err != nil {
			return Fail(exitUsage, err.Error(), "see `"+c.CommandPath()+" --help`")
		}
		return nil
	}
}

// ---- output ----

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	faint  = lipgloss.NewStyle().Faint(true)
	strong = lipgloss.NewStyle().Bold(true)
)

func paint(s lipgloss.Style, text string) string {
	if !color {
		return text
	}
	return s.Render(text)
}

func okMark() string       { return paint(green, "✓") }
func badMark() string      { return paint(red, "✗") }
func warnMark() string     { return paint(yellow, "!") }
func dim(s string) string  { return paint(faint, s) }
func bold(s string) string { return paint(strong, s) }

// useColor: off for --no-color, --json, NO_COLOR, or stdout that is not a console. It also
// switches the console to VT mode so ANSI codes render; failing that, no color.
func useColor() bool {
	if opt.noColor || opt.json || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

// isConsole reports whether w is an attached Windows console.
func isConsole(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

// say prints human output to stdout (silent with --quiet or --json).
func say(format string, a ...any) {
	if !opt.quiet && !opt.json {
		fmt.Fprintf(stdout, format+"\n", a...)
	}
}

// note prints human progress/notes to stderr (silent with --quiet or --json).
func note(format string, a ...any) {
	if !opt.quiet && !opt.json {
		fmt.Fprintf(stderr, format+"\n", a...)
	}
}

// emit writes v as indented JSON to stdout.
func emit(v any) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// table prints rows under a bold header, padding by visible width so styled cells align.
// right lists the column indexes to right-align.
func table(w io.Writer, header []string, rows [][]string, right ...int) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], lipgloss.Width(c))
		}
	}
	isRight := map[int]bool{}
	for _, i := range right {
		isRight[i] = true
	}
	line := func(cells []string, style func(string) string) {
		var b strings.Builder
		for i, c := range cells {
			pad := strings.Repeat(" ", widths[i]-lipgloss.Width(c))
			if style != nil {
				c = style(c)
			}
			switch {
			case isRight[i]:
				b.WriteString(pad + c)
			case i < len(cells)-1:
				b.WriteString(c + pad)
			default:
				b.WriteString(c)
			}
			if i < len(cells)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(w, b.String())
	}
	line(header, bold)
	for _, r := range rows {
		line(r, nil)
	}
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "venvctl.exe"
	}
	return exe
}

// migrate moves pyGo's state into venvctl's folder the first time venvctl runs, and prints a
// one-time summary on stderr (so --json stdout stays clean). See envmgr.MigrateFromPygo.
func migrate() {
	if !envmgr.NeedsMigration() {
		return
	}
	m, err := envmgr.MigrateFromPygo()
	if opt.quiet && err == nil && len(m.LeftBehind)+len(m.RepairFailed) == 0 {
		return
	}
	w := stderr
	fmt.Fprintf(w, "%s first run: migrated pyGo state from %s to %s\n", okMark(), m.From, envmgr.Dir())
	if len(m.Copied) > 0 {
		fmt.Fprintf(w, "    copied %s\n", strings.Join(m.Copied, ", "))
	}
	for _, p := range m.Moved {
		fmt.Fprintf(w, "    moved and repaired central env %s\n", p)
	}
	for _, p := range m.LeftBehind {
		fmt.Fprintf(w, "%s left in place (registry still points there): %s\n", warnMark(), p)
	}
	for _, p := range m.RepairFailed {
		dir, _, _ := strings.Cut(p, ": ")
		fmt.Fprintf(w, "%s repair failed: %s\n    → run: venvctl repair \"%s\"\n", warnMark(), p, dir)
	}
	if err != nil {
		printErr(&exitError{code: exitFail, msg: "migration stopped: " + err.Error(),
			fix: "your pyGo folder is untouched apart from moved envs; fix the problem, delete " + envmgr.Dir() + `\registry.json and rerun`})
		return
	}
	fmt.Fprintf(w, "    the old folder %s is left for you to delete once `venvctl list` looks right\n", m.From)
}

// ---- version, completion, overview, doctor ----

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		GroupID: "setup",
		Short:   "Show the venvctl version, commit, build date and Go version",
		Example: `  venvctl version
  venvctl --json version`,
		Args: nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			v := struct {
				Version   string `json:"version"`
				Commit    string `json:"commit"`
				BuildDate string `json:"build_date"`
				Go        string `json:"go"`
				Exe       string `json:"exe"`
			}{version, "", buildDate, runtime.Version(), exePath()}
			if bi, ok := debug.ReadBuildInfo(); ok {
				for _, s := range bi.Settings {
					switch {
					case s.Key == "vcs.revision" && len(s.Value) >= 12:
						v.Commit = s.Value[:12]
					case s.Key == "vcs.modified" && s.Value == "true":
						v.Commit += "-dirty"
					}
				}
			}
			if opt.json {
				return emit(v)
			}
			say("venvctl %s", v.Version)
			say("  commit  %s", orDash(v.Commit))
			say("  built   %s", orDash(v.BuildDate))
			say("  go      %s", v.Go)
			say("  exe     %s", v.Exe)
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func completionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion powershell|bash",
		GroupID:   "setup",
		Short:     "Print a tab-completion script for PowerShell or Git Bash",
		ValidArgs: []string{"powershell", "bash"},
		Example: `  venvctl completion powershell | Out-String | Invoke-Expression
  venvctl completion powershell >> $PROFILE
  eval "$(venvctl completion bash)"`,
		Args: nargs(cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs)),
		RunE: func(_ *cobra.Command, args []string) error {
			if args[0] == "bash" {
				return root.GenBashCompletionV2(stdout, true)
			}
			return root.GenPowerShellCompletionWithDesc(stdout)
		},
	}
}

// overview is `venvctl` with no arguments: one screen of status.
func overview() error {
	reg := envmgr.LoadRegistry()
	var total int64
	for _, e := range reg.Envs {
		total += e.Size
	}
	active := os.Getenv("VIRTUAL_ENV")
	nearest := ""
	if e, err := envmgr.Resolve(reg, ""); err == nil {
		nearest = e.Path
	}
	hooks := shell.Status(exePath())
	if opt.json {
		type hookJSON struct {
			Shell  string `json:"shell"`
			State  string `json:"state"`
			Detail string `json:"detail"`
		}
		hj := []hookJSON{}
		for _, h := range hooks {
			hj = append(hj, hookJSON{h.Shell, h.State, h.Detail})
		}
		return emit(struct {
			Version    string     `json:"version"`
			Active     string     `json:"active"`
			Nearest    string     `json:"nearest"`
			Envs       int        `json:"envs"`
			TotalBytes int64      `json:"total_bytes"`
			Hooks      []hookJSON `json:"hooks"`
		}{version, active, nearest, len(reg.Envs), total, hj})
	}
	activeText := dim("no venv active")
	if active != "" {
		name := filepath.Base(active)
		for _, e := range reg.Envs {
			if envmgr.SamePath(e.Path, active) {
				name = e.Name
			}
		}
		activeText = paint(green, name) + "  " + dim(active)
	}
	nearText := dim("no .venv in this folder or its parents")
	if nearest != "" {
		nearText = nearest
	}
	var hs []string
	for _, h := range hooks {
		st := paint(green, h.State)
		if h.State != "ok" {
			st = paint(yellow, h.State)
		}
		hs = append(hs, h.Shell+" "+st)
	}
	size := "sizes unknown until `venvctl list`"
	if total > 0 {
		size = human(total) + dim(" (as of last list)")
	}
	say("%s %s  %s", bold("venvctl"), version, dim(exePath()))
	say("")
	say("  active   %s", activeText)
	say("  here     %s", nearText)
	say("  known    %d venvs, %s", len(reg.Envs), size)
	say("  hooks    %s", strings.Join(hs, ", "))
	say("")
	say("  venvctl on [name]       activate (nearest .venv by default)")
	say("  venvctl list            every venv with size and last use")
	say("  venvctl create <name>   new central venv (uses uv if installed)")
	say("  venvctl nuke <name>     delete a venv, unlocking its files first")
	say("  venvctl doctor          diagnose this machine")
	say("")
	say(dim("venvctl --help for all commands"))
	return nil
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		GroupID: "maintain",
		Short:   "Diagnose PATH, Store stubs, ExecutionPolicy, long paths, VC++, uv, hooks, cloud folders",
		Long:    "Runs read-only checks and prints a fix for each problem. Warnings exit 0; only failed checks exit 1.",
		Example: `  venvctl doctor
  venvctl --json doctor`,
		Args: nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			checks := doctor.Run(exePath())
			type checkJSON struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Detail string `json:"detail"`
				Fix    string `json:"fix"`
			}
			fails := 0
			out := []checkJSON{}
			for _, c := range checks {
				if c.Status() == "fail" {
					fails++
				}
				out = append(out, checkJSON{c.Name, c.Status(), c.Detail, c.Fix})
			}
			if opt.json {
				if err := emit(struct {
					Checks []checkJSON `json:"checks"`
					Failed int         `json:"failed"`
				}{out, fails}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					mark := map[string]string{"ok": okMark(), "warn": warnMark(), "fail": badMark()}[c.Status()]
					say("%s %s  %s", mark, bold(c.Name), dim(c.Detail))
					if c.Fix != "" {
						say("    → %s", c.Fix)
					}
				}
			}
			if fails > 0 {
				return Fail(exitFail, fmt.Sprintf("%d check(s) failed", fails), "apply the fixes above, then rerun `venvctl doctor`")
			}
			return nil
		},
	}
}
