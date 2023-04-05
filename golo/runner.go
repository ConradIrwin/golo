package golo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Runner runs go run/go build or go test with syntax errors and type errors
// deferred until runtime.
type Runner struct {
	mode    string
	verbose bool

	buildArgs []string
	runArgs   []string

	built       bool
	fixed       map[string][]byte
	overlays    packages.OverlayJSON
	overlayFile string
	exeFile     string
	cleanup     []string
}

// New returns a runner with the given args.
// These args should be what you might pass to a go subcommand of the same name as "mode"
// Valid modes are "run", "build" and "test".
// If verbose, more output will be generated (mostly useful for debugging golo itself)
func New(mode string, verbose bool, args []string) *Runner {
	r := &Runner{
		mode:      mode,
		verbose:   verbose,
		buildArgs: args,
		fixed:     map[string][]byte{},

		overlays: packages.OverlayJSON{Replace: map[string]string{}},
		built:    false,
	}

	if mode == "run" {
		if strings.HasSuffix(args[0], ".go") {
			i := 0
			for i < len(args) {
				if !strings.HasSuffix(args[i], ".go") {
					break
				}
				i++
			}
			r.buildArgs = args[0:i]
			r.runArgs = args[i:]
		} else {
			r.buildArgs = args[0:1]
			r.runArgs = args[1:]
		}
	}

	return r
}

// Prepare attempts the build, and (best-effort) fixes any build errors
func (r *Runner) Prepare() error {
	fixed := map[string]bool{}

	fixer := &Fixer{mode: r.mode, verbose: r.verbose, Fixed: r.fixed}
	for {
		toFix, err := r.getBrokenPackages()
		if err != nil {
			return err
		}

		if len(toFix) == 0 {
			r.built = true
			return nil
		}

		clidx := -1

		for i, pkg := range toFix {
			if fixed[pkg] {
				return nil
			}
			if pkg == "command-line-arguments" {
				clidx = i
			}
			fixed[pkg] = true
		}
		if clidx > -1 {
			toFix = append(toFix[0:clidx:clidx], toFix[clidx+1:]...)
			if err := fixer.Fix(r.buildArgs...); err != nil {
				return err
			}
		}
		if err := fixer.Fix(toFix...); err != nil {
			return err
		}
	}
}

var rePackage = regexp.MustCompile(`^# ([^\s]*)( \[.*\])?$`)

func (r *Runner) getBrokenPackages() ([]string, error) {
	if r.exeFile == "" {
		exe, err := os.CreateTemp("", "golo-*")
		if err != nil {
			return nil, err
		}
		r.exeFile = exe.Name()
		r.cleanup = append(r.cleanup, r.exeFile)
		if err := os.Chmod(r.exeFile, 0o777); err != nil {
			return nil, err
		}
	}
	subCmd := []string{"build"}
	if r.mode == "test" {
		subCmd = []string{"test", "-vet=off", "-c"}
	}

	if len(r.fixed) != 0 {
		if err := r.updateOverlays(); err != nil {
			return nil, err
		}
		subCmd = append(subCmd, "-overlay", r.overlayFile)
	}
	args := append(append(subCmd, "-o", r.exeFile), r.buildArgs...)
	if r.verbose {
		fmt.Println("# running: go ", strings.Join(args, " "))
	}
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		r.built = true
		return nil, nil
	}

	toFix := []string{}

	for _, line := range bytes.Split(out, []byte("\n")) {
		if matches := rePackage.FindSubmatch(line); matches != nil {
			toFix = append(toFix, string(matches[1]))
		}
	}

	return toFix, nil
}

func (r *Runner) updateOverlays() error {
	if r.overlayFile == "" {
		overlay, err := os.CreateTemp("", "golo-*.json")
		if err != nil {
			return err
		}
		r.overlayFile = overlay.Name()
		r.cleanup = append(r.cleanup, r.overlayFile)
		overlay.Close()
	}
	for f := range r.fixed {
		if r.overlays.Replace[f] == "" {
			newF, err := os.CreateTemp("", "golo-*.go")
			if err != nil {
				return err
			}
			r.overlays.Replace[f] = newF.Name()
			r.cleanup = append(r.cleanup, newF.Name())
			newF.Close()
			if err := os.WriteFile(newF.Name(), r.fixed[f], 0o666); err != nil {
				return err
			}
			if r.verbose {
				fmt.Println("#", f, newF.Name())
				os.Stdout.Write(r.fixed[f])
			}
		}
	}
	overlay, err := os.Create(r.overlayFile)
	if err != nil {
		return err
	}

	if r.verbose {
		fmt.Println("# overlay.json", r.overlayFile)
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		e.Encode(r.overlays)
	}
	if err := json.NewEncoder(overlay).Encode(r.overlays); err != nil {
		return err
	}
	return overlay.Close()
}

// Run does what the user asked. Call .Prepare() first
func (r *Runner) Run() (int, error) {
	// we failed to fix it, run the compiler again so the user can see the problems
	if !r.built {
		if r.verbose {
			fmt.Println("golo: failed to build, running with no overlay")
		}
		return r.exec(exec.Command("go", append(append([]string{r.mode}, r.buildArgs...), r.runArgs...)...))
	}

	switch r.mode {
	case "run":
		return r.exec(exec.Command(r.exeFile, r.runArgs...))
	case "test":
		args := r.buildArgs
		if r.overlayFile != "" {
			args = append([]string{"-vet=off", "-overlay=" + r.overlayFile}, r.buildArgs...)
		}
		return r.exec(exec.Command("go", append([]string{"test"}, args...)...))
	case "build":
		// TODO: copy the binary we just built to the right place?
		args := r.buildArgs
		if r.overlayFile != "" {
			args = append([]string{"-overlay=" + r.overlayFile}, r.buildArgs...)
		}
		return r.exec(exec.Command("go", append([]string{"build"}, args...)...))
	default:
		return 0, fmt.Errorf("%v is not supported yet", r.mode)
	}
}

func (r *Runner) exec(cmd *exec.Cmd) (int, error) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if !r.verbose {
		for _, file := range r.cleanup {
			os.Remove(file)
		}
	}
	if cmd.ProcessState == nil {
		return 0, err
	}
	return cmd.ProcessState.ExitCode(), nil
}
