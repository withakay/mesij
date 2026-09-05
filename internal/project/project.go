package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const markerName = ".mesij-project"

// Context describes the project and current worktree. Default databases live
// outside repositories and are keyed by one canonical project locator.
type Context struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Root       string `json:"root"`
	CommonDir  string `json:"common_dir,omitempty"`
	Database   string `json:"database"`
	Invocation string `json:"invocation_dir"`
	Worktree   string `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Source     Source `json:"source"`
}

type marker struct {
	Name string `json:"name"`
}

func Discover(dir, databaseOverride, projectSelector string) (Context, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return Context{}, fmt.Errorf("get working directory: %w", err)
		}
	}
	invocation, err := CanonicalPath(dir)
	if err != nil {
		return Context{}, fmt.Errorf("resolve invocation directory: %w", err)
	}

	if projectSelector == "" {
		projectSelector = os.Getenv("MESIJ_PROJECT")
	}
	projectDir := invocation
	projectName := projectSelector
	pathSelected := false
	switch {
	case strings.HasPrefix(projectSelector, "name:"):
		projectName = strings.TrimPrefix(projectSelector, "name:")
	case strings.HasPrefix(projectSelector, "path:"):
		pathSelected = true
		projectName = ""
		projectDir, err = CanonicalPath(resolveFrom(invocation, strings.TrimPrefix(projectSelector, "path:")))
		if err != nil {
			return Context{}, fmt.Errorf("resolve project path: %w", err)
		}
		info, statErr := os.Stat(projectDir)
		if statErr != nil || !info.IsDir() {
			return Context{}, fmt.Errorf("project path %q is not a directory", projectDir)
		}
	}

	database := databaseOverride
	if database == "" {
		database = os.Getenv("MESIJ_DB")
	}
	if database != "" {
		database, err = CanonicalPath(resolveFrom(invocation, database))
		if err != nil {
			return Context{}, fmt.Errorf("resolve database path: %w", err)
		}
	}
	databaseExplicit := database != ""

	root, gitErr := git(projectDir, "rev-parse", "--show-toplevel")
	if gitErr == nil {
		return discoverGit(invocation, projectDir, root, database, projectName, databaseExplicit)
	}
	return discoverPath(invocation, projectDir, database, projectName, pathSelected, databaseExplicit)
}

func discoverGit(invocation, projectDir, root, database, projectName string, databaseExplicit bool) (Context, error) {
	root, err := CanonicalPath(root)
	if err != nil {
		return Context{}, fmt.Errorf("resolve Git root: %w", err)
	}
	common, err := git(projectDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Context{}, fmt.Errorf("locate Git common directory: %w", err)
	}
	common, err = CanonicalPath(common)
	if err != nil {
		return Context{}, fmt.Errorf("resolve Git common directory: %w", err)
	}
	branch, _ := git(projectDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	commit, _ := git(projectDir, "rev-parse", "--verify", "HEAD")
	if projectName == "" {
		remote, _ := git(projectDir, "remote", "get-url", "origin")
		projectName = defaultProjectName(common, remote)
	}
	if database == "" {
		legacy := filepath.Join(common, "mesij", "events.sqlite3")
		if _, err := os.Stat(legacy); err == nil {
			database = legacy
		}
	}
	return makeContext(invocation, root, common, database, projectName, common, branch, commit, databaseExplicit)
}

func discoverPath(invocation, projectDir, database, projectName string, pathSelected, databaseExplicit bool) (Context, error) {
	root := projectDir
	markerName := ""
	if !pathSelected {
		if markerRoot, name, ok := findMarker(projectDir); ok {
			root, markerName = markerRoot, name
		}
	}
	if projectName == "" {
		if markerName != "" && !pathSelected {
			projectName = markerName
		} else {
			projectName = filepath.Base(root)
		}
	}
	return makeContext(invocation, root, "", database, projectName, root, "", "", databaseExplicit)
}

func makeContext(invocation, root, common, database, name, locator, branch, commit string, databaseExplicit bool) (Context, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Context{}, errors.New("project name cannot be empty")
	}
	if databaseExplicit {
		locator = database
	}
	sum := sha256.Sum256([]byte(locator + "\x00" + name))
	id := hex.EncodeToString(sum[:16])
	if database == "" {
		home, err := dataHome()
		if err != nil {
			return Context{}, err
		}
		database = filepath.Join(home, "projects", sanitize(name)+"-"+id+".sqlite3")
	}
	return Context{
		Name: name, ID: id, Root: root, CommonDir: common, Database: database,
		Invocation: invocation, Worktree: root, Branch: branch, Commit: commit,
		Source: ObserveSource(),
	}, nil
}

// Init writes a marker that lets nested non-Git paths resolve one project.
// Git projects already have a stable common-directory identity.
func Init(path, name string) error {
	root, err := CanonicalPath(path)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	if _, err := git(root, "rev-parse", "--git-common-dir"); err == nil {
		return nil
	}
	if name == "" {
		name = filepath.Base(root)
	}
	data, err := json.MarshalIndent(marker{Name: name}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, markerName), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write project marker: %w", err)
	}
	return nil
}

// CanonicalPath resolves symlinks through the longest existing parent. This
// also canonicalizes planned files that do not exist yet.
func CanonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	current := abs
	var suffix []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func dataHome() (string, error) {
	if home := os.Getenv("MESIJ_HOME"); home != "" {
		return CanonicalPath(home)
	}
	if runtime.GOOS == "linux" {
		if home := os.Getenv("XDG_DATA_HOME"); home != "" {
			return filepath.Join(home, "mesij"), nil
		}
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return filepath.Join(userHome, ".local", "share", "mesij"), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(config, "mesij"), nil
}

func findMarker(start string) (string, string, bool) {
	for dir := start; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, markerName))
		if err == nil {
			var value marker
			if json.Unmarshal(data, &value) == nil && strings.TrimSpace(value.Name) != "" {
				return dir, value.Name, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
	}
}

// defaultProjectName derives one name shared by every worktree of a repository.
// It prefers the origin remote (org/repo joined with "-") and otherwise uses
// the Git common directory. It never uses a worktree directory name, because
// linked worktrees must resolve to the same project.
func defaultProjectName(common, remote string) string {
	if name := remoteProjectName(remote); name != "" {
		return name
	}
	base := filepath.Base(common)
	// ".git" or a hidden bare directory such as ".bare": name the parent.
	if strings.HasPrefix(base, ".") {
		return filepath.Base(filepath.Dir(common))
	}
	// Bare clone such as "repo.git".
	return strings.TrimSuffix(base, ".git")
}

// remoteProjectName extracts "org-repo" from a Git remote URL. Hosted URLs
// (https, ssh, scp-like) contribute the last two path segments; local paths
// contribute only the repository directory name.
func remoteProjectName(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	hosted := false
	path := remote
	if i := strings.Index(remote, "://"); i >= 0 {
		rest := remote[i+3:]
		hosted = !strings.HasPrefix(remote, "file://")
		if j := strings.Index(rest, "/"); j >= 0 {
			path = rest[j:]
		} else {
			path = ""
		}
	} else if i := strings.Index(remote, ":"); i > 1 && !strings.ContainsAny(remote[:i], "/\\") {
		// scp-like: [user@]host:org/repo (i > 1 skips Windows drive letters)
		hosted = true
		path = remote[i+1:]
	}
	var parts []string
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "" || part == "_git" { // Azure DevOps inserts "_git"
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	repo := strings.TrimSuffix(parts[len(parts)-1], ".git")
	if repo == "" {
		return ""
	}
	if hosted && len(parts) >= 2 {
		return parts[len(parts)-2] + "-" + repo
	}
	return repo
}

func sanitize(value string) string {
	var result strings.Builder
	dash := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			result.WriteRune(r)
			dash = false
		} else if result.Len() > 0 && !dash {
			result.WriteByte('-')
			dash = true
		}
	}
	cleaned := strings.Trim(result.String(), "-")
	if cleaned == "" {
		return "project"
	}
	return cleaned
}

func resolveFrom(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
