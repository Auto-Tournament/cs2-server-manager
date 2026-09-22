package csm

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildELF returns a minimal ELF shared object with a PT_LOAD and a
// PT_GNU_STACK program header whose flags are stackFlags.
func buildELF(t *testing.T, class elf.Class, bo binary.ByteOrder, stackFlags elf.ProgFlag) []byte {
	t.Helper()
	var buf bytes.Buffer
	data := elf.ELFDATA2LSB
	if bo == binary.BigEndian {
		data = elf.ELFDATA2MSB
	}
	ident := [16]byte{0x7f, 'E', 'L', 'F', byte(class), byte(data), byte(elf.EV_CURRENT)}
	progs := []struct {
		typ   elf.ProgType
		flags elf.ProgFlag
	}{
		{elf.PT_LOAD, elf.PF_R | elf.PF_X},
		{elf.PT_GNU_STACK, stackFlags},
	}
	w := func(v any) {
		if err := binary.Write(&buf, bo, v); err != nil {
			t.Fatal(err)
		}
	}
	if class == elf.ELFCLASS64 {
		w(elf.Header64{Ident: ident, Type: uint16(elf.ET_DYN), Machine: uint16(elf.EM_X86_64), Version: 1,
			Phoff: 64, Ehsize: 64, Phentsize: 56, Phnum: uint16(len(progs))})
		for _, p := range progs {
			w(elf.Prog64{Type: uint32(p.typ), Flags: uint32(p.flags), Align: 16})
		}
	} else {
		w(elf.Header32{Ident: ident, Type: uint16(elf.ET_DYN), Machine: uint16(elf.EM_386), Version: 1,
			Phoff: 52, Ehsize: 52, Phentsize: 32, Phnum: uint16(len(progs))})
		for _, p := range progs {
			w(elf.Prog32{Type: uint32(p.typ), Flags: uint32(p.flags), Align: 16})
		}
	}
	return buf.Bytes()
}

func stackFlagsOf(t *testing.T, path string) elf.ProgFlag {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("debug/elf cannot parse %s: %v", path, err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_GNU_STACK {
			return p.Flags
		}
	}
	t.Fatalf("%s has no PT_GNU_STACK", path)
	return 0
}

func TestClearExecStack(t *testing.T) {
	tests := []struct {
		name  string
		class elf.Class
		bo    binary.ByteOrder
		flags elf.ProgFlag
		want  bool // changed
	}{
		{"elf64 le execstack", elf.ELFCLASS64, binary.LittleEndian, elf.PF_R | elf.PF_W | elf.PF_X, true},
		{"elf64 le already clear", elf.ELFCLASS64, binary.LittleEndian, elf.PF_R | elf.PF_W, false},
		{"elf32 le execstack", elf.ELFCLASS32, binary.LittleEndian, elf.PF_R | elf.PF_W | elf.PF_X, true},
		{"elf64 be execstack", elf.ELFCLASS64, binary.BigEndian, elf.PF_R | elf.PF_W | elf.PF_X, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "counterstrikesharp.so")
			if err := os.WriteFile(path, buildELF(t, tt.class, tt.bo, tt.flags), 0o755); err != nil {
				t.Fatal(err)
			}
			has, err := elfHasExecStack(path)
			if err != nil || has != (tt.flags&elf.PF_X != 0) {
				t.Fatalf("elfHasExecStack = %v, %v", has, err)
			}
			changed, err := clearExecStack(path)
			if err != nil {
				t.Fatalf("clearExecStack: %v", err)
			}
			if changed != tt.want {
				t.Fatalf("changed = %v, want %v", changed, tt.want)
			}
			if got := stackFlagsOf(t, path); got != elf.PF_R|elf.PF_W {
				t.Fatalf("stack flags after clear = %v, want PF_R+PF_W", got)
			}
			if has, _ := elfHasExecStack(path); has {
				t.Fatal("still has exec stack")
			}
		})
	}
}

func TestClearExecStackInTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "addons", "counterstrikesharp")
	bin := filepath.Join(root, "bin", "linuxsteamrt64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(bin, "counterstrikesharp.so")
	versioned := filepath.Join(root, "dotnet", "libfoo.so.1")
	notELF := filepath.Join(root, "fake.so")
	dll := filepath.Join(root, "plugins", "x.dll")
	for path, body := range map[string][]byte{
		css:       buildELF(t, elf.ELFCLASS64, binary.LittleEndian, elf.PF_R|elf.PF_W|elf.PF_X),
		versioned: buildELF(t, elf.ELFCLASS64, binary.LittleEndian, elf.PF_R|elf.PF_W|elf.PF_X),
		notELF:    []byte("not an elf"),
		dll:       buildELF(t, elf.ELFCLASS64, binary.LittleEndian, elf.PF_R|elf.PF_W|elf.PF_X),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := clearExecStackInTree(&out, root); err != nil {
		t.Fatalf("clearExecStackInTree: %v", err)
	}
	for _, p := range []string{css, versioned} {
		if has, _ := elfHasExecStack(p); has {
			t.Errorf("%s still requests an executable stack", p)
		}
	}
	if has, _ := elfHasExecStack(dll); !has {
		t.Error("non-.so files must be left alone")
	}
	if !strings.Contains(out.String(), "counterstrikesharp.so") {
		t.Errorf("expected log line for counterstrikesharp.so:\n%s", out.String())
	}
	if err := clearExecStackInTree(&out, filepath.Join(root, "missing")); err != nil {
		t.Errorf("missing root should be a no-op: %v", err)
	}
}
