// Package github talks to the GitHub API about sync pull requests.
package github

import (
	"context"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v88/github"
)

// Client works on the pull requests of one repository.
type Client struct {
	api   *gh.Client
	owner string
	repo  string
}

// New returns a client for repository ("owner/name"), authenticated with token.
// apiURL overrides the API's address, as GitHub Actions passes it in
// GITHUB_API_URL; empty means api.github.com.
func New(token, repository, apiURL string) (*Client, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("repository %q is not owner/name", repository)
	}
	opts := []gh.ClientOptionsFunc{gh.WithAuthToken(token)}
	if apiURL != "" {
		opts = append(opts, gh.WithURLs(&apiURL, nil))
	}
	api, err := gh.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create the GitHub client: %w", err)
	}
	return &Client{api: api, owner: owner, repo: repo}, nil
}

// PullRequestExists reports whether a pull request with branch as its head
// exists, in any state: a pull request closed without merging is a decision
// not to take that release.
func (c *Client) PullRequestExists(ctx context.Context, branch string) (bool, error) {
	prs, _, err := c.api.PullRequests.List(ctx, c.owner, c.repo, &gh.PullRequestListOptions{
		State:       "all",
		Head:        c.owner + ":" + branch,
		ListOptions: gh.ListOptions{PerPage: 1},
	})
	if err != nil {
		return false, fmt.Errorf("failed to list pull requests: %w", err)
	}
	return len(prs) > 0, nil
}

// CreatePullRequest opens a pull request from branch into base and returns its URL.
func (c *Client) CreatePullRequest(ctx context.Context, base, branch, title, body string, draft bool) (string, error) {
	pr, _, err := c.api.PullRequests.Create(ctx, c.owner, c.repo, &gh.NewPullRequest{
		Title: new(title),
		Head:  new(branch),
		Base:  new(base),
		Body:  new(body),
		Draft: new(draft),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create the pull request: %w", err)
	}
	return pr.GetHTMLURL(), nil
}
