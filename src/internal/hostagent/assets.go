package hostagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sivert-io/cs2-server-manager/src/internal/gamefiles"
)

// handleAsset reads the picture from the master install and uploads it; the
// platform serves and caches it, so nothing of Valve's is kept anywhere else.
func (a *Agent) handleAsset(ctx context.Context, ref string, c *AssetCmd) ResultPayload {
	if a.opts.CS2Dir == "" {
		return rejected(CodeUnsupported, "this csm has no CS2 install to read pictures from")
	}
	var data []byte
	var typ string
	var err error
	if c.Set == "catalog" {
		if a.opts.Catalog == nil {
			return rejected(CodeUnsupported, "this csm doesn't build catalogs")
		}
		data, err = a.opts.Catalog(c.File)
		typ = "application/json"
	} else {
		data, typ, err = gamefiles.Asset(a.opts.CS2Dir, c.Set, c.File)
	}
	if errors.Is(err, gamefiles.ErrNoAsset) {
		return failed(CodeNotFound, c.Set+"/"+c.File+" is not in this install", "")
	}
	if err != nil {
		return failed(CodeFailed, err.Error(), "")
	}
	creds := a.currentCreds()
	if creds == nil {
		return failed(CodeFailed, "not linked", "")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	url := strings.TrimRight(creds.PlatformURL, "/") + "/api/fleet/host/assets/" + ref
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return failed(CodeFailed, err.Error(), "")
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	req.Header.Set("Content-Type", typ)
	hc := a.opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return failed(CodeFailed, "upload: "+err.Error(), "")
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return failed(CodeFailed, fmt.Sprintf("upload: %s", res.Status), "")
	}
	return okResult(fmt.Sprintf("%s/%s: %d bytes", c.Set, c.File, len(data)))
}
