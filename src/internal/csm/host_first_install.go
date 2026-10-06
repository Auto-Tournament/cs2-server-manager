package csm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// First install from the platform (issue #108): a linked server-N machine
// with no servers yet gets its first servers from server.create, the same
// steps the TUI install wizard runs, without questions: bootstrap (SteamCMD
// master install, server-1..N, shared config), the plugin stack (Ready Up
// after the servers exist), then the agent's pre-start hook (fleet.cfg) and
// start.

// FirstServersGamePort is server-1's game port on a fresh host.
func (b *HostBackend) FirstServersGamePort() int { return DefaultBaseGamePort }

// bootstrapStep matches bootstrap's step lines ("[2/5] Installing CS2 ...").
var bootstrapStep = regexp.MustCompile(`^\[(\d+)/(\d+)\]\s*(.+)$`)

// stepWriter turns bootstrap's "[n/m] ..." lines into progress calls
// (0-80 %; Ready Up and starting the servers take the rest).
type stepWriter struct {
	progress func(step string, pct int)
	pending  strings.Builder
}

func (s *stepWriter) Write(p []byte) (int, error) {
	s.pending.Write(p)
	text := s.pending.String()
	last := strings.LastIndexByte(text, '\n')
	if last < 0 {
		return len(p), nil
	}
	sc := bufio.NewScanner(strings.NewReader(text[:last]))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := bootstrapStep.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			total, _ := strconv.Atoi(m[2])
			if total > 0 {
				s.progress(strings.TrimSuffix(m[3], "..."), 5+(n-1)*75/total)
			}
		}
	}
	s.pending.Reset()
	s.pending.WriteString(text[last+1:])
	return len(p), nil
}

// BootstrapFirstServers installs and starts the first `count` servers.
func (b *HostBackend) BootstrapFirstServers(ctx context.Context, count int, beforeStart func(serverDir string) error, progress func(step string, pct int)) ([]int, string, error) {
	if count < 1 {
		count = 1
	}
	user := configuredOrDetectedUser()
	cfg := BootstrapConfig{
		CS2User:        user,
		NumServers:     count,
		BaseGamePort:   DefaultBaseGamePort,
		BaseTVPort:     DefaultBaseTVPort,
		HostnamePrefix: "CS2 Server",
		EnableMetamod:  true,
		UpdateMaster:   true,
		Progress:       &stepWriter{progress: progress},
	}
	var out strings.Builder
	log, err := BootstrapWithContext(ctx, cfg)
	out.WriteString(log)
	LogAction("agent", "first install (bootstrap)", log, err)
	if err != nil {
		return nil, out.String(), err
	}
	if !UsesLegacyStack(user) {
		progress("installing Ready Up on the new servers", 82)
		plog, perr := UpdateAndDeployPluginsWithContext(ctx)
		out.WriteString(plog)
		LogAction("agent", "first install (Ready Up)", plog, perr)
		if perr != nil {
			return nil, out.String(), fmt.Errorf("the servers were created, but installing Ready Up failed: %w", perr)
		}
	}
	mgr, err := NewTmuxManager()
	if err != nil {
		return nil, out.String(), err
	}
	var nums []int
	for n := 1; n <= count; n++ {
		progress(fmt.Sprintf("starting server-%d", n), 90+n*10/count-1)
		dir := filepath.Join("/home", user, fmt.Sprintf("server-%d", n))
		if beforeStart != nil {
			if err := beforeStart(dir); err != nil {
				return nums, out.String(), fmt.Errorf("server-%d: %w", n, err)
			}
		}
		if err := mgr.Start(n); err != nil {
			return nums, out.String(), fmt.Errorf("starting server-%d: %w", n, err)
		}
		nums = append(nums, n)
	}
	return nums, out.String(), nil
}

var _ io.Writer = (*stepWriter)(nil)
