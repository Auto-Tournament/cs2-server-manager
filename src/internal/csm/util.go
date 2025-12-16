package csm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// runCmdLogged runs a command, streaming its combined output into the provided
// writer. It returns any error from exec.Command.
func runCmdLogged(w io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v failed: %w", name, args, err)
	}
	return nil
}

// getenvDefault returns the value of the environment variable key, or def if
// the variable is unset or empty.
func getenvDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
