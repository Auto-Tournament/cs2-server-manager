package csm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGithubSlugAndRedact(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Auto-Tournament/cs2-server-manager.git": "Auto-Tournament/cs2-server-manager",
		"git@github.com:Auto-Tournament/cs2-server-manager.git":     "Auto-Tournament/cs2-server-manager",
		"https://github.com/Auto-Tournament/cs2-server-manager":     "Auto-Tournament/cs2-server-manager",
		"/srv/git/cs2-server-manager.git":                           "",
	}
	for in, want := range cases {
		if got := githubSlug(in); got != want {
			t.Errorf("githubSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := redactRemote("https://user:ghp_secret@github.com/a/cs2-server-manager.git"); got != "https://github.com/a/cs2-server-manager.git" {
		t.Errorf("redactRemote = %q", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestPublishMapThumbnails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "csm test", "GIT_AUTHOR_EMAIL": "csm@example.com",
		"GIT_COMMITTER_NAME": "csm test", "GIT_COMMITTER_EMAIL": "csm@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}

	base := t.TempDir()
	origin := filepath.Join(base, "cs2-server-manager.git")
	repo := filepath.Join(base, "checkout")
	runGit(t, base, "init", "-q", "--bare", origin)
	runGit(t, base, "init", "-q", "-b", "master", repo)
	runGit(t, repo, "remote", "add", "origin", origin)
	if err := os.MkdirAll(filepath.Join(repo, "map_thumbnails"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "map_thumbnails", "de_dust2.webp"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")

	thumbs := filepath.Join(base, "map_thumbnails")
	if err := os.MkdirAll(thumbs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"de_dust2.webp": "new", "maps.json": "{}\n", "notes.txt": "skip me"} {
		if err := os.WriteFile(filepath.Join(thumbs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := PublishMapThumbnails(context.Background(), thumbs, repo, "1.41.1.4", &out); err != nil {
		t.Fatalf("publish: %v\n%s", err, out.String())
	}
	log := out.String()
	if !strings.Contains(log, "gh pr create") || !strings.Contains(log, "push -u origin maps/update-") {
		t.Fatalf("missing next-step commands:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(repo, "map_thumbnails", "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("non-image files must not be copied")
	}
	subject, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(subject)); got != "chore(maps): update map thumbnails and maps.json for CS2 1.41.1.4" {
		t.Fatalf("commit subject = %q", got)
	}
	// Nothing may have been pushed.
	if refs, _ := exec.Command("git", "-C", origin, "for-each-ref").Output(); len(bytes.TrimSpace(refs)) != 0 {
		t.Fatalf("origin has refs after publish: %s", refs)
	}

	// A second run with the same files has nothing to publish.
	out.Reset()
	runGit(t, repo, "checkout", "-q", "master")
	runGit(t, repo, "merge", "-q", "--ff-only", "HEAD@{1}")
	if err := PublishMapThumbnails(context.Background(), thumbs, repo, "", &out); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to publish") {
		t.Fatalf("expected nothing to publish:\n%s", out.String())
	}
}

func TestPublishMapThumbnailsRejectsDirtyOrForeignRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := t.TempDir()
	repo := filepath.Join(base, "other")
	runGit(t, base, "init", "-q", repo)
	runGit(t, repo, "remote", "add", "origin", "https://github.com/someone/other.git")
	if err := PublishMapThumbnails(context.Background(), base, repo, "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "does not look like") {
		t.Fatalf("foreign repo: err = %v", err)
	}

	runGit(t, repo, "remote", "set-url", "origin", "https://github.com/x/cs2-server-manager.git")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishMapThumbnails(context.Background(), base, repo, "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty repo: err = %v", err)
	}
}
