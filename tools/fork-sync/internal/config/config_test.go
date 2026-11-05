package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func load(t *testing.T, content string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, DefaultPath), []byte(content), 0o644))
	return Load(dir, DefaultPath)
}

func TestLoad(t *testing.T) {
	cfg, err := load(t, `
upstream: https://github.com/fluxcd/source-controller.git
ownedPaths: [.github/]
rename:
  files: ["*.go"]
  replace:
    - {from: source.toolkit.fluxcd.io, to: cd.qdrant.io}
  shortNames:
    - {from: gitrepo, to: qdrantgitrepo}
  crdDir: config/crd/bases
adaptations:
  - name: make tidy
    run: [make, tidy]
    allowFailure: true
prBodyTemplate: .github/sync-upstream-pr-body.md
`)
	require.NoError(t, err)
	assert.Equal(t, Config{
		Upstream:   "https://github.com/fluxcd/source-controller.git",
		BaseBranch: "main",
		OwnedPaths: []string{".github/"},
		Rename: &Rename{
			Files:      []string{"*.go"},
			Replace:    []Replacement{{From: "source.toolkit.fluxcd.io", To: "cd.qdrant.io"}},
			ShortNames: []Replacement{{From: "gitrepo", To: "qdrantgitrepo"}},
			CRDDir:     "config/crd/bases",
		},
		Adaptations:    []Adaptation{{Name: "make tidy", Run: []string{"make", "tidy"}, AllowFailure: true}},
		PRBodyTemplate: ".github/sync-upstream-pr-body.md",
	}, cfg)
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"unknown field, e.g. a typo", "upstream: x\nownedPath: [.github/]\n", "ownedpath"},
		{"no upstream", "ownedPaths: [.github/]\n", "upstream is required"},
		{"an adaptation without a name", "upstream: x\nadaptations:\n  - run: [true]\n", "has no name"},
		{"an adaptation with nothing to run", "upstream: x\nadaptations:\n  - name: tidy\n", "has nothing to run"},
		{"not YAML", "upstream: [x\n", "failed to read"},
		{"a rename without files", "upstream: x\nrename:\n  replace: [{from: a, to: b}]\n", "rename has no files"},
		{"no pull request body template", "upstream: x\n", "prBodyTemplate is required"},
		{"a replacement without from", "upstream: x\nrename:\n  files: [\"*.go\"]\n  shortNames: [{to: b}]\n", "nothing to replace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, tt.content)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	_, err := Load(t.TempDir(), DefaultPath)
	require.ErrorContains(t, err, "failed to read "+DefaultPath)
}
