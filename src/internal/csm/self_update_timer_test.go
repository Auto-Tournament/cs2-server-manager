package csm

import (
	"strings"
	"testing"
)

func TestSelfUpdateUnits(t *testing.T) {
	service, timer := RenderSelfUpdateUnits("/usr/local/bin/csm", "/home/cs2")
	for _, want := range []string{"ExecStart=/usr/local/bin/csm self-update --auto", "Environment=CSM_ROOT=/home/cs2", "Type=oneshot"} {
		if !strings.Contains(service, want) {
			t.Errorf("service lacks %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"OnUnitActiveSec=6h", "RandomizedDelaySec=", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
}
