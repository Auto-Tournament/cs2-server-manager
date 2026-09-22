package csm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// fakeHost simulates python/apt/pip for resolveThumbnailPython.
type fakeHost struct {
	onPath      map[string]string // name -> path
	files       map[string]bool
	withModules map[string]bool // interpreter -> has vpk+PIL
	venvFails   int             // number of `-m venv` calls that fail
	pipFails    bool
	commands    []string
}

func (h *fakeHost) env(root bool) pythonEnv {
	return pythonEnv{
		venvDir: "/opt/csm/python-venv",
		isRoot:  root,
		lookPath: func(name string) (string, error) {
			if p, ok := h.onPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		exists:     func(p string) bool { return h.files[p] },
		hasModules: func(py string, _ []string) bool { return h.withModules[py] },
		run: func(_ context.Context, _ io.Writer, name string, args ...string) error {
			cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
			h.commands = append(h.commands, cmd)
			switch {
			case name == "apt-get":
				h.onPath["python3"] = "/usr/bin/python3"
			case strings.Contains(cmd, "-m venv"):
				if h.venvFails > 0 {
					h.venvFails--
					return errors.New("ensurepip is not available")
				}
				h.files["/opt/csm/python-venv/bin/python3"] = true
			case strings.Contains(cmd, "-m pip install"):
				if h.pipFails {
					return errors.New("pip failed")
				}
				h.withModules["/opt/csm/python-venv/bin/python3"] = true
			}
			return nil
		},
	}
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		onPath:      map[string]string{"apt-get": "/usr/bin/apt-get"},
		files:       map[string]bool{},
		withModules: map[string]bool{},
	}
}

const venvPython = "/opt/csm/python-venv/bin/python3"

func TestResolveThumbnailPython(t *testing.T) {
	tests := []struct {
		name      string
		root      bool
		setup     func(h *fakeHost)
		want      string
		wantErr   string
		wantCmds  []string // substrings, in order
		wantNoCmd bool
	}{
		{
			name: "existing venv with modules",
			root: true,
			setup: func(h *fakeHost) {
				h.onPath["python3"] = "/usr/bin/python3"
				h.files[venvPython] = true
				h.withModules[venvPython] = true
			},
			want:      venvPython,
			wantNoCmd: true,
		},
		{
			name: "system python already has modules",
			root: false,
			setup: func(h *fakeHost) {
				h.onPath["python3"] = "/usr/bin/python3"
				h.withModules["/usr/bin/python3"] = true
			},
			want:      "/usr/bin/python3",
			wantNoCmd: true,
		},
		{
			name:    "not root and nothing installed",
			root:    false,
			setup:   func(h *fakeHost) { h.onPath["python3"] = "/usr/bin/python3" },
			wantErr: "sudo csm extract-map-data",
		},
		{
			name:     "root creates venv and installs modules",
			root:     true,
			setup:    func(h *fakeHost) { h.onPath["python3"] = "/usr/bin/python3" },
			want:     venvPython,
			wantCmds: []string{"-m venv --system-site-packages /opt/csm/python-venv", "-m pip install --disable-pip-version-check vpk Pillow"},
		},
		{
			name: "root without python installs python3 first",
			root: true,
			want: venvPython,
			wantCmds: []string{
				"apt-get install -y python3 python3-venv",
				"/usr/bin/python3 -m venv",
				"-m pip install",
			},
		},
		{
			name: "missing ensurepip installs python3-venv and retries",
			root: true,
			setup: func(h *fakeHost) {
				h.onPath["python3"] = "/usr/bin/python3"
				h.venvFails = 1
			},
			want:     venvPython,
			wantCmds: []string{"-m venv", "apt-get install -y python3-venv", "-m venv", "-m pip install"},
		},
		{
			name: "pip failure is reported",
			root: true,
			setup: func(h *fakeHost) {
				h.onPath["python3"] = "/usr/bin/python3"
				h.pipFails = true
			},
			wantErr: "pip install vpk Pillow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newFakeHost()
			if tt.setup != nil {
				tt.setup(h)
			}
			var out bytes.Buffer
			got, err := resolveThumbnailPython(context.Background(), &out, h.env(tt.root))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v\n%s", err, out.String())
			}
			if got != tt.want {
				t.Fatalf("python = %q, want %q", got, tt.want)
			}
			if tt.wantNoCmd && len(h.commands) != 0 {
				t.Fatalf("expected no commands, ran %v", h.commands)
			}
			i := 0
			for _, c := range h.commands {
				if i < len(tt.wantCmds) && strings.Contains(c, tt.wantCmds[i]) {
					i++
				}
			}
			if i != len(tt.wantCmds) {
				t.Fatalf("commands %v do not contain %v in order", h.commands, tt.wantCmds)
			}
			if strings.Contains(strings.Join(h.commands, "\n"), "--break-system-packages") {
				t.Fatal("must not use --break-system-packages")
			}
		})
	}
}
