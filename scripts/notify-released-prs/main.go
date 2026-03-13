// Package main comments on pull requests after a release is published.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type config struct {
	Repo       string
	From       string
	To         string
	ReleaseTag string
	ReleaseURL string
	TokenEnv   string
	GitHubAPI  string
}

type githubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type compareResponse struct {
	Commits []struct {
		SHA string `json:"sha"`
	} `json:"commits"`
}

type pullRequest struct {
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	MergedAt *time.Time `json:"merged_at"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "notify-released-prs: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv(cfg.TokenEnv))
	if token == "" {
		return fmt.Errorf("%s is required", cfg.TokenEnv)
	}
	client := githubClient{
		baseURL: strings.TrimRight(cfg.GitHubAPI, "/"),
		token:   token,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
	ctx := context.Background()
	prs, err := client.releasePRs(ctx, cfg.Repo, cfg.From, cfg.To)
	if err != nil {
		return err
	}
	if len(prs) == 0 {
		return nil
	}
	message := fmt.Sprintf("Included in `%s`.\n\nRelease: %s", cfg.ReleaseTag, cfg.ReleaseURL)
	for _, pr := range prs {
		if err := client.commentOnPR(ctx, cfg.Repo, pr, message); err != nil {
			return err
		}
	}
	return nil
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.Repo, "repo", "", "GitHub repo in owner/name form")
	flag.StringVar(&cfg.From, "from", "", "Previous tag")
	flag.StringVar(&cfg.To, "to", "", "Current tag")
	flag.StringVar(&cfg.ReleaseTag, "release-tag", "", "Published release tag")
	flag.StringVar(&cfg.ReleaseURL, "release-url", "", "Published release URL")
	flag.StringVar(&cfg.TokenEnv, "token-env", "GITHUB_TOKEN", "Environment variable containing GitHub token")
	flag.StringVar(&cfg.GitHubAPI, "github-api", "https://api.github.com", "GitHub API base URL")
	flag.Parse()
	if cfg.Repo == "" || cfg.From == "" || cfg.To == "" || cfg.ReleaseTag == "" || cfg.ReleaseURL == "" {
		return config{}, errors.New("required flags: --repo, --from, --to, --release-tag, --release-url")
	}
	if err := validateGitHubAPI(cfg.GitHubAPI); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func validateGitHubAPI(raw string) error {
	switch {
	case strings.HasPrefix(raw, "https://api.github.com"):
		return nil
	case strings.HasPrefix(raw, "http://127.0.0.1"), strings.HasPrefix(raw, "http://localhost"):
		return nil
	default:
		return fmt.Errorf("unsupported GitHub API base URL: %s", raw)
	}
}

func (g githubClient) releasePRs(ctx context.Context, repo, from, to string) ([]int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/compare/%s...%s", g.baseURL, repo, from, to), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	//nolint:gosec // The GitHub API base URL is validated before the client is created.
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("compare tags: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	var compare compareResponse
	if err := json.NewDecoder(resp.Body).Decode(&compare); err != nil {
		return nil, fmt.Errorf("decode compare response: %w", err)
	}
	seen := make(map[int]struct{})
	for _, commit := range compare.Commits {
		for _, pr := range g.associatedPRs(ctx, repo, commit.SHA) {
			if pr.MergedAt == nil || strings.HasPrefix(pr.Title, "chore: release ") {
				continue
			}
			seen[pr.Number] = struct{}{}
		}
	}
	var prs []int
	for number := range seen {
		prs = append(prs, number)
	}
	sort.Ints(prs)
	return prs, nil
}

func (g githubClient) associatedPRs(ctx context.Context, repo, sha string) []pullRequest {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/pulls", g.baseURL, repo, sha), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	//nolint:gosec // The GitHub API base URL is validated before the client is created.
	resp, err := g.client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var pulls []pullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pulls); err != nil {
		return nil
	}
	return pulls
}

func (g githubClient) commentOnPR(ctx context.Context, repo string, number int, message string) error {
	exists, err := g.hasComment(ctx, repo, number, message)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	body, err := json.Marshal(map[string]string{"body": message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.baseURL, repo, number), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	//nolint:gosec // The GitHub API base URL is validated before the client is created.
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("comment on PR #%d: %s (%s)", number, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (g githubClient) hasComment(ctx context.Context, repo string, number int, message string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.baseURL, repo, number), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	//nolint:gosec // The GitHub API base URL is validated before the client is created.
	resp, err := g.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return false, fmt.Errorf("list comments for PR #%d: %s (%s)", number, resp.Status, strings.TrimSpace(string(body)))
	}
	var comments []struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return false, fmt.Errorf("decode comments for PR #%d: %w", number, err)
	}
	for _, comment := range comments {
		if strings.TrimSpace(comment.Body) == strings.TrimSpace(message) {
			return true, nil
		}
	}
	return false, nil
}
