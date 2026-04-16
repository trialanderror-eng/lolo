// Package deploys is an Investigator that surfaces what shipped in the
// incident window: merged PRs and releases on the configured GitHub repos.
package deploys

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Investigator struct {
	client       *client
	defaultRepos []string
	skipReason   string // set if construction-time misconfigured; we still implement the interface
}

// New constructs the investigator. token is a GitHub PAT (or app token); repos
// are owner/name strings used when the incident itself doesn't name any.
// With an empty token the investigator no-ops — main is expected to either
// configure it or leave it out entirely.
func New(token string, repos []string) *Investigator {
	inv := &Investigator{defaultRepos: repos}
	if token == "" {
		inv.skipReason = "no GitHub token configured (set LOLO_GITHUB_TOKEN)"
		return inv
	}
	inv.client = newClient(token)
	return inv
}

func (*Investigator) Name() string { return "github.deploys" }

func (i *Investigator) Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	if i.skipReason != "" {
		log.Printf("github.deploys: skipping — %s", i.skipReason)
		return nil, nil
	}
	repos := repoSet(i.defaultRepos, inc.Scope.Repos)
	if len(repos) == 0 {
		log.Printf("github.deploys: no repos in scope; skipping")
		return nil, nil
	}

	var out []evidence.Evidence
	for _, repo := range repos {
		ev, err := i.investigateRepo(ctx, inc, repo)
		if err != nil {
			log.Printf("github.deploys: %s: %v", repo, err)
			continue
		}
		out = append(out, ev...)
	}
	return out, nil
}

func (i *Investigator) investigateRepo(ctx context.Context, inc incident.Incident, repo string) ([]evidence.Evidence, error) {
	var out []evidence.Evidence

	prs, err := i.client.listMergedPRs(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("list PRs: %w", err)
	}
	for _, p := range prs {
		if !inWindow(p.MergedAt, inc) {
			continue
		}
		out = append(out, evidence.Evidence{
			Source:     "github.deploys",
			Kind:       "merged_pr",
			At:         p.MergedAt,
			Confidence: confidence(p.MergedAt, inc),
			Summary:    fmt.Sprintf("%s: PR #%d merged — %s", repo, p.Number, p.Title),
			Data: map[string]any{
				"repo":   repo,
				"number": p.Number,
				"title":  p.Title,
				"author": p.User.Login,
				"base":   p.Base.Ref,
				"labels": labelNames(p.Labels),
			},
			Links: []evidence.Link{{Label: "PR", URL: p.HTMLURL}},
		})
	}

	rels, err := i.client.listReleases(ctx, repo)
	if err != nil {
		return out, fmt.Errorf("list releases: %w", err)
	}
	for _, r := range rels {
		if r.Draft {
			continue
		}
		if !inWindow(r.PublishedAt, inc) {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.TagName
		}
		out = append(out, evidence.Evidence{
			Source:     "github.deploys",
			Kind:       "release",
			At:         r.PublishedAt,
			Confidence: confidence(r.PublishedAt, inc),
			Summary:    fmt.Sprintf("%s: release %s published", repo, name),
			Data: map[string]any{
				"repo":       repo,
				"tag":        r.TagName,
				"name":       r.Name,
				"prerelease": r.Prerelease,
			},
			Links: []evidence.Link{{Label: "release", URL: r.HTMLURL}},
		})
	}

	return out, nil
}

func inWindow(t time.Time, inc incident.Incident) bool {
	if t.IsZero() {
		return false
	}
	return !t.Before(inc.Start()) && !t.After(inc.End())
}

// confidence weights an event by how close it is to the incident trigger,
// inside the window. A deploy 1 minute before the page is much more
// suspicious than one 25 minutes before.
func confidence(at time.Time, inc incident.Incident) float64 {
	gap := inc.End().Sub(at)
	switch {
	case gap < 15*time.Minute:
		return 0.9
	case gap < 30*time.Minute:
		return 0.7
	default:
		return 0.4
	}
}

func repoSet(defaults, fromScope []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, src := range [][]string{defaults, fromScope} {
		for _, r := range src {
			r = strings.TrimSpace(r)
			if r == "" || seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func labelNames(labels []struct {
	Name string `json:"name"`
}) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.Name)
	}
	return out
}
