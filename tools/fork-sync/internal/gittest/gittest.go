// Package gittest creates git repositories for tests.
package gittest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
)

// New returns a new repository, on branch main, in a temporary directory. It
// keeps the user's git configuration out of the test.
func New(t *testing.T) git.Repo {
	t.Helper()
	isolate(t)
	repo := git.Repo{Dir: t.TempDir()}
	Run(t, repo, "init", "--quiet", "--initial-branch", "main")
	return repo
}

// NewBare returns a new bare repository, to push to.
func NewBare(t *testing.T) git.Repo {
	t.Helper()
	isolate(t)
	repo := git.Repo{Dir: t.TempDir()}
	Run(t, repo, "init", "--quiet", "--bare")
	return repo
}

// Run runs git in repo and returns its output.
func Run(t *testing.T, repo git.Repo, args ...string) string {
	t.Helper()
	out, err := repo.Run(args...)
	require.NoError(t, err)
	return out
}

// Change is a change to a file of a repository: new content, or its removal.
type Change struct {
	Path    string
	Content string
	Removed bool
}

// Write is a change that writes content to the file at path.
func Write(path, content string) Change {
	return Change{Path: path, Content: content}
}

// Remove is a change that removes the file at path.
func Remove(path string) Change {
	return Change{Path: path, Removed: true}
}

// Apply makes the changes in the working tree of repo.
func Apply(t *testing.T, repo git.Repo, changes ...Change) {
	t.Helper()
	for _, c := range changes {
		file := filepath.Join(repo.Dir, c.Path)
		if c.Removed {
			require.NoError(t, os.Remove(file))
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		require.NoError(t, os.WriteFile(file, []byte(c.Content), 0o644))
	}
}

// Commit makes the changes in repo and commits them.
func Commit(t *testing.T, repo git.Repo, message string, changes ...Change) {
	t.Helper()
	Apply(t, repo, changes...)
	Run(t, repo, "add", "--all")
	Run(t, repo, "commit", "--quiet", "--message", message)
}

// Read returns the content of the file at path in the working tree of repo.
func Read(t *testing.T, repo git.Repo, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo.Dir, path))
	require.NoError(t, err)
	return string(data)
}

func isolate(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
}
