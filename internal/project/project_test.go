package project

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSharesDatabaseAcrossWorktreesAndScopesProjectNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESIJ_HOME", home)
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	repo := filepath.Join(t.TempDir(), "repo")
	run(t, "git", "init", repo)
	run(t, "git", "-C", repo, "config", "user.email", "test@example.com")
	run(t, "git", "-C", repo, "config", "user.name", "Test")
	run(t, "git", "-C", repo, "commit", "--allow-empty", "-m", "initial")
	worktree := filepath.Join(filepath.Dir(repo), "worktree")
	run(t, "git", "-C", repo, "worktree", "add", "-b", "other", worktree)

	defaultMain, err := Discover(repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defaultLinked, err := Discover(worktree, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if defaultMain.Name != defaultLinked.Name || defaultMain.ID != defaultLinked.ID {
		t.Fatalf("default project must be shared across worktrees: main=%+v linked=%+v", defaultMain, defaultLinked)
	}

	main, err := Discover(repo, "", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := Discover(worktree, "", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	if main.ID != linked.ID || main.Database != linked.Database || main.CommonDir != linked.CommonDir {
		t.Fatalf("contexts should share project log:\nmain=%+v\nlinked=%+v", main, linked)
	}
	if !strings.HasPrefix(main.Database, filepath.Join(canonical(t, home), "projects")+string(filepath.Separator)) {
		t.Fatalf("database %q is not in external project storage", main.Database)
	}
	if strings.HasPrefix(main.Database, main.CommonDir+string(filepath.Separator)) {
		t.Fatalf("database %q is inside Git common directory %q", main.Database, main.CommonDir)
	}
	if main.Worktree == linked.Worktree {
		t.Fatal("worktree identity should differ")
	}
	other, err := Discover(repo, "", "another-project")
	if err != nil {
		t.Fatal(err)
	}
	if main.ID == other.ID {
		t.Fatal("project names should create independent streams")
	}
}

func TestDiscoverOutsideGitWithExplicitDatabase(t *testing.T) {
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "shared.sqlite3")
	got, err := Discover(dir, db, "project")
	if err != nil {
		t.Fatal(err)
	}
	if got.Database != canonical(t, db) || got.Name != "project" || got.Worktree != canonical(t, dir) {
		t.Fatalf("context = %+v", got)
	}
}

func TestExplicitDatabaseUsesSameProjectIDInsideAndOutsideGit(t *testing.T) {
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	repo := filepath.Join(t.TempDir(), "repo")
	run(t, "git", "init", repo)
	database := filepath.Join(t.TempDir(), "shared.sqlite3")

	inside, err := Discover(repo, database, "project")
	if err != nil {
		t.Fatal(err)
	}
	outside, err := Discover(t.TempDir(), database, "project")
	if err != nil {
		t.Fatal(err)
	}
	if inside.ID != outside.ID {
		t.Fatalf("same database and project resolved to different IDs: %q != %q", inside.ID, outside.ID)
	}
}

func TestDiscoverPreservesLegacyGitDatabaseAndProjectID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESIJ_HOME", home)
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	repo := filepath.Join(t.TempDir(), "repo")
	run(t, "git", "init", repo)
	commonOutput, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	common := canonical(t, strings.TrimSpace(string(commonOutput)))
	legacy := filepath.Join(common, "mesij", "events.sqlite3")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(repo, "", "legacy-project")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(common + "\x00legacy-project"))
	wantID := hex.EncodeToString(sum[:16])
	if got.Database != legacy || got.ID != wantID {
		t.Fatalf("legacy context = %+v, want database %q and id %q", got, legacy, wantID)
	}
}

func TestDiscoverNonGitProjectFromMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESIJ_HOME", home)
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "service")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root, "plain-project"); err != nil {
		t.Fatal(err)
	}

	got, err := Discover(nested, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "plain-project" || got.Root != canonical(t, root) {
		t.Fatalf("context = %+v", got)
	}
	if !strings.HasPrefix(got.Database, filepath.Join(canonical(t, home), "projects")+string(filepath.Separator)) {
		t.Fatalf("database %q is not external", got.Database)
	}
}

func TestProjectSelectorDisambiguatesNameAndPath(t *testing.T) {
	t.Setenv("MESIJ_HOME", t.TempDir())
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	root := t.TempDir()
	path := filepath.Join(root, "checkout")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	named, err := Discover(root, "", "name:checkout")
	if err != nil {
		t.Fatal(err)
	}
	pathProject, err := Discover(root, "", "path:checkout")
	if err != nil {
		t.Fatal(err)
	}
	if named.Name != "checkout" || pathProject.Root != canonical(t, path) || named.ID == pathProject.ID {
		t.Fatalf("selectors not disambiguated: named=%+v path=%+v", named, pathProject)
	}
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
