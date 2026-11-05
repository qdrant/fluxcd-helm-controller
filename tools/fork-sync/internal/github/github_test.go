package github

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := New("token", "qdrant/fork", server.URL+"/")
	require.NoError(t, err)
	return c
}

func TestPullRequestExists(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{name: "no pull request", response: `[]`, want: false},
		{name: "a closed one counts too", response: `[{"number": 7, "state": "closed"}]`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/repos/qdrant/fork/pulls", r.URL.Path)
				assert.Equal(t, "all", r.URL.Query().Get("state"))
				assert.Equal(t, "qdrant:sync/upstream-v1.0.1", r.URL.Query().Get("head"))
				assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(tt.response))
			})
			got, err := c.PullRequestExists(t.Context(), "sync/upstream-v1.0.1")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCreatePullRequest(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/repos/qdrant/fork/pulls", r.URL.Path)
		var got map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, map[string]any{
			"title": "Sync upstream v1.0.1",
			"head":  "sync/upstream-v1.0.1",
			"base":  "main",
			"body":  "body",
			"draft": true,
		}, got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url": "https://github.com/qdrant/fork/pull/8"}`))
	})
	url, err := c.CreatePullRequest(t.Context(), "main", "sync/upstream-v1.0.1", "Sync upstream v1.0.1", "body", true)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/qdrant/fork/pull/8", url)
}

func TestNewRejectsBadRepository(t *testing.T) {
	for _, repository := range []string{"", "qdrant", "/fork", "qdrant/"} {
		_, err := New("token", repository, "")
		require.Error(t, err, repository)
	}
}
