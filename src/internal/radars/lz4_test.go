package radars

import (
	"bytes"
	"testing"
)

func TestLZ4Block(t *testing.T) {
	// "abc" then a 9-byte overlapping match at offset 3, then the literal "xyz".
	src := []byte{0x35, 'a', 'b', 'c', 3, 0, 0x30, 'x', 'y', 'z'}
	dst := make([]byte, 32)
	n, err := lz4Block(src, dst)
	if err != nil || !bytes.Equal(dst[:n], []byte("abcabcabcabcxyz")) {
		t.Fatalf("got %q, %v", dst[:n], err)
	}
	if _, err := lz4Block([]byte{0x10, 'a', 9, 0}, make([]byte, 8)); err == nil {
		t.Fatal("an offset past the output must fail")
	}
}
