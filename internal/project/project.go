package project

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Context describes the Git project and the current worktree. The database is
// stored below Git's common directory so all linked worktrees see one log.
type Context struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Root       string `json:"root"`
	CommonDir  string `json:"common_dir"`
	Database   string `json:"database"`
	Invocation string `json:"invocation_dir"`
	Worktree   string `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

func Discover(dir, databaseOverride, projectName string) (Context, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return Context{}, err
		}
	}

	dir, _ = filepath.Abs(dir)
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return discoverWithoutGit(dir, databaseOverride, projectName)
	}
	root, _ = filepath.Abs(root)

	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Context{}, fmt.Errorf("locate Git common directory: %w", err)
	}
	common, _ = filepath.Abs(common)

	branch, _ := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	commit, _ := git(dir, "rev-parse", "--verify", "HEAD")

	database := databaseOverride
	if database == "" {
		database = os.Getenv("MESIJ_DB")
	}
	if database == "" {
		database = filepath.Join(common, "mesij", "events.sqlite3")
	} else {
		database, _ = filepath.Abs(database)
	}

	if projectName == "" {
		projectName = os.Getenv("MESIJ_PROJECT")
	}
	if projectName == "" {
		projectName = defaultProjectName(common, root)
	}
	if strings.TrimSpace(projectName) == "" {
		return Context{}, errors.New("project name cannot be empty")
	}

	sum := sha256.Sum256([]byte(common + "\x00" + projectName))
	return Context{
		Name:       projectName,
		ID:         hex.EncodeToString(sum[:16]),
		Root:       root,
		CommonDir:  common,
		Database:   database,
		Invocation: dir,
		Worktree:   root,
		Branch:     branch,
		Commit:     commit,
	}, nil
}

func discoverWithoutGit(dir, databaseOverride, projectName string) (Context, error) {
	database := databaseOverride
	if database == "" {
		database = os.Getenv("MESIJ_DB")
	}
	if database == "" {
		return Context{}, errors.New("mesij must be run inside a Git worktree unless --db or MESIJ_DB is set")
	}
	database, _ = filepath.Abs(database)
	if projectName == "" {
		projectName = os.Getenv("MESIJ_PROJECT")
	}
	if projectName == "" {
		projectName = filepath.Base(dir)
	}
	if strings.TrimSpace(projectName) == "" {
		return Context{}, errors.New("project name cannot be empty")
	}
	sum := sha256.Sum256([]byte(database + "\x00" + projectName))
	return Context{
		Name: projectName, ID: hex.EncodeToString(sum[:16]), Root: dir, Database: database,
		Invocation: dir, Worktree: dir,
	}, nil
}

func defaultProjectName(common, root string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common))
	}
	if name := strings.TrimSuffix(filepath.Base(common), ".git"); name != "" {
		return name
	}
	return filepath.Base(root)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
