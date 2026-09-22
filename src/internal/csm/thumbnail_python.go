package csm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Map thumbnail extraction needs Python with the vpk and Pillow modules.
// Debian/Ubuntu mark the system Python as externally managed (PEP 668), so
// `pip install` into it fails and the old hint told users to pass
// --break-system-packages. Instead csm keeps its own virtualenv under the csm
// root (<root>/python-venv) and installs the modules there when it runs as
// root. The venv uses --system-site-packages, so a distro python3-pil is
// reused when present.

// thumbnailPythonModules are the imports the thumbnail scripts need.
var thumbnailPythonModules = []string{"vpk", "PIL.Image"}

// thumbnailPipPackages are the pip packages that provide them.
var thumbnailPipPackages = []string{"vpk", "Pillow"}

// ThumbnailVenvDir is where csm keeps the Python venv for map thumbnails.
func ThumbnailVenvDir() string {
	return filepath.Join(ResolveRoot(), "python-venv")
}

// pythonEnv abstracts the host so resolveThumbnailPython can be unit tested.
type pythonEnv struct {
	venvDir string
	isRoot  bool
	// lookPath finds an executable on PATH (exec.LookPath).
	lookPath func(name string) (string, error)
	// exists reports whether path exists.
	exists func(path string) bool
	// hasModules reports whether py can import every module.
	hasModules func(py string, modules []string) bool
	// run runs a command, streaming output to w.
	run func(ctx context.Context, w io.Writer, name string, args ...string) error
}

func defaultPythonEnv() pythonEnv {
	return pythonEnv{
		venvDir:  ThumbnailVenvDir(),
		isRoot:   os.Geteuid() == 0,
		lookPath: exec.LookPath,
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		hasModules: pythonHasModules,
		run:        runCmdLoggedContext,
	}
}

// pythonHasModules reports whether py can import every module.
func pythonHasModules(py string, modules []string) bool {
	if len(modules) == 0 {
		return true
	}
	code := "import " + strings.Join(modules, ", ")
	return exec.Command(py, "-c", code).Run() == nil
}

func (e pythonEnv) systemPython() string {
	for _, name := range []string{"python3", "python"} {
		if p, err := e.lookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// resolveThumbnailPython returns a Python interpreter that can import vpk and
// Pillow. It prefers csm's venv, then the system Python, and otherwise (as
// root) creates the venv and installs the modules into it, installing
// python3 / python3-venv with apt-get first when they are missing.
func resolveThumbnailPython(ctx context.Context, w io.Writer, e pythonEnv) (string, error) {
	venvPy := filepath.Join(e.venvDir, "bin", "python3")
	if e.exists(venvPy) && e.hasModules(venvPy, thumbnailPythonModules) {
		return venvPy, nil
	}
	sysPy := e.systemPython()
	if sysPy != "" && e.hasModules(sysPy, thumbnailPythonModules) {
		return sysPy, nil
	}

	if !e.isRoot {
		return "", fmt.Errorf("python modules %s are not installed; run `sudo csm extract-map-data` once so csm can set them up in %s",
			strings.Join(thumbnailPipPackages, " and "), e.venvDir)
	}

	fmt.Fprintf(w, "[i] Setting up Python for map thumbnails in %s ...\n", e.venvDir)
	aptInstall := func(pkgs ...string) error {
		if _, err := e.lookPath("apt-get"); err != nil {
			return fmt.Errorf("apt-get not found; install %s manually", strings.Join(pkgs, " "))
		}
		fmt.Fprintf(w, "[deps] Running: apt-get install -y %s\n", strings.Join(pkgs, " "))
		return e.run(ctx, w, "apt-get", append([]string{"install", "-y"}, pkgs...)...)
	}

	if sysPy == "" {
		if err := aptInstall("python3", "python3-venv"); err != nil {
			return "", fmt.Errorf("python3 not found and could not be installed: %w", err)
		}
		if sysPy = e.systemPython(); sysPy == "" {
			return "", fmt.Errorf("python3 still not found after apt-get install")
		}
	}

	if !e.exists(venvPy) {
		mkVenv := func() error {
			return e.run(ctx, w, sysPy, "-m", "venv", "--system-site-packages", e.venvDir)
		}
		if err := mkVenv(); err != nil {
			// Debian/Ubuntu split venv/ensurepip into python3-venv.
			fmt.Fprintf(w, "[i] Creating the venv failed (%v); installing python3-venv and retrying\n", err)
			if aerr := aptInstall("python3-venv"); aerr != nil {
				return "", fmt.Errorf("could not create Python venv at %s: %w (and installing python3-venv failed: %v)", e.venvDir, err, aerr)
			}
			if err := mkVenv(); err != nil {
				return "", fmt.Errorf("could not create Python venv at %s: %w", e.venvDir, err)
			}
		}
	}

	pipArgs := append([]string{"-m", "pip", "install", "--disable-pip-version-check"}, thumbnailPipPackages...)
	if err := e.run(ctx, w, venvPy, pipArgs...); err != nil {
		return "", fmt.Errorf("pip install %s into %s failed: %w", strings.Join(thumbnailPipPackages, " "), e.venvDir, err)
	}
	if !e.hasModules(venvPy, thumbnailPythonModules) {
		return "", fmt.Errorf("python modules still missing in %s after pip install", e.venvDir)
	}
	fmt.Fprintf(w, "[OK] Python for map thumbnails ready (%s)\n", venvPy)
	return venvPy, nil
}
