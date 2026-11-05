package forksync

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
)

func TestAdaptationsMessage(t *testing.T) {
	msg := adaptationsMessage(Result{
		Restored:          []git.Change{{Status: "M", Path: ".github/workflows/scan.yaml"}, {Status: "D", Path: ".github/workflows/new.yaml"}},
		FailedAdaptations: []string{"make tidy"},
	})
	assert.Equal(t, `Reapplies what the fork owns on top of the upstream merge.

Fork-owned paths, put back as the fork has them (status is relative to the merge commit):
M	.github/workflows/scan.yaml
D	.github/workflows/new.yaml

make tidy failed, so its changes are not included.`, msg)
}

func TestPRBody(t *testing.T) {
	t.Run("upstream's changes to the fork's workflows come with the command to take them", func(t *testing.T) {
		body, err := PRBody("TEMPLATE\n", Result{
			Tag:    "v1.10.1",
			Synced: "v1.9.3",
			Restored: []git.Change{
				{Status: "M", Path: ".github/workflows/test.yaml"},
				{Status: "M", Path: ".github/workflows/e2e.yaml"},
				{Status: "D", Path: ".github/workflows/scan.yaml"}, // deleted in the fork
				{Status: "M", Path: ".github/labels.yaml"},         // not a workflow
			},
		}, "https://github.com/fluxcd/source-controller.git")
		require.NoError(t, err)
		assert.Contains(t, body, "`.github/workflows/test.yaml`, `.github/workflows/e2e.yaml`. They are not taken automatically.")
		assert.Contains(t, body, "> git fetch --no-tags https://github.com/fluxcd/source-controller.git \"+refs/tags/*:refs/upstream-tags/*\"\n"+
			"> git diff refs/upstream-tags/v1.9.3 refs/upstream-tags/v1.10.1 -- .github/workflows/test.yaml .github/workflows/e2e.yaml | git apply -3\n")
		assert.NotContains(t, body, "scan.yaml")
		assert.NotContains(t, body, "labels.yaml")
	})

	t.Run("a clean merge is just the template", func(t *testing.T) {
		body, err := PRBody("Merges [{{ .Tag }}]({{ .UpstreamURL }}/releases/tag/{{ .Tag }}).\n", Result{Tag: "v1.9.6"},
			"https://github.com/fluxcd/source-controller.git")
		require.NoError(t, err)
		assert.Equal(t, "Merges [v1.9.6](https://github.com/fluxcd/source-controller/releases/tag/v1.9.6).\n", body)
	})

	t.Run("what needs attention comes first", func(t *testing.T) {
		body, err := PRBody("TEMPLATE {{ .Tag }}\n", Result{
			Tag:               "v1.9.6",
			Conflicts:         []string{"a.go", "b.yaml"},
			FailedAdaptations: []string{"make tidy"},
		}, "x")
		require.NoError(t, err)
		assert.Contains(t, body, "**`make tidy` failed**")
		assert.Contains(t, body, "> - `a.go`\n> - `b.yaml`\n")
		assert.Contains(t, body, ConflictsFile)
		assert.Contains(t, body, "`go -C tools/fork-sync run . --repo ../.. rename`", "how to finish the sync by hand")
		assert.True(t, strings.HasSuffix(body, "TEMPLATE v1.9.6\n"), "the template comes last")
	})

	t.Run("a template using an unknown field is an error", func(t *testing.T) {
		_, err := PRBody("{{ .Nope }}", Result{}, "x")
		require.Error(t, err)
	})
}

func TestSummary(t *testing.T) {
	summary := Summary(Result{Tag: "v1.9.6", Status: StatusMerged}, "abc1234 chore: merge upstream tag v1.9.6\n", "The body.\n")
	assert.Equal(t, "## Sync upstream v1.9.6\n\n"+
		"Merge status: **merged**.\n\n"+
		"```\nabc1234 chore: merge upstream tag v1.9.6\n```\n\n"+
		"### Pull request body\n\nThe body.\n", summary)
}
