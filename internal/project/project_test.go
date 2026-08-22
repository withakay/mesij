package project

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDiscoverSharesDatabaseAcrossWorktreesAndScopesProjectNames(t *testing.T) {
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
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "shared.sqlite3")
	got, err := Discover(dir, db, "project")
	if err != nil {
		t.Fatal(err)
	}
	if got.Database != db || got.Name != "project" || got.Worktree != dir {
		t.Fatalf("context = %+v", got)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
