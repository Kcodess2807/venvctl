package cli

import (
	"os"
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
		return shell.Deactivate(sh), true, nil
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
		return shell.Activate(sh, e.Path, e.Name), true, nil
	}
}
