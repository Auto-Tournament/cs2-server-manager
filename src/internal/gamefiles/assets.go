package gamefiles

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNoAsset is a set or file the install doesn't have.
var ErrNoAsset = errors.New("no such picture in the install")

// Sets are the picture sizes the platform asks for (its /api/game/cs2/img/<set>/<file>).
var Sets = map[string]struct {
	dir, suffix string
	width       int // 0 = as stored
}{
	"maps-1280": {"panorama/images/map_icons/screenshots/1080p/", "_png.vtex_c", 1280},
	"maps-480":  {"panorama/images/map_icons/screenshots/1080p/", "_png.vtex_c", 480},
	"map-icons": {"panorama/images/map_icons/map_icon_", ".vsvg_c", 0},
	"skins-256": {"panorama/images/econ/default_generated/", "_png.vtex_c", 256},
	"skins":     {"panorama/images/econ/default_generated/", "_png.vtex_c", 0},
}

var stemRe = regexp.MustCompile(`^[a-z0-9_]{1,150}$`)

// Asset reads one picture from the install's pak01: a PNG (or the SVG of a
// map icon), named as csm's old map_thumbnails/ and skin_images/ files were.
func Asset(cs2Dir, set, file string) ([]byte, string, error) {
	s, ok := Sets[set]
	ext := filepath.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if !ok || (ext != ".webp" && ext != ".png" && ext != ".svg") || !stemRe.MatchString(stem) {
		return nil, "", ErrNoAsset
	}
	v, err := openCached(filepath.Join(cs2Dir, "pak01_dir.vpk"))
	if err != nil {
		return nil, "", err
	}
	name := stem
	if set == "map-icons" {
		name = strings.TrimSuffix(stem, "_icon")
	} else if strings.HasPrefix(set, "skins") && !strings.HasSuffix(stem, "_medium") && !strings.HasSuffix(stem, "_heavy") {
		name = stem + "_light" // the game's Factory New preview
	}
	data, err := v.Read(s.dir + name + s.suffix)
	if err != nil {
		return nil, "", ErrNoAsset
	}
	if set == "map-icons" {
		start, end := bytes.Index(data, []byte("<svg")), bytes.LastIndex(data, []byte("</svg>"))
		if start < 0 || end < start {
			return nil, "", ErrNoAsset
		}
		return data[start : end+len("</svg>")], "image/svg+xml", nil
	}
	img, err := DecodeVtex(data)
	if err != nil {
		return nil, "", err
	}
	if s.width > 0 && img.Bounds().Dx() > s.width {
		img = shrink(img, s.width)
	}
	var buf bytes.Buffer
	if strings.HasPrefix(set, "maps") { // screenshots have no alpha: JPEG is a tenth of the PNG
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82})
		return buf.Bytes(), "image/jpeg", err
	}
	err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	return buf.Bytes(), "image/png", err
}

// shrink scales img down to width by averaging the source pixels under each
// output pixel (alpha-weighted, so transparent edges don't darken).
func shrink(img *image.NRGBA, width int) *image.NRGBA {
	sw, sh := img.Bounds().Dx(), img.Bounds().Dy()
	height := max(1, sh*width/sw)
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0, y1 := y*sh/height, max(y*sh/height+1, (y+1)*sh/height)
		for x := 0; x < width; x++ {
			x0, x1 := x*sw/width, max(x*sw/width+1, (x+1)*sw/width)
			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				p := img.Pix[sy*img.Stride+x0*4 : sy*img.Stride+x1*4]
				for i := 0; i < len(p); i += 4 {
					pa := uint64(p[i+3])
					r, g, b, a, n = r+uint64(p[i])*pa, g+uint64(p[i+1])*pa, b+uint64(p[i+2])*pa, a+pa, n+1
				}
			}
			o := out.Pix[y*out.Stride+x*4:]
			if a > 0 {
				o[0], o[1], o[2] = uint8(r/a), uint8(g/a), uint8(b/a)
			}
			o[3] = uint8(a / n)
		}
	}
	return out
}

// The pak01 directory is opened once and kept until CS2 updates it.
var (
	cacheMu   sync.Mutex
	cachedVPK *VPK
	cachedKey string
)

func openCached(path string) (*VPK, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	key := path + "@" + fi.ModTime().Format(time.RFC3339Nano)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cachedVPK != nil && cachedKey == key {
		return cachedVPK, nil
	}
	v, err := OpenVPK(path)
	if err != nil {
		return nil, err
	}
	cachedVPK, cachedKey = v, key
	return v, nil
}
