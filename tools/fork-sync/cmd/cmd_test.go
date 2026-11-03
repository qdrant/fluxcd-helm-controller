package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/gittest"
)

// fakeGitHub answers the pull request API calls of target and publish.
type fakeGitHub struct {
	mu      sync.Mutex
	created []map[string]any
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if len(f.created) == 0 {
			_, _ = w.Write([]byte(`[]`))
		} else {
			_, _ = w.Write([]byte(`[{"number": 1}]`))
		}
	case http.MethodPost:
		var pr map[string]any
		_ = json.NewDecoder(r.Body).Decode(&pr)
		f.created = append(f.created, pr)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url": "https://github.com/qdrant/fork/pull/1"}`))
	}
}

// TestTargetMergePublish runs the commands the way the workflow does, against
// local repositories and a fake GitHub API.
func TestTargetMergePublish(t *testing.T) {
	upstream := gittest.New(t)
	gittest.Commit(t, upstream, "v1.0.0", gittest.Write("main.go", "package main\n"))
	gittest.Run(t, upstream, "tag", "v1.0.0")
	gittest.Commit(t, upstream, "v1.0.1", gittest.Write("main.go", "package main // v1.0.1\n"))
	gittest.Run(t, upstream, "tag", "v1.0.1")

	// The fork is upstream v1.0.0 plus its sync configuration, pushed to origin.
	origin := gittest.NewBare(t)
	fork := gittest.New(t)
	gittest.Run(t, fork, "fetch", "--quiet", upstream.Dir, "refs/tags/v1.0.0")
	gittest.Run(t, fork, "reset", "--quiet", "--hard", "FETCH_HEAD")
	gittest.Commit(t, fork, "fork",
		gittest.Write(config.DefaultPath, "upstream: "+upstream.Dir+"\nprBodyTemplate: .github/pr-body.md\n"),
		gittest.Write(".github/pr-body.md", "Merges {{ .Tag }}.\n"),
	)
	gittest.Run(t, fork, "remote", "add", "origin", origin.Dir)
	gittest.Run(t, fork, "push", "--quiet", "origin", "main")

	api := &fakeGitHub{}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	t.Setenv("GH_TOKEN", "token")
	t.Setenv("GITHUB_REPOSITORY", "qdrant/fork")
	t.Setenv("GITHUB_API_URL", server.URL+"/")
	body := filepath.Join(t.TempDir(), "pr-body.md")
	summary := filepath.Join(t.TempDir(), "summary.md")
	base := strings.TrimSpace(gittest.Run(t, fork, "rev-parse", "HEAD"))

	out := run(t, "target", "--repo", fork.Dir, "--check-existing")
	assert.Equal(t, "tag=v1.0.1\nbranch=sync/upstream-v1.0.1\nstatus=needs-sync\n", out)

	out = run(t, "merge", "v1.0.1", "--repo", fork.Dir, "--body-file", body, "--summary-file", summary)
	assert.Equal(t, "tag=v1.0.1\nbranch=sync/upstream-v1.0.1\nbase="+base+"\nstatus=merged\n", out)
	assert.Equal(t, "Merges v1.0.1.\n", readFile(t, body))
	assert.Contains(t, readFile(t, summary), "chore: merge upstream tag v1.0.1")

	out = run(t, "publish", "--repo", fork.Dir, "--branch", "sync/upstream-v1.0.1", "--body-file", body)
	assert.Equal(t, "url=https://github.com/qdrant/fork/pull/1\n", out)
	assert.True(t, origin.Succeeds("rev-parse", "--verify", "refs/heads/sync/upstream-v1.0.1"), "the branch is pushed")
	require.Len(t, api.created, 1)
	assert.Equal(t, map[string]any{
		"title": "Sync upstream v1.0.1",
		"head":  "sync/upstream-v1.0.1",
		"base":  "main",
		"body":  "Merges v1.0.1.\n",
		"draft": false,
	}, api.created[0])

	gittest.Run(t, fork, "checkout", "--quiet", "main")
	out = run(t, "target", "--repo", fork.Dir, "--check-existing")
	assert.Equal(t, "tag=v1.0.1\nbranch=sync/upstream-v1.0.1\nstatus=exists\n", out, "the pull request exists now")
}

func TestRejectsUnknownLogLevels(t *testing.T) {
	_, err := execute("target", "--repo", t.TempDir(), "--log-level", "loud")
	require.ErrorContains(t, err, "invalid log level")
}

func TestRename(t *testing.T) {
	repo := gittest.New(t)
	gittest.Apply(t, repo,
		gittest.Write(config.DefaultPath, `upstream: x
prBodyTemplate: x.md
rename:
  files: ["*.go"]
  replace: [{from: fluxcd.io, to: qdrant.io}]
`),
		gittest.Write(".github/without-rename.yaml", "upstream: x\nprBodyTemplate: x.md\n"),
		gittest.Write("group.go", "const group = \"fluxcd.io\"\n"),
		gittest.Write("other.go", "const other = 1\n"),
	)

	out := run(t, "rename", "--repo", repo.Dir, "--log-level", "warn")
	assert.Equal(t, "group.go\n", out, "prints the files it wrote")
	assert.Equal(t, "const group = \"qdrant.io\"\n", gittest.Read(t, repo, "group.go"))

	_, err := execute("rename", "--repo", repo.Dir, "--config", ".github/without-rename.yaml")
	require.ErrorContains(t, err, "there is no rename")
}

func TestPublishRejectsOtherBranches(t *testing.T) {
	_, err := execute("publish", "--branch", "main", "--body-file", "x")
	require.ErrorContains(t, err, "must be a sync branch")
}

// run executes the command line and returns what it printed on stdout.
func run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := execute(args...)
	require.NoError(t, err)
	return out
}

func execute(args ...string) (string, error) {
	// Flags keep their values between executions: start from the defaults.
	repoDir, configPath, logLevel = ".", config.DefaultPath, "info"
	targetOpts.tag, targetOpts.checkExisting = "", false
	mergeOpts.bodyFile, mergeOpts.summaryFile = "", ""
	publishOpts.branch, publishOpts.bodyFile = "", ""

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
