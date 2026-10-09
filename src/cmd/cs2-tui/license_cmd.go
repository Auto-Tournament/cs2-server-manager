package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// runLicenseCommand handles `csm license set [<key>|-]`, `csm license status`
// and `csm license clear`. It never prints the full key.
func runLicenseCommand(args []string, stdin io.Reader, stdinIsTerminal bool) (string, error) {
	if len(args) == 0 || args[0] == "status" {
		s, err := csm.CurrentLicense()
		if err != nil {
			return "", err
		}
		return s.Report(), nil
	}
	switch args[0] {
	case "set":
		var key string
		switch {
		case len(args) == 2 && args[1] != "-":
			key = args[1]
		case len(args) == 1 || (len(args) == 2 && args[1] == "-"):
			if stdinIsTerminal {
				fmt.Fprint(os.Stderr, "Paste the license key and press Enter: ")
			}
			k, err := readKey(stdin)
			if err != nil {
				return "", err
			}
			key = k
		default:
			return "", fmt.Errorf("usage: csm license set <key>  (or pipe it in: csm license set < key.txt)")
		}
		var out strings.Builder
		s, err := csm.SetLicenseKey(&out, key)
		if err != nil {
			return out.String(), err
		}
		// Check in at once: the license's current terms and where it stands.
		csm.MaybeCheckIn(context.Background(), &out, true)
		return "License key saved.\n" + out.String() + "\n" + s.Report(), nil
	case "cap":
		// csm license cap [<n>|off]: how many servers the linked platform may create here.
		if len(args) == 1 {
			if n := csm.PlatformCap(csm.DefaultPlatformLink); n >= 0 {
				return fmt.Sprintf("The linked platform may create at most %d server(s) on this host.\n", n), nil
			}
			return "No cap: the linked platform may create servers here up to the license's limit.\n", nil
		}
		if len(args) != 2 {
			return "", fmt.Errorf("usage: csm license cap [<n>|off]")
		}
		n := -1
		if args[1] != "off" {
			v, err := strconv.Atoi(args[1])
			if err != nil || v < 0 {
				return "", fmt.Errorf("the cap is a number of servers (0 or more), or off")
			}
			n = v
		}
		if err := csm.SetPlatformCap(csm.DefaultPlatformLink, n); err != nil {
			return "", err
		}
		if n < 0 {
			return "Cap removed.\n", nil
		}
		return fmt.Sprintf("The linked platform may now create at most %d server(s) on this host.\n", n), nil
	case "clear", "remove", "unset":
		if len(args) != 1 {
			return "", fmt.Errorf("usage: csm license clear")
		}
		var out strings.Builder
		if err := csm.ClearLicenseKey(&out); err != nil {
			return out.String(), err
		}
		return "License key removed.\n" + out.String(), nil
	}
	return "", fmt.Errorf("unknown subcommand %q", args[0])
}

// readKey reads the first non-empty line from r.
func readKey(r io.Reader) (string, error) {
	sc := bufio.NewScanner(io.LimitReader(r, 64<<10))
	sc.Buffer(make([]byte, 0, 8192), 64<<10)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no key given (csm license set <key>, or pipe it in)")
}

func licenseCommand(args []string) {
	out, err := runLicenseCommand(args, os.Stdin, isatty.IsTerminal(os.Stdin.Fd()))
	// Never log the key: only the subcommand name.
	action := "license"
	if len(args) > 0 {
		action += " " + args[0]
	}
	csm.LogAction("cli", action, "", err)
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "license: %v\n", err)
		printLicenseUsage(os.Stderr)
		os.Exit(1)
	}
}

func printLicenseUsage(w *os.File) {
	fmt.Fprintln(w, "usage: csm license set <key> | status | clear | cap [<n>|off]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  set <key>   store an Auto Tournament license key and hand it to Ready Up on every server")
	fmt.Fprintln(w, "              (without <key>, or with -, it is read from stdin)")
	fmt.Fprintln(w, "  status      check the stored key offline and show what it covers")
	fmt.Fprintln(w, "  clear       remove the key, also from the servers' config")
	fmt.Fprintln(w, "  cap [<n>|off]  how many servers the linked platform may create on this host")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Free for non-commercial use without a key: "+csm.LicensePricingURL)
	fmt.Fprintln(w, "With a paid key, servers are created within the license's limit (shared by every")
	fmt.Fprintln(w, "install using the key), and an unpaid, replaced or moved key stops new servers.")
}

// licenseStatusLine is the one line `csm status` prints.
func licenseStatusLine() string {
	s, err := csm.CurrentLicense()
	if err != nil {
		return "License: could not read the stored key (" + err.Error() + ")"
	}
	line := s.Line()
	if s.Set && (len(s.Result.Warnings) > 0) {
		line += " — csm license status"
	}
	if st := csm.CurrentStanding().StandingLine(); st != "" {
		line += "\n" + st
	}
	return line
}
