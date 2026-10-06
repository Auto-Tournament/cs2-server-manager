package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

const pluginsUsage = `usage: csm plugins [status | <setting> <value>]

  csm plugins status              Stack, Ready Up channel/version/bundle/license, what each server has
  csm plugins stack readyup|legacy
                                  readyup: Ready Up (no Metamod). legacy: Metamod + CounterStrikeSharp
                                  + the MatchZy-era plugin. Unset: an existing legacy host stays legacy,
                                  a fresh install gets Ready Up (beta until it has a stable release).
  csm plugins channel stable|beta stable: the latest stable release. beta: pre-releases too
  csm plugins version vX.Y.Z[-beta.N]|latest
                                  pin a release (overrides the channel); latest follows the channel
  csm plugins bundle essentials|full
                                  full adds skins, hello, midas, whitelist, deathmatch, addons
  csm plugins license noncommercial|commercial|clear
                                  your Ready Up license answer, asked once (AT_ACCEPT_LICENSE overrides)
  csm plugins auto on|off         csm monitor keeps Ready Up on its channel (idle servers only; default on)

Then csm update-plugins installs or updates it on every server. Environment overrides:
CSM_PLUGIN_STACK, CSM_READYUP_CHANNEL, CSM_READYUP_VERSION, CSM_READYUP_BUNDLE, AT_ACCEPT_LICENSE.`

// runPluginsCommand handles `csm plugins ...`.
func runPluginsCommand(args []string, stdin io.Reader, stdout io.Writer, terminal bool) (string, error) {
	if len(args) == 0 || args[0] == "status" {
		return csm.PluginsReport(context.Background(), true), nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		return pluginsUsage + "\n", nil
	}
	if len(args) == 1 && args[0] == "license" && terminal {
		use, err := csm.PromptReadyUpLicense(stdin, stdout)
		if err != nil {
			return "", err
		}
		if use == "" {
			return "Nothing saved.\n", nil
		}
		return "Saved: " + use + " use.\n", nil
	}
	if len(args) != 2 {
		return "", fmt.Errorf("usage: csm plugins %s <value> (csm plugins -h)", args[0])
	}
	key := args[0]
	if _, err := csm.SetPluginSetting(key, args[1]); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Saved %s = %s.\n", key, args[1])
	switch key {
	case "stack", "channel", "version", "bundle":
		msg += "Run csm update-plugins to apply it (or let csm monitor do it on idle servers).\n"
	}
	return msg + "\n" + csm.PluginsReport(context.Background(), key == "channel" || key == "version"), nil
}

func pluginsCommand(args []string) {
	out, err := runPluginsCommand(args, os.Stdin, os.Stdout, isatty.IsTerminal(os.Stdin.Fd()))
	action := "plugins"
	if len(args) > 0 {
		action += " " + strings.Join(args, " ")
	}
	csm.LogAction("cli", action, "", err)
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugins: %v\n\n%s\n", err, pluginsUsage)
		os.Exit(1)
	}
}

// ensureReadyUpLicense asks for the license answer once, in a terminal,
// when none is saved or in the environment. Unattended runs get the error
// from the install instead.
func ensureReadyUpLicense() {
	s, err := csm.LoadPluginSettings()
	if err != nil || s.Resolved().AcceptLicense != "" {
		return
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return
	}
	use, err := csm.PromptReadyUpLicense(os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not save the license answer: %v\n", err)
	}
	if use == "" {
		fmt.Fprintln(os.Stderr, "No license answer: Ready Up was not installed.")
		os.Exit(1)
	}
}

// updateReadyUpCLI is `csm update-plugins` on the readyup stack.
func updateReadyUpCLI() {
	ensureReadyUpLicense()
	out, err := csm.UpdateReadyUpOnServers(context.Background(), nil)
	csm.LogAction("cli", "update-plugins-readyup", out, err)
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ready Up update failed: %v\n", err)
		os.Exit(1)
	}
}
