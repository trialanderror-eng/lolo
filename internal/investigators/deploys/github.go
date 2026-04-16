package deploys

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.github.com"

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClient(token string) *client {
	return &client{
		baseURL: defaultBaseURL,
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

type pullRequest struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	HTMLURL  string    `json:"html_url"`
	MergedAt time.Time `json:"merged_at"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type release struct {
	Name        string    `json:"name"`
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

// listMergedPRs returns merged PRs targeting the default branch, sorted by
// merge time descending. The window is applied client-side because the
// GitHub search API is rate-limited differently and less precise.
func (c *client) listMergedPRs(ctx context.Context, repo string) ([]pullRequest, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls?state=closed&sort=updated&direction=desc&per_page=100", c.baseURL, repo)
	var prs []pullRequest
	if err := c.getJSON(ctx, url, &prs); err != nil {
		return nil, err
	}
	out := prs[:0]
	for _, p := range prs {
		if !p.MergedAt.IsZero() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (c *client) listReleases(ctx context.Context, repo string) ([]release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=100", c.baseURL, repo)
	var rs []release
	if err := c.getJSON(ctx, url, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func (c *client) getJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
