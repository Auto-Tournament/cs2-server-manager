package csm

import "fmt"

// launchSpec holds everything needed to build a server's launch command. It
// has no filesystem or environment lookups of its own so the command
// construction can be unit tested.
type launchSpec struct {
	// Mode selects the launcher: "alternate"/"csm"/"csmsh"/"csm.sh" for csm.sh,
	// "binary"/"cs2"/"exe" for the cs2 binary, anything else for Valve's cs2.sh.
	Mode string
	// LegacyCS2Sh is true when game/cs2.sh is an older CSM wrapper that
	// already injects -dedicated/-ip/-usercon.
	LegacyCS2Sh bool

	GamePort   int
	TVPort     int
	MaxPlayers int
	GSLT       string

	// ConfigScope is the MatchZy persistent config scope for this server (see
	// MatchzyConfigScope). Empty omits the argument.
	ConfigScope string
}

// usesCSMLauncherSh reports whether the spec launches through csm.sh, which
// the caller must make sure exists first.
func (s launchSpec) usesCSMLauncherSh() bool {
	switch s.Mode {
	case "alternate", "csm", "csmsh", "csm.sh":
		return true
	}
	return false
}

// buildLaunchCommand returns the command run inside the server's game
// directory. The format is the v1.4.5 one:
//   - `-port` (command-line flag)
//   - `+tv_port` (console command - note the + not -)
//   - `+map` to load a map at startup
//   - `+maxplayers` for the player limit
//
// followed by `+matchzy_config_scope <scope>` and the optional GSLT.
func buildLaunchCommand(s launchSpec) string {
	extra := matchzyScopeLaunchArg(s.ConfigScope)
	if s.GSLT != "" {
		extra += fmt.Sprintf(" -gslt %s", s.GSLT)
	}

	switch s.Mode {
	case "alternate", "csm", "csmsh", "csm.sh":
		// csm.sh already provides -dedicated/-ip/-usercon.
		return fmt.Sprintf("./csm.sh +map de_dust2 -port %d +tv_port %d +maxplayers %d%s",
			s.GamePort, s.TVPort, s.MaxPlayers, extra,
		)
	case "binary", "cs2", "exe":
		// Run the cs2 binary directly. Not recommended upstream, but offered as
		// an opt-in. LD_LIBRARY_PATH prefers bundled libs so "binary mode"
		// doesn't accidentally load incompatible distro libs.
		return fmt.Sprintf("LD_LIBRARY_PATH=./bin/linuxsteamrt64:./csgo/bin/linuxsteamrt64:$LD_LIBRARY_PATH ENABLE_PATHMATCH=1 ./bin/linuxsteamrt64/cs2 -dedicated -ip 0.0.0.0 -usercon +map de_dust2 -port %d +tv_port %d +maxplayers %d%s",
			s.GamePort, s.TVPort, s.MaxPlayers, extra,
		)
	default:
		if s.LegacyCS2Sh {
			// Backward-compat: older installs may have a CSM-managed cs2.sh
			// that already injects its own default args.
			return fmt.Sprintf("./cs2.sh +map de_dust2 -port %d +tv_port %d +maxplayers %d%s",
				s.GamePort, s.TVPort, s.MaxPlayers, extra,
			)
		}
		// Valve cs2.sh must be told it's a dedicated server.
		return fmt.Sprintf("./cs2.sh -dedicated -ip 0.0.0.0 +map de_dust2 -port %d +tv_port %d +maxplayers %d -usercon%s",
			s.GamePort, s.TVPort, s.MaxPlayers, extra,
		)
	}
}

// wrapSteamRuntimeLaunch runs launch inside Steam Runtime (SteamRT3) for
// newer-distro CounterStrikeSharp compatibility. It prefers the common
// "-- <command>" form and falls back to a direct invocation if the wrapper
// rejects the extra separator/args on some builds.
func wrapSteamRuntimeLaunch(rtRun, launch string) string {
	return fmt.Sprintf("bash -lc \"rt=%s; $rt --graphics-provider \\\"\\\" -- %s || exec $rt %s\"",
		rtRun,
		launch,
		launch,
	)
}

// buildTmuxStartCmdline returns the shell command (run as the CS2 user via
// `su -c`) that starts launch in a detached tmux session.
func buildTmuxStartCmdline(gameDir, session, launch string) string {
	return fmt.Sprintf("cd %s && tmux new-session -d -s %s '%s'", gameDir, session, launch)
}
