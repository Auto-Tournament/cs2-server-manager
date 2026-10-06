package csm

import "testing"

func TestCheckCleanupTarget(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		invoking   string
		uid        int
		deleteUser bool
		ok         bool
	}{
		{"no user", "", "", -1, false, false},
		{"root", "root", "domi", 0, false, false},
		{"system account", "daemon", "domi", 1, false, false},
		{"own servers, keep the account", "domi", "domi", 1000, false, true},
		{"delete own account", "domi", "domi", 1000, true, false},
		{"delete a dedicated service account", "cs2servermanager", "domi", 1001, true, true},
	}
	for _, c := range cases {
		err := checkCleanupTarget(c.target, c.invoking, c.uid, c.deleteUser)
		if (err == nil) != c.ok {
			t.Errorf("%s: checkCleanupTarget(%q, %q, %d, %v) = %v, want ok=%v", c.name, c.target, c.invoking, c.uid, c.deleteUser, err, c.ok)
		}
	}
}

func TestCheckStateRoot(t *testing.T) {
	for _, bad := range []string{"/", "/root", "/home", "/usr/local/bin", "/etc", "relative/dir"} {
		if checkStateRoot(bad) == nil {
			t.Errorf("checkStateRoot(%q) = nil, want a refusal", bad)
		}
	}
	for _, good := range []string{"/home/domi", "/opt/cs2-server-manager", "/srv/csm"} {
		if err := checkStateRoot(good); err != nil {
			t.Errorf("checkStateRoot(%q) = %v, want nil", good, err)
		}
	}
}
