package forksync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/gittest"
)

// The tests sync real git repositories: a small upstream and a fork of it.
//
// Upstream v1.0.0 has three files:
//
//	.github/workflows/ci.yaml   upstream ci
//	crd.yaml                    group: source.toolkit.fluxcd.io, version: v1
//	main.go                     package main
//
// The fork starts from v1.0.0. It has its own ci.yaml ("fork ci"), a workflow
// of its own (fork.yaml), crd.yaml with the group renamed, and the sync
// configuration below: it owns .github/. Each test makes upstream v1.0.1 out of
// v1.0.0 plus some changes, sometimes changes the fork too, and then syncs
// v1.0.1.

const syncConfig = `upstream: %s
prBodyTemplate: pr-body.md
ownedPaths:
  - .github/
rename:
  files: ["*.yaml"]
  replace:
    - {from: source.toolkit.fluxcd.io, to: cd.qdrant.io}
adaptations:
  - name: make tidy
    run: [make, tidy]
    allowFailure: true
`

// The commits a sync can make, by subject.
const (
	mergeCommit       = "chore: merge upstream tag v1.0.1"
	adaptationsCommit = "chore: apply Qdrant adaptations after merging v1.0.1"
	conflictCommit    = "chore: merge upstream tag v1.0.1 (unresolved conflicts)"
	keepOwnedCommit   = "chore: keep the fork-owned paths after merging v1.0.1"
)

// absent, as the expected content of a file, means that the file must not exist.
const absent = "<absent>"

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		upstream []change // what upstream v1.0.1 changes
		fork     []change // what the fork changes before the sync

		wantStatus    Status
		wantConflicts []string
		wantCommits   []string
		// wantFiles maps paths to text they contain on the sync branch.
		wantFiles map[string]string
		// wantInMergeCommit maps paths to text they contain in the merge commit.
		wantInMergeCommit map[string]string
		// wantInMessage is text in the message of the last commit.
		wantInMessage []string
	}{
		{
			name:        "upstream changes a file the fork does not own",
			upstream:    []change{write("main.go", "package main // v1.0.1\n")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit},
			wantFiles: map[string]string{
				"main.go":                   "// v1.0.1",
				".github/workflows/ci.yaml": "fork ci",
			},
		},
		{
			name:              "upstream changes a file the fork owns",
			upstream:          []change{write(".github/workflows/ci.yaml", "upstream ci v1.0.1\n")},
			wantStatus:        StatusMerged,
			wantCommits:       []string{mergeCommit, adaptationsCommit},
			wantFiles:         map[string]string{".github/workflows/ci.yaml": "fork ci", ".github/workflows/fork.yaml": "fork only"},
			wantInMergeCommit: map[string]string{".github/workflows/ci.yaml": "upstream ci v1.0.1"},
			wantInMessage:     []string{"M\t.github/workflows/ci.yaml"},
		},
		{
			name:              "upstream adds a file with its group name",
			upstream:          []change{write("new.yaml", "group: source.toolkit.fluxcd.io\n")},
			wantStatus:        StatusMerged,
			wantCommits:       []string{mergeCommit, adaptationsCommit},
			wantFiles:         map[string]string{"new.yaml": "group: cd.qdrant.io"},
			wantInMergeCommit: map[string]string{"new.yaml": "group: source.toolkit.fluxcd.io"},
		},
		{
			name:          "upstream adds a workflow",
			upstream:      []change{write(".github/workflows/new.yaml", "on: push\n")},
			wantStatus:    StatusMerged,
			wantCommits:   []string{mergeCommit, adaptationsCommit},
			wantFiles:     map[string]string{".github/workflows/new.yaml": absent},
			wantInMessage: []string{"D\t.github/workflows/new.yaml"},
		},
		{
			name:        "upstream adds a workflow with a non-ASCII name",
			upstream:    []change{write(".github/workflows/schéma.yaml", "on: push\n")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit, adaptationsCommit},
			wantFiles:   map[string]string{".github/workflows/schéma.yaml": absent},
		},
		{
			name: "upstream renames a file the fork owns",
			upstream: []change{
				remove(".github/workflows/ci.yaml"),
				write(".github/workflows/ci-renamed.yaml", "upstream ci\n"),
			},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit, adaptationsCommit},
			wantFiles: map[string]string{
				".github/workflows/ci.yaml":         "fork ci",
				".github/workflows/ci-renamed.yaml": absent,
			},
		},
		{
			name:        "upstream deletes a file the fork owns",
			upstream:    []change{remove(".github/workflows/ci.yaml")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit, adaptationsCommit},
			wantFiles:   map[string]string{".github/workflows/ci.yaml": "fork ci"},
		},
		{
			name:        "upstream changes the line after one the fork renamed",
			upstream:    []change{write("crd.yaml", "group: source.toolkit.fluxcd.io\nversion: v2\n")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit},
			wantFiles:   map[string]string{"crd.yaml": "group: cd.qdrant.io\nversion: v2\n"},
		},
		{
			name:        "upstream changes a file the fork owns and deleted",
			upstream:    []change{write(".github/workflows/ci.yaml", "upstream ci v1.0.1\n")},
			fork:        []change{remove(".github/workflows/ci.yaml")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit, adaptationsCommit},
			wantFiles:   map[string]string{".github/workflows/ci.yaml": absent},
		},
		{
			name:     "an adaptation allowed to fail drops its changes",
			upstream: []change{write("new.yaml", "group: source.toolkit.fluxcd.io\n")},
			// Like the real Makefile, which deletes go.sum before go mod tidy fails.
			fork:        []change{write("Makefile", "tidy:\n\trm main.go\n\ttouch leftover.txt\n\texit 1\n")},
			wantStatus:  StatusMerged,
			wantCommits: []string{mergeCommit, adaptationsCommit},
			wantFiles: map[string]string{
				"main.go":      "package main",
				"leftover.txt": absent,
				"new.yaml":     "group: cd.qdrant.io",
			},
			wantInMessage: []string{"make tidy failed, so its changes are not included."},
		},
		{
			name:          "a conflict",
			upstream:      []change{write("main.go", "package main // upstream\n")},
			fork:          []change{write("main.go", "package main // fork\n")},
			wantStatus:    StatusConflict,
			wantConflicts: []string{"main.go"},
			wantCommits:   []string{conflictCommit},
			wantFiles:     map[string]string{"main.go": "<<<<<<<", ConflictsFile: "main.go\n"},
		},
		{
			name:          "a conflict without markers: upstream deletes a file the fork changed",
			upstream:      []change{remove("main.go")},
			fork:          []change{write("main.go", "package main // fork\n")},
			wantStatus:    StatusConflict,
			wantConflicts: []string{"main.go"},
			wantCommits:   []string{conflictCommit},
			wantFiles:     map[string]string{ConflictsFile: "main.go\n"},
		},
		{
			name: "a conflict while upstream changes what the fork owns",
			upstream: []change{
				write("main.go", "package main // upstream\n"),
				write(".github/workflows/ci.yaml", "upstream ci v1.0.1\n"),
				write(".github/workflows/new.yaml", "on: push\n"),
			},
			fork:          []change{write("main.go", "package main // fork\n")},
			wantStatus:    StatusConflict,
			wantConflicts: []string{"main.go"},
			wantCommits:   []string{conflictCommit, keepOwnedCommit},
			wantFiles: map[string]string{
				".github/workflows/ci.yaml":  "fork ci",
				".github/workflows/new.yaml": absent,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.upstream, tt.fork)

			res, err := f.syncer.Merge("v1.0.1")
			require.NoError(t, err)

			assert.Equal(t, tt.wantStatus, res.Status)
			assert.Equal(t, "v1.0.0", res.Synced, "the release the fork had")
			assert.Equal(t, tt.wantConflicts, res.Conflicts)
			assert.Equal(t, tt.wantCommits, f.commitSubjects())
			assert.Empty(t, f.git("status", "--porcelain"), "everything is committed")
			for path, want := range tt.wantFiles {
				f.assertFile(path, want)
			}
			for path, want := range tt.wantInMergeCommit {
				assert.Contains(t, f.git("show", f.mergeCommit()+":"+path), want, path)
			}
			for _, want := range tt.wantInMessage {
				assert.Contains(t, f.git("log", "-1", "--format=%B"), want)
			}
		})
	}
}

// TestMergeFromAPatchReleaseToTheNextMinor syncs releases from two upstream
// branches, as upstream releases patches from release branches: the fork syncs
// v1.0.1, from the branch of v1.0.0, then v1.1.0, from another branch that
// started at v1.0.0. Both releases change the same lines, so merging v1.1.0 from
// v1.0.0, where the two meet, conflicts; merging it from v1.0.1 does not.
func TestMergeFromAPatchReleaseToTheNextMinor(t *testing.T) {
	f := newFixture(t, []change{write("main.go", "package main // dependency v2\n")}, nil)
	_, err := f.syncer.Merge("v1.0.1")
	require.NoError(t, err)
	f.git("checkout", "--quiet", "main")
	f.git("merge", "--quiet", "--ff-only", "sync/upstream-v1.0.1")

	gittest.Run(t, f.upstream, "checkout", "--quiet", "-b", "next", "v1.0.0")
	gittest.Commit(t, f.upstream, "v1.1.0",
		write("main.go", "package main // dependency v3\n"),
		write("crd.yaml", "group: source.toolkit.fluxcd.io\nversion: v2\n"),
	)
	gittest.Run(t, f.upstream, "tag", "v1.1.0")

	res, err := f.syncer.Merge("v1.1.0")
	require.NoError(t, err)

	assert.Equal(t, StatusMerged, res.Status)
	assert.Empty(t, res.Conflicts)
	f.assertFile("main.go", "package main // dependency v3\n")
	f.assertFile("crd.yaml", "group: cd.qdrant.io\nversion: v2\n")
}

// TestMergeKeepsOwnedRenamedCopies syncs a release that changes a file the fork
// replaced with a renamed copy it owns. git's rename detection would apply the
// change to the copy, but the fork's version is kept.
func TestMergeKeepsOwnedRenamedCopies(t *testing.T) {
	f := newFixture(t,
		[]change{write(".github/workflows/ci.yaml", "upstream ci\nchanged upstream\n")},
		[]change{remove(".github/workflows/ci.yaml"), write(".github/workflows/ci-copy.yaml", "upstream ci\n")},
	)

	res, err := f.syncer.Merge("v1.0.1")
	require.NoError(t, err)

	assert.Equal(t, StatusMerged, res.Status)
	assert.Equal(t, "upstream ci\n", gittest.Read(t, f.fork, ".github/workflows/ci-copy.yaml"))
	f.assertFile(".github/workflows/ci.yaml", absent)
}

func TestMergeRefusesAWorkingTreeWithChanges(t *testing.T) {
	f := newFixture(t, []change{write("main.go", "package main // v1.0.1\n")}, nil)
	gittest.Apply(t, f.fork, write("notes.txt", "a local file\n"))

	_, err := f.syncer.Merge("v1.0.1")
	require.ErrorContains(t, err, "the working tree has changes")
	assert.Equal(t, "a local file\n", gittest.Read(t, f.fork, "notes.txt"))
}

func TestMergeStopsOnAnAdaptationThatChangesWorkflows(t *testing.T) {
	f := newFixture(t, []change{write("main.go", "package main // v1.0.1\n")},
		[]change{write("Makefile", "tidy:\n\techo 'on: push' > .github/workflows/injected.yaml\n")})

	_, err := f.syncer.Merge("v1.0.1")
	require.ErrorContains(t, err, `the adaptation "make tidy" changed workflows`)
	require.ErrorContains(t, err, ".github/workflows/injected.yaml")
}

func TestMergeStopsOnAFailingAdaptation(t *testing.T) {
	f := newFixture(t, []change{write("main.go", "package main // v1.0.1\n")}, nil)
	f.syncer.Config.Adaptations = append(f.syncer.Config.Adaptations, config.Adaptation{Name: "generate", Run: []string{"false"}})

	_, err := f.syncer.Merge("v1.0.1")
	require.ErrorContains(t, err, `adaptation "generate"`)
	var exitErr *exec.ExitError
	assert.ErrorAs(t, err, &exitErr)
}

func TestFindTarget(t *testing.T) {
	f := newFixture(t, []change{write("main.go", "package main // v1.0.1\n")}, nil)

	target, err := f.syncer.FindTarget("")
	require.NoError(t, err)
	assert.Equal(t, "v1.0.1", target.Tag, "the latest stable release, not the pre-release or the api/ tag")
	assert.Equal(t, "sync/upstream-v1.0.1", target.Branch())
	assert.False(t, target.UpToDate)

	_, err = f.syncer.Merge("v1.0.1")
	require.NoError(t, err)
	target, err = f.syncer.FindTarget("")
	require.NoError(t, err)
	assert.True(t, target.UpToDate, "up to date once the release is merged")

	_, err = f.syncer.FindTarget("v1.0.1;x")
	require.ErrorIs(t, err, ErrInvalidTag)
	_, err = f.syncer.FindTarget("v1.0.2-rc.1")
	require.ErrorIs(t, err, ErrInvalidTag)
	_, err = f.syncer.FindTarget("v9.9.9")
	require.ErrorContains(t, err, "not found")
	_, err = f.syncer.FindTarget("v1.0.0")
	require.ErrorContains(t, err, "older than v1.0.1", "the fork only moves forward")
}

// fixture is an upstream repository and a fork of it, as described at the top
// of this file.
type fixture struct {
	t        *testing.T
	upstream git.Repo
	fork     git.Repo
	base     string // the fork's commit the sync starts from
	syncer   Syncer
}

func newFixture(t *testing.T, upstreamChanges, forkChanges []change) *fixture {
	t.Helper()
	upstream := gittest.New(t)
	gittest.Commit(t, upstream, "v1.0.0",
		write(".github/workflows/ci.yaml", "upstream ci\n"),
		write("crd.yaml", "group: source.toolkit.fluxcd.io\nversion: v1\n"),
		write("main.go", "package main\n"),
	)
	gittest.Run(t, upstream, "tag", "v1.0.0")
	gittest.Commit(t, upstream, "v1.0.1", upstreamChanges...)
	gittest.Run(t, upstream, "tag", "v1.0.1")
	gittest.Run(t, upstream, "tag", "v1.0.2-rc.1") // a pre-release, never synced
	gittest.Run(t, upstream, "tag", "api/v1.0.1")  // the tag of the api/ module, never synced

	fork := gittest.New(t)
	gittest.Run(t, fork, "fetch", "--quiet", upstream.Dir, "refs/tags/v1.0.0")
	gittest.Run(t, fork, "reset", "--quiet", "--hard", "FETCH_HEAD")
	gittest.Commit(t, fork, "fork",
		write(".github/workflows/ci.yaml", "fork ci\n"),
		write(".github/workflows/fork.yaml", "fork only\n"),
		write("crd.yaml", "group: cd.qdrant.io\nversion: v1\n"),
		write("Makefile", "tidy:\n\t@true\n"),
		write(config.DefaultPath, fmt.Sprintf(syncConfig, upstream.Dir)),
	)
	if len(forkChanges) > 0 {
		gittest.Commit(t, fork, "fork changes", forkChanges...)
	}

	cfg, err := config.Load(fork.Dir, config.DefaultPath)
	require.NoError(t, err)
	return &fixture{
		t:        t,
		upstream: upstream,
		fork:     fork,
		base:     strings.TrimSpace(gittest.Run(t, fork, "rev-parse", "HEAD")),
		syncer:   Syncer{Repo: fork, Config: cfg, Logger: testr.New(t)},
	}
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	return gittest.Run(f.t, f.fork, args...)
}

// commitSubjects returns the subjects of the commits the sync made, oldest first.
func (f *fixture) commitSubjects() []string {
	f.t.Helper()
	return strings.Split(strings.TrimSpace(f.git("log", "--reverse", "--first-parent", "--format=%s", f.base+"..HEAD")), "\n")
}

// mergeCommit returns the first commit the sync made, the merge.
func (f *fixture) mergeCommit() string {
	f.t.Helper()
	return strings.Fields(f.git("rev-list", "--reverse", "--first-parent", f.base+"..HEAD"))[0]
}

// assertFile checks that the file at path contains want, or does not exist if
// want is absent.
func (f *fixture) assertFile(path, want string) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.fork.Dir, path))
	if want == absent {
		assert.ErrorIs(f.t, err, os.ErrNotExist, "%s must not exist", path)
		return
	}
	require.NoError(f.t, err)
	assert.Contains(f.t, string(data), want, path)
}

// change, write and remove are short names for the table above.
type change = gittest.Change

var (
	write  = gittest.Write
	remove = gittest.Remove
)
