package radars

import (
	"bytes"
	"github.com/sivert-io/cs2-server-manager/src/internal/gamefiles"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRealInstall(t *testing.T) {
	dir := os.Getenv("CS2_DIR")
	if dir == "" {
		t.Skip()
	}
	local, from := localRadars(dir, nil)
	ok := 0
	for key, r := range local {
		data, err := from[key].Read(radarTexture(r.Map, r.Level))
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		img, err := gamefiles.DecodeVtex(data)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if b := img.Bounds(); b.Dx() < 256 {
			t.Errorf("%s: %v", key, b)
		}
		if out := os.Getenv("OUT"); out != "" {
			var b bytes.Buffer
			_ = png.Encode(&b, img)
			_ = os.WriteFile(filepath.Join(out, r.Map+"_"+r.Level+".png"), b.Bytes(), 0o644)
		}
		ok++
	}
	t.Logf("%d of %d radars decoded", ok, len(local))
}
