package csm

import (
	"bytes"
	"testing"
)

func TestProgressFilterDropsRedraws(t *testing.T) {
	var out bytes.Buffer
	f := NewProgressFilter(&out)
	_, _ = f.Write([]byte("  Updating server-1 ...\n  0%  0.00kB/s\r  10%\r  100%  done\r"))
	_, _ = f.Write([]byte("  [OK] updated\nlast"))
	_ = f.Flush()
	if got, want := out.String(), "  Updating server-1 ...\n  [OK] updated\nlast\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
