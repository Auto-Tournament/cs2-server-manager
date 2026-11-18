package gamefiles

import "errors"

var errLZ4 = errors.New("corrupt block")

// LZ4Block decodes one raw LZ4 block (no frame header) into dst and returns
// the bytes written.
func LZ4Block(src, dst []byte) (int, error) {
	si, di := 0, 0
	length := func(n int) (int, bool) {
		if n != 15 {
			return n, true
		}
		for si < len(src) {
			b := int(src[si])
			si++
			n += b
			if b != 255 {
				return n, true
			}
		}
		return 0, false
	}
	for si < len(src) {
		token := src[si]
		si++
		lit, ok := length(int(token >> 4))
		if !ok || si+lit > len(src) || di+lit > len(dst) {
			return di, errLZ4
		}
		di += copy(dst[di:], src[si:si+lit])
		si += lit
		if si == len(src) {
			return di, nil
		}
		if si+2 > len(src) {
			return di, errLZ4
		}
		off := int(src[si]) | int(src[si+1])<<8
		si += 2
		n, ok := length(int(token & 15))
		n += 4
		if !ok || off == 0 || off > di || di+n > len(dst) {
			return di, errLZ4
		}
		for i := 0; i < n; i++ { // byte by byte: a match may overlap what it copies
			dst[di] = dst[di-off]
			di++
		}
	}
	return di, nil
}
