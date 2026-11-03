package rename

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/gittest"
)

// cfg is the rename of the fork's .github/fork-sync.yaml, shortened.
var cfg = config.Rename{
	Files: []string{"*.go", "*.yaml", "docs/*.md", "PROJECT"},
	Replace: []config.Replacement{
		{From: "source.toolkit.fluxcd.io", To: "cd.qdrant.io"},
		{From: "domain: toolkit.fluxcd.io", To: "domain: qdrant.io"},
		{From: "group: source", To: "group: cd"},
	},
	ShortNames: []config.Replacement{
		{From: "gitrepo", To: "qdrantgitrepo"},
		{From: "hc", To: "qdranthc"},
	},
	CRDDir: "config/crd/bases",
}

const upstreamCRD = `spec:
  group: source.toolkit.fluxcd.io
  names:
    shortNames:
    - gitrepo
    singular: gitrepository
`

const renamedCRD = `spec:
  group: cd.qdrant.io
  names:
    shortNames:
    - qdrantgitrepo
    singular: gitrepository
`

func TestContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "replacements, in order",
			content: "domain: toolkit.fluxcd.io\ngroup: source\n",
			want:    "domain: qdrant.io\ngroup: cd\n",
		},
		{
			name:    "a CRD",
			content: upstreamCRD,
			want:    renamedCRD,
		},
		{
			name:    "only whole short names are renamed",
			content: "shortNames:\n- gitrepo\n- gitrepository\n",
			want:    "shortNames:\n- qdrantgitrepo\n- gitrepository\n",
		},
		{
			name:    "only the entries of shortNames lists are renamed",
			content: "shortNames:\n- hc\ncategories:\n- hc\n",
			want:    "shortNames:\n- qdranthc\ncategories:\n- hc\n",
		},
		{
			name:    "renamed content stays as it is",
			content: renamedCRD,
			want:    renamedCRD,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Content(cfg, tt.content))
		})
	}
}

func TestApply(t *testing.T) {
	const upstreamGroup = "group: source.toolkit.fluxcd.io\n"
	const renamedGroup = "group: cd.qdrant.io\n"

	// The files of the repository, and what they contain after the rename.
	files := []struct {
		path    string
		content string
		want    string
	}{
		{path: "main.go", content: upstreamGroup, want: renamedGroup},
		{path: "PROJECT", content: upstreamGroup, want: renamedGroup},
		{path: "docs/spec/v1/spec.md", content: upstreamGroup, want: renamedGroup},
		{path: "README.md", content: upstreamGroup, want: upstreamGroup},                 // not in cfg.Files
		{path: ".github/workflows/ci.yaml", content: upstreamGroup, want: upstreamGroup}, // owned
		{path: "tools/sync/sync_test.go", content: upstreamGroup, want: upstreamGroup},   // owned
		{path: "unchanged.go", content: "package unchanged\n", want: "package unchanged\n"},
		// Upstream's CRD stays, and the renamed one is written next to it.
		{path: "config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml", content: upstreamCRD, want: upstreamCRD},
	}
	repo := gittest.New(t)
	for _, f := range files {
		gittest.Apply(t, repo, gittest.Write(f.path, f.content))
	}
	gittest.Commit(t, repo, "upstream", gittest.Write(".gitignore", "ignored.yaml\n"))
	gittest.Apply(t, repo,
		gittest.Write("untracked.yaml", upstreamGroup),
		gittest.Write("ignored.yaml", upstreamGroup),
	)

	written, err := Apply(repo, cfg, []string{".github/", "tools/"})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"main.go", "PROJECT", "docs/spec/v1/spec.md", "untracked.yaml",
		"config/crd/bases/cd.qdrant.io_gitrepositories.yaml",
	}, written)
	for _, f := range files {
		assert.Equal(t, f.want, gittest.Read(t, repo, f.path), f.path)
	}
	assert.Equal(t, renamedCRD, gittest.Read(t, repo, "config/crd/bases/cd.qdrant.io_gitrepositories.yaml"))
	assert.Equal(t, renamedGroup, gittest.Read(t, repo, "untracked.yaml"), "untracked files are renamed")
	assert.Equal(t, upstreamGroup, gittest.Read(t, repo, "ignored.yaml"), "ignored files are not")

	written, err = Apply(repo, cfg, []string{".github/", "tools/"})
	require.NoError(t, err)
	assert.Empty(t, written, "a second run changes nothing")
}

func TestApplySkipsDeletedFiles(t *testing.T) {
	repo := gittest.New(t)
	gittest.Commit(t, repo, "upstream", gittest.Write("deleted.yaml", "group: source.toolkit.fluxcd.io\n"))
	gittest.Apply(t, repo, gittest.Remove("deleted.yaml"))

	written, err := Apply(repo, cfg, nil)
	require.NoError(t, err)
	assert.Empty(t, written)
}

func TestTarget(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{
			name: "a file is renamed in place",
			file: "api/v1/gitrepository_types.go",
			want: "api/v1/gitrepository_types.go",
		},
		{
			name: "upstream's CRD gets a renamed copy",
			file: "config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml",
			want: "config/crd/bases/cd.qdrant.io_gitrepositories.yaml",
		},
		{
			name: "the renamed copy is renamed in place",
			file: "config/crd/bases/cd.qdrant.io_gitrepositories.yaml",
			want: "config/crd/bases/cd.qdrant.io_gitrepositories.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Target(cfg, tt.file))
		})
	}
}
