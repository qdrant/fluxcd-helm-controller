package git_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/gittest"
)

func TestPathsKeepUnusualNames(t *testing.T) {
	repo := gittest.New(t)
	gittest.Apply(t, repo, gittest.Write("schéma.yaml", "x"), gittest.Write("with space.txt", "x"))
	gittest.Run(t, repo, "add", "--all")

	paths, err := repo.Paths("diff", "--cached", "--name-only", "-z")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"schéma.yaml", "with space.txt"}, paths)
}

func TestNameStatus(t *testing.T) {
	repo := gittest.New(t)
	gittest.Commit(t, repo, "base", gittest.Write("kept", "1"), gittest.Write("removed", "1"))
	gittest.Apply(t, repo, gittest.Write("kept", "2"), gittest.Write("added", "1"), gittest.Remove("removed"))
	gittest.Run(t, repo, "add", "--all")

	changes, err := repo.NameStatus("--cached")
	require.NoError(t, err)
	assert.ElementsMatch(t, []git.Change{{Status: "A", Path: "added"}, {Status: "M", Path: "kept"}, {Status: "D", Path: "removed"}}, changes)
}

func TestRunError(t *testing.T) {
	repo := gittest.New(t)

	_, err := repo.Run("rev-parse", "--verify", "no-such-ref")
	require.ErrorContains(t, err, "git rev-parse")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.False(t, repo.Succeeds("rev-parse", "--verify", "no-such-ref"))
}

func TestPushWithToken(t *testing.T) {
	const token = "ghp_secretsecretsecret"
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))

	t.Run("pushes the branch", func(t *testing.T) {
		repo := gittest.New(t)
		remote := gittest.NewBare(t)
		gittest.Run(t, repo, "remote", "add", "origin", remote.Dir)
		gittest.Commit(t, repo, "first", gittest.Write("main.go", "package main\n"))

		require.NoError(t, repo.PushWithToken("main", token))
		assert.True(t, remote.Succeeds("rev-parse", "--verify", "refs/heads/main"))
	})

	t.Run("replaces the branch, without running hooks", func(t *testing.T) {
		repo := gittest.New(t)
		remote := gittest.NewBare(t)
		gittest.Run(t, repo, "remote", "add", "origin", remote.Dir)
		gittest.Commit(t, repo, "first", gittest.Write("main.go", "package main\n"))
		require.NoError(t, repo.PushWithToken("main", token))

		gittest.Run(t, repo, "commit", "--quiet", "--amend", "--message", "first, amended")
		gittest.Apply(t, repo, gittest.Write(".git/hooks/pre-push", "#!/bin/sh\nexit 1\n"))
		require.NoError(t, os.Chmod(filepath.Join(repo.Dir, ".git/hooks/pre-push"), 0o755))

		require.NoError(t, repo.PushWithToken("main", token))
		assert.Equal(t, gittest.Run(t, repo, "rev-parse", "HEAD"), gittest.Run(t, remote, "rev-parse", "main"))
	})

	t.Run("the token never shows up in errors or logs", func(t *testing.T) {
		var log bytes.Buffer
		repo := gittest.New(t)
		repo.Output = &log
		gittest.Run(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))

		err := repo.PushWithToken("main", token)
		require.Error(t, err)
		for _, secret := range []string{token, encoded} {
			assert.NotContains(t, err.Error(), secret)
			assert.NotContains(t, log.String(), secret)
		}
	})
}
