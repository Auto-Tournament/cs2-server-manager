package gamefiles

import (
	"os"
	"testing"
)

// Valve's textures can't be committed: VTEX_FILE points at a two-mip skin
// preview (econ/default_generated/*_light_png.vtex_c) from an install.
func TestDecodeMultiMip(t *testing.T) {
	f := os.Getenv("VTEX_FILE")
	if f == "" {
		t.Skip("VTEX_FILE not set")
	}
	d, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	img, err := DecodeVtex(d)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 384 {
		t.Fatalf("got %v", b)
	}
}
