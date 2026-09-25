package tui

import "testing"

func TestSelfUpdateTarget(t *testing.T) {
	cases := []struct {
		name        string
		exe         string
		dirWritable bool
		euid        int
		home        string
		want        string
		wantErr     bool
	}{
		{"writable dir replaces in place", "/usr/local/bin/csm", true, 0, "/root", "/usr/local/bin/csm", false},
		{"cs2 user in writable dir", "/home/cs2/.local/bin/csm", true, 998, "/home/cs2", "/home/cs2/.local/bin/csm", false},
		{"cs2 user, root-owned dir -> ~/.local/bin", "/usr/local/bin/csm", false, 998, "/home/cs2", "/home/cs2/.local/bin/csm", false},
		{"root cannot write", "/usr/local/bin/csm", false, 0, "/root", "", true},
		{"no home", "/usr/local/bin/csm", false, 998, "", "", true},
		{"already in ~/.local/bin but not writable", "/home/cs2/.local/bin/csm", false, 998, "/home/cs2", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selfUpdateTarget(c.exe, c.dirWritable, c.euid, c.home)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
