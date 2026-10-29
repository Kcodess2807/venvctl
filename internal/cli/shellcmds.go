package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"venvctl/internal/envmgr"
	"venvctl/internal/shell"
)

// needHook runs when on/off/cd reach venvctl.exe directly, meaning the shell function is not loaded.
func needHook(c *cobra.Command, _ []string) error {
	return Fail(exitFail, "`venvctl "+c.Name()+"` must change this shell, which needs the venvctl shell hook (not loaded here)",
		"run `venvctl init`, open a new terminal, then retry; in PowerShell you can load it now with: venvctl init pwsh | Out-String | iex")
}

func onCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "on [name|path]",
		Aliases: []string{"activate"},
		GroupID: "use",
		Short:   "Activate a venv: the nearest .venv, a registered name, or a path",
		Example: `  venvctl on
  venvctl on api
  venvctl on D:\PROJECTS\x\.venv`,
		Args: nargs(cobra.MaximumNArgs(1)),
		RunE: needHook,
	}
}

func offCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "off",
		Aliases: []string{"deactivate"},
		GroupID: "use",
		Short:   "Deactivate the venv venvctl activated; restores PATH and prompt",
		Example: `  venvctl off`,
		Args:    nargs(cobra.NoArgs),
		RunE:    needHook,
	}
}

func cdCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "cd <name|path>",
		GroupID: "use",
		Short:   "Change this shell to a venv's project folder",
		Example: `  venvctl cd api
  venvctl cd gravity-render`,
		Args: nargs(cobra.ExactArgs(1)),
		RunE: needHook,
	}
}

func initCmd() *cobra.Command {
	var install, dry, remove bool
	c := &cobra.Command{
		Use:     "init [pwsh|powershell|bash|cmd]",
		GroupID: "setup",
		Short:   "Install the shell hooks that make on/off/cd work (and remove old WinVenv/pyGo hooks)",
		Long: `With no argument, updates every detected shell: each PowerShell $PROFILE (5.1 and 7), the CMD
AutoRun registry value, and ~/.bashrc for Git Bash. In each it first removes hooks left by the
earlier WinVenv (wv) and pyGo (pygo) versions, then adds the venvctl hook. Other lines and
other AutoRun commands (such as VS Code's) are kept as they are.

"venvctl init pwsh" and "venvctl init bash" print the hook code that those profile lines evaluate;
add --install to update just that shell. CMD cannot evaluate code, so "venvctl init cmd" installs.`,
		Example: `  venvctl init --dry-run
  venvctl init
  venvctl init --remove
  venvctl init pwsh | Out-String | Invoke-Expression`,
		ValidArgs: []string{"pwsh", "powershell", "bash", "cmd"},
		Args:      nargs(cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs)),
		RunE: func(_ *cobra.Command, args []string) error {
			sh := ""
			if len(args) == 1 {
				sh = args[0]
			}
			if sh != "" && sh != "cmd" && !install && !dry && !remove {
				s, _ := shell.Init(sh, exePath())
				_, err := stdout.Write([]byte(s))
				return err
			}
			changes, err := shell.Install(exePath(), shell.Options{Only: sh, DryRun: dry, Remove: remove})
			if opt.json {
				type changeJSON struct {
					Shell   string         `json:"shell"`
					Target  string         `json:"target"`
					Removed map[string]int `json:"removed"`
					Added   bool           `json:"added"`
					Changed bool           `json:"changed"`
					Report  string         `json:"report"`
				}
				out := []changeJSON{}
				for _, ch := range changes {
					out = append(out, changeJSON{ch.Shell, ch.Target, ch.Removed, ch.Added, ch.Changed, ch.String()})
				}
				emit(struct {
					DryRun  bool         `json:"dry_run"`
					Changes []changeJSON `json:"changes"`
				}{dry, out})
			}
			if dry {
				say(dim("dry run, nothing written:"))
			}
			changed := false
			for _, ch := range changes {
				mark := okMark()
				if !ch.Changed {
					mark = dim("-")
				}
				changed = changed || ch.Changed
				say("%s %s  %s", mark, ch, dim(ch.Target))
			}
			if err != nil {
				return Fail(exitFail, "some shells were not updated: "+err.Error(),
					"fix the file or registry access problem above and rerun `venvctl init`")
			}
			switch {
			case dry && changed:
				say(dim("run `venvctl init` to apply"))
			case remove && changed:
				say(dim("hooks removed; open a new terminal for it to take effect"))
			case changed:
				say(dim("open a new terminal, then: venvctl on"))
			default:
				return nothing("hooks already up to date")
			}
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&install, "install", false, "with pwsh or bash: update that shell's profile instead of printing the hook")
	f.BoolVarP(&dry, "dry-run", "n", false, "print what would be removed and added, write nothing")
	f.BoolVar(&remove, "remove", false, "remove venvctl and legacy hooks, add nothing (uninstall)")
	return c
}

// rootArg is the folder allow/deny act on: the given one, else the auto-activate root of the
// current folder, else the current folder.
func rootArg(args []string) string {
	if len(args) == 1 {
		abs, err := filepath.Abs(args[0])
		if err == nil {
			return abs
		}
		return args[0]
	}
	cwd, _ := os.Getwd()
	if r := envmgr.FindRoot(cwd); r != "" {
		return r
	}
	return cwd
}

func allowCmd() *cobra.Command {
	var list bool
	c := &cobra.Command{
		Use:     "allow [dir]",
		GroupID: "setup",
		Short:   "Trust a project root so its venv auto-activates when you cd into it",
		Long: `Records a project root in trust.json. Only trusted roots auto-activate: putting a folder's
Scripts on PATH runs its code as soon as you type python, so venvctl asks you first.
Without dir it trusts the root of the current folder: the closest folder (up to 12 levels,
stopping at .git) with a .venv or venv, else with pyproject.toml.
Auto-activate itself is off until "venvctl config auto-activate on".`,
		Example: `  venvctl allow
  venvctl allow D:\PROJECTS\api
  venvctl allow --list`,
		Args: nargs(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			if list {
				t := envmgr.Trusted()
				if t == nil {
					t = []string{}
				}
				if opt.json {
					emit(struct {
						Trusted []string `json:"trusted"`
					}{t})
				}
				for _, p := range t {
					say("%s", filepath.FromSlash(p))
				}
				if len(t) == 0 {
					return nothing("no trusted roots yet; run `venvctl allow` in a project")
				}
				return nil
			}
			dir := rootArg(args)
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return Fail(exitUsage, dir+" is not a folder", "pass an existing project folder")
			}
			changed, err := envmgr.SetTrust(dir, true)
			if err != nil {
				return Fail(exitFail, "could not save "+envmgr.TrustFile()+": "+err.Error(), "check that the venvctl state folder is writable")
			}
			if !changed {
				return nothing(dir + " is already trusted")
			}
			say("%s trusted %s", okMark(), dir)
			if !envmgr.HasVenv(dir) {
				say(dim("no venv in it yet; it will auto-activate once it has a .venv"))
			}
			if !shell.AutoEnabled() {
				say(dim("auto-activate is off: run `venvctl config auto-activate on`, then open a new terminal"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&list, "list", false, "list trusted roots")
	return c
}

func denyCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "deny [dir]",
		GroupID: "setup",
		Short:   "Stop trusting a project root (it no longer auto-activates)",
		Example: `  venvctl deny
  venvctl deny D:\PROJECTS\api`,
		Args: nargs(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := rootArg(args)
			changed, err := envmgr.SetTrust(dir, false)
			if err != nil {
				return Fail(exitFail, "could not save "+envmgr.TrustFile()+": "+err.Error(), "check that the venvctl state folder is writable")
			}
			if !changed {
				return nothing(dir + " was not trusted")
			}
			say("%s no longer trusted: %s %s", okMark(), dir, dim("(a venv it already activated stays until you leave or run venvctl off)"))
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "config [key [value]]",
		GroupID: "setup",
		Short:   "Show or change settings (auto-activate on|off)",
		Long: `Without arguments prints the settings in config.json. Keys you can set:
  auto-activate on|off   activate trusted projects' venvs on cd (new terminals pick it up)`,
		Example: `  venvctl config
  venvctl config auto-activate on
  venvctl --json config`,
		Args: nargs(cobra.MaximumNArgs(2)),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg := envmgr.LoadConfig()
			if len(args) == 0 {
				if opt.json {
					return emit(cfg)
				}
				say("auto-activate   %s", map[bool]string{true: "on", false: "off"}[cfg.AutoActivate])
				say("default-python  %s", orDash(cfg.DefaultPython))
				say("scan-roots      %s", orDash(strings.Join(cfg.ScanRoots, ", ")))
				say(dim(filepath.Join(envmgr.Dir(), "config.json")))
				return nil
			}
			if args[0] != "auto-activate" {
				return Fail(exitUsage, "unknown setting "+args[0], "settable keys: auto-activate (others: edit config.json)")
			}
			if len(args) == 1 {
				say("%s", map[bool]string{true: "on", false: "off"}[cfg.AutoActivate])
				return nil
			}
			var on bool
			switch strings.ToLower(args[1]) {
			case "on", "true", "1":
				on = true
			case "off", "false", "0":
			default:
				return Fail(exitUsage, "auto-activate takes on or off, not "+args[1], "run `venvctl config auto-activate on`")
			}
			if on == cfg.AutoActivate {
				return nothing("auto-activate is already " + args[1])
			}
			if err := envmgr.SetConfig("auto_activate", on); err != nil {
				return Fail(exitFail, err.Error(), "fix config.json, or delete it to start over")
			}
			if on {
				say("%s auto-activate on: open a new terminal; trusted roots (`venvctl allow`) activate on cd", okMark())
				if shell.HasClink() {
					say(dim("for CMD run `venvctl init cmd` once to install the Clink script"))
				}
			} else {
				say("%s auto-activate off for new terminals", okMark())
			}
			return nil
		},
	}
}

// benchHookCmd times the PowerShell hook's pure-PowerShell path (no spawn) 1,000 times.
func benchHookCmd() *cobra.Command {
	n := 1000
	c := &cobra.Command{
		Use:    "bench-hook",
		Hidden: true,
		Short:  "Time the PowerShell auto-activate hook without spawning venvctl",
		Args:   nargs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			tmp, err := os.MkdirTemp("", "venvctl-bench-")
			if err != nil {
				return Fail(exitFail, err.Error(), "check %TEMP%")
			}
			defer os.RemoveAll(tmp)
			root := filepath.Join(tmp, "proj")
			deep, side := filepath.Join(root, "a", "b", "c", "d"), filepath.Join(root, "x")
			os.MkdirAll(deep, 0o755)
			os.MkdirAll(side, 0o755)
			os.MkdirAll(filepath.Join(tmp, "home"), 0o755)
			os.WriteFile(filepath.Join(root, ".venv"), []byte(`C:\nowhere`+"\n"), 0o644) // untrusted: never spawns
			script := fmt.Sprintf(`
$env:VENVCTL_HOME = '%[1]s'; $env:VENVCTL_AUTO_ACTIVATE = '1'
& '%[2]s' init pwsh | Out-String | Invoke-Expression
function pct($l, $p) { $s = $l | Sort-Object; '{0:N3}' -f $s[[int][math]::Floor($p * ($s.Count - 1))] }
$sw = [Diagnostics.Stopwatch]::new()
Set-Location -LiteralPath '%[3]s'; __venvctl_auto *> $null
$same = [Collections.Generic.List[double]]::new()
for ($i = 0; $i -lt %[5]d; $i++) { $sw.Restart(); __venvctl_auto; $sw.Stop(); $same.Add($sw.Elapsed.TotalMilliseconds) }
$walk = [Collections.Generic.List[double]]::new()
for ($i = 0; $i -lt %[5]d; $i++) {
    if ($i %% 2) { Set-Location -LiteralPath '%[3]s' } else { Set-Location -LiteralPath '%[4]s' }
    $sw.Restart(); __venvctl_auto; $sw.Stop(); $walk.Add($sw.Elapsed.TotalMilliseconds)
}
"folder unchanged          p50 $(pct $same 0.5) ms   p99 $(pct $same 0.99) ms   (target p99 < 2 ms)"
"folder changed, same root p50 $(pct $walk 0.5) ms   p99 $(pct $walk 0.99) ms   (walks up 5 levels)"
`, filepath.Join(tmp, "home"), exePath(), deep, side, n)
			ps := "powershell"
			if _, err := exec.LookPath(ps); err != nil {
				ps = "pwsh"
			}
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script)
			cmd.Stdout, cmd.Stderr = stdout, stderr
			say(dim(fmt.Sprintf("%s, %d runs each, venvctl.exe never spawned (root untrusted)", ps, n)))
			if err := cmd.Run(); err != nil {
				return Fail(exitFail, "benchmark failed: "+err.Error(), "run it from a normal console")
			}
			return nil
		},
	}
	c.Flags().IntVar(&n, "runs", 1000, "runs per scenario")
	return c
}

// shellCmd is called by the shell wrappers. For pwsh/bash it prints a script to evaluate.
// For cmd it always writes the env file the doskey macro calls, and runs other commands itself.
func shellCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "__shell <pwsh|cmd|bash> [args...]",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               nargs(cobra.MinimumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			sh, rest := args[0], args[1:]
			script, handled, err := hookScript(sh, rest)
			if sh != "cmd" {
				if err != nil {
					return err
				}
				_, werr := stdout.Write([]byte(script))
				return werr
			}
			os.MkdirAll(envmgr.Dir(), 0o755)
			if werr := os.WriteFile(shell.EnvFile(), []byte(script), 0o644); werr != nil && err == nil {
				err = Fail(exitFail, "could not write "+shell.EnvFile()+": "+werr.Error(), "check that %LOCALAPPDATA%\\venvctl is writable")
			}
			if handled || err != nil {
				return err
			}
			return done(Run(rest, stdout, stderr, stdin))
		},
	}
}

// hookScript builds the script for on/off/cd. handled is false for every other command.
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
		return shell.DeactivateFor(sh), true, nil
	case "auto":
		return autoScript(reg, sh, target), true, nil
	case "cd":
		if target == "" {
			return "", true, Fail(exitUsage, "`venvctl cd` needs a name or path", "run `venvctl cd <name>`; `venvctl list` shows names")
		}
		e, err := resolve(reg, target, "cd")
		if err != nil {
			return "", true, err
		}
		dir := e.Path
		if e.Project != "" {
			dir = e.Project
		} else if b := strings.ToLower(filepath.Base(dir)); b == ".venv" || b == "venv" {
			dir = filepath.Dir(dir)
		}
		return shell.Cd(sh, dir), true, nil
	default:
		e, err := resolve(reg, target, "on")
		if err != nil {
			return "", true, err
		}
		e.LastUsed = time.Now()
		reg.Upsert(e)
		reg.Save()
		// A manual `on` is the user's choice: clear the auto mark so leaving the folder keeps it.
		return shell.ActivateFor(sh, e.Path, e.Name) + shell.SetAuto(sh, false), true, nil
	}
}

// autoScript is what the auto-activate hook evaluates after the project root changed to root
// ("" = none). It activates root's venv when root is trusted, switches away from a venv it
// auto-activated earlier, and never touches a venv someone else activated.
func autoScript(reg *envmgr.Registry, sh, root string) string {
	auto := os.Getenv("_VENVCTL_AUTO") == "1"
	active := os.Getenv("VIRTUAL_ENV")
	var e envmgr.Entry
	found := false
	if root != "" && envmgr.IsTrusted(root) {
		for _, n := range []string{".venv", "venv"} {
			if e, found = envmgr.FromDir(filepath.Join(root, n)); found {
				e = reg.Merge(e)
				break
			}
		}
	}
	switch {
	case found && active != "" && envmgr.SamePath(active, e.Path):
		return "" // already active
	case found && active != "" && !auto:
		return "" // activated by the user or an editor: leave it alone
	case found:
		e.LastUsed = time.Now()
		reg.Upsert(e)
		reg.Save()
		return shell.ActivateFor(sh, e.Path, e.Name) + shell.SetAuto(sh, true)
	case auto:
		return shell.DeactivateFor(sh) // left the root venvctl auto-activated
	}
	return ""
}
