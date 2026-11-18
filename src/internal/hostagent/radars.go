package hostagent

import (
	"context"
	"net/http"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/radars"
)

// syncRadars sends the install's map radars at start and every 6 hours, once
// the host is linked. Only changed textures go up (radars.Sync).
func (a *Agent) syncRadars(ctx context.Context) {
	if a.opts.CS2Dir == "" {
		return
	}
	hc := a.opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	for {
		wait := 6 * time.Hour
		if c, err := LoadCredentials(a.opts.Paths); err != nil {
			wait = a.opts.NotLinkedWait
		} else if err := radars.Sync(ctx, hc, c.PlatformURL, c.Token, a.opts.CS2Dir, a.logf); err != nil {
			a.logf("radars: %v", err)
		}
		if !sleepCtx(ctx, wait) {
			return
		}
	}
}
