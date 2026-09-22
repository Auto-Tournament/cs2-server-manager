package csm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Newer glibc (2.41+, shipped by Debian 13 and Ubuntu 25.04) refuses to
// dlopen a shared library whose PT_GNU_STACK header asks for an executable
// stack ("cannot enable executable stack as shared object requires").
// CounterStrikeSharp's counterstrikesharp.so has that flag set
// (CounterStrikeSharp issue #1024), so Metamod cannot load it there.
//
// The usual fix is `patchelf --clear-execstack` (execstack/prelink are gone
// from Debian 13). clearExecStack does the same edit natively: it clears the
// PF_X bit of the PT_GNU_STACK program header, which is all that command
// changes. Doing it in Go avoids a new package dependency and works with the
// older patchelf builds on Ubuntu 22.04 that lack --clear-execstack.

const (
	elfPTGnuStack = 0x6474e551
	elfPFX        = 0x1
)

// elfStackHeader locates the PT_GNU_STACK program header in an ELF image.
// It returns the file offset of its p_flags field and the flags, or
// found=false when the file is not ELF or has no such header.
func elfStackHeader(r io.ReaderAt) (flagsOff int64, flags uint32, found bool, err error) {
	ident := make([]byte, 16)
	if _, err := r.ReadAt(ident, 0); err != nil {
		return 0, 0, false, nil // too short to be ELF
	}
	if !bytes.Equal(ident[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return 0, 0, false, nil
	}
	var bo binary.ByteOrder
	switch ident[5] {
	case 1:
		bo = binary.LittleEndian
	case 2:
		bo = binary.BigEndian
	default:
		return 0, 0, false, fmt.Errorf("unknown ELF data encoding %d", ident[5])
	}

	var phoff int64
	var phentsize, phnum uint16
	var flagsField int64 // offset of p_flags inside one program header
	switch ident[4] {
	case 2: // ELFCLASS64
		hdr := make([]byte, 64)
		if _, err := r.ReadAt(hdr, 0); err != nil {
			return 0, 0, false, fmt.Errorf("read ELF64 header: %w", err)
		}
		phoff = int64(bo.Uint64(hdr[32:40]))
		phentsize = bo.Uint16(hdr[54:56])
		phnum = bo.Uint16(hdr[56:58])
		flagsField = 4
	case 1: // ELFCLASS32
		hdr := make([]byte, 52)
		if _, err := r.ReadAt(hdr, 0); err != nil {
			return 0, 0, false, fmt.Errorf("read ELF32 header: %w", err)
		}
		phoff = int64(bo.Uint32(hdr[28:32]))
		phentsize = bo.Uint16(hdr[42:44])
		phnum = bo.Uint16(hdr[44:46])
		flagsField = 24
	default:
		return 0, 0, false, fmt.Errorf("unknown ELF class %d", ident[4])
	}
	if phoff <= 0 || phentsize < 32 {
		return 0, 0, false, nil
	}

	ph := make([]byte, phentsize)
	for i := 0; i < int(phnum); i++ {
		off := phoff + int64(i)*int64(phentsize)
		if _, err := r.ReadAt(ph, off); err != nil {
			return 0, 0, false, fmt.Errorf("read program header %d: %w", i, err)
		}
		if bo.Uint32(ph[0:4]) != elfPTGnuStack {
			continue
		}
		return off + flagsField, bo.Uint32(ph[flagsField : flagsField+4]), true, nil
	}
	return 0, 0, false, nil
}

// elfHasExecStack reports whether the ELF file at path requests an
// executable stack.
func elfHasExecStack(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, flags, found, err := elfStackHeader(f)
	if err != nil || !found {
		return false, err
	}
	return flags&elfPFX != 0, nil
}

// clearExecStack clears the executable-stack flag of the ELF file at path,
// like `patchelf --clear-execstack`. It reports whether the file changed.
func clearExecStack(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	off, flags, found, err := elfStackHeader(f)
	if err != nil || !found || flags&elfPFX == 0 {
		return false, err
	}

	// Byte order is the same one used to read the header.
	ident := make([]byte, 6)
	if _, err := f.ReadAt(ident, 0); err != nil {
		return false, err
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if ident[5] == 2 {
		bo = binary.BigEndian
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, flags&^elfPFX)
	if _, err := f.WriteAt(buf, off); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, f.Close()
}

// clearExecStackInTree clears the executable-stack flag on every shared
// library (*.so, *.so.N) under root that has it set, logging each change to
// w. A missing root is a no-op. Failures on single files are logged and the
// first one is returned after the walk.
func clearExecStackInTree(w io.Writer, root string) error {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil
	}
	var firstErr error
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".so") && !strings.Contains(name, ".so.") {
			return nil
		}
		changed, cerr := clearExecStack(path)
		if cerr != nil {
			fmt.Fprintf(w, "  [WARN] Could not clear executable-stack flag on %s: %v\n", path, cerr)
			if firstErr == nil {
				firstErr = cerr
			}
			return nil
		}
		if changed {
			fmt.Fprintf(w, "  [*] Cleared executable-stack flag on %s (needed for glibc 2.41+, e.g. Debian 13)\n", path)
		}
		return nil
	})
	return firstErr
}
