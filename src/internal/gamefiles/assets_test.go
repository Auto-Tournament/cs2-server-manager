package gamefiles

import (
	"image"
	"os"
	"testing"
)

func TestShrink(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = 200, 255
	}
	img.Pix[3] = 0 // one transparent pixel: its colour must not count
	out := shrink(img, 2)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 1 {
		t.Fatalf("size %v", out.Bounds())
	}
	if out.Pix[0] != 200 || out.Pix[3] != 191 || out.Pix[7] != 255 {
		t.Fatalf("pixels %v", out.Pix)
	}
}

func TestAssetRefusesOddNames(t *testing.T) {
	for _, c := range [][2]string{{"skins", "../pak01.webp"}, {"nope", "de_dust2.webp"}, {"skins", "A.webp"}, {"maps-480", "de_dust2.exe"}} {
		if _, _, err := Asset(t.TempDir(), c[0], c[1]); err != ErrNoAsset {
			t.Errorf("%v: %v", c, err)
		}
	}
}

// CS2_DIR points at an install's game/csgo; OUT, when set, gets the files.
func TestAssetsFromInstall(t *testing.T) {
	dir := os.Getenv("CS2_DIR")
	if dir == "" {
		t.Skip("CS2_DIR not set")
	}
	for _, c := range [][3]string{
		{"maps-1280", "de_dust2.webp", "image/jpeg"},
		{"maps-480", "de_mirage_1.webp", "image/jpeg"},
		{"map-icons", "de_inferno_icon.svg", "image/svg+xml"},
		{"skins-256", "weapon_ak47_cu_ak47_asiimov.webp", "image/png"},
		{"skins", "weapon_ak47_cu_ak47_asiimov_heavy.webp", "image/png"},
	} {
		data, typ, err := Asset(dir, c[0], c[1])
		if err != nil || typ != c[2] || len(data) < 500 {
			t.Errorf("%v: %s %d %v", c, typ, len(data), err)
			continue
		}
		if out := os.Getenv("OUT"); out != "" {
			_ = os.WriteFile(out+"/"+c[0]+"_"+c[1]+map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/svg+xml": ".svg"}[typ], data, 0o644)
		}
	}
	if _, _, err := Asset(dir, "skins", "weapon_ak47_no_such_skin.webp"); err != ErrNoAsset {
		t.Errorf("missing skin: %v", err)
	}
}
