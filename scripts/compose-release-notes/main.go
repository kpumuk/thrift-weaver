// Package main composes the final GitHub release body from release-please notes and repo policy snippets.
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
	"strings"
	"time"
)

type config struct {
	Repo       string
	Commit     string
	LinuxNote  string
	PerfReport string
	Output     string
	TokenEnv   string
	GitHubAPI  string
}

type githubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type pullRequest struct {
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	Body     string     `json:"body"`
	MergedAt *time.Time `json:"merged_at"`
}

type perfReport struct {
	GoVersion   string        `json:"go_version"`
	GOOS        string        `json:"goos"`
	GOARCH      string        `json:"goarch"`
	CPUs        int           `json:"cpus"`
	ParseBench  []benchReport `json:"parse_bench"`
	FormatBench []benchReport `json:"format_bench"`
	Memory      memoryReport  `json:"memory"`
}

type benchReport struct {
	Set   string      `json:"set"`
	Stats sampleStats `json:"stats"`
}

type sampleStats struct {
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
}

type memoryReport struct {
	HeapAllocGrowth     int64 `json:"heap_alloc_growth"`
	HeapInuseGrowth     int64 `json:"heap_inuse_growth"`
	UnboundedGrowthHint bool  `json:"unbounded_growth_hint"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "compose-release-notes: %v\n", err)
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
	baseNotes, err := client.fetchReleasePRBody(ctx, cfg.Repo, cfg.Commit)
	if err != nil {
		return err
	}
	linuxDoc, err := os.ReadFile(cfg.LinuxNote)
	if err != nil {
		return fmt.Errorf("read linux note: %w", err)
	}
	perfJSON, err := os.ReadFile(cfg.PerfReport)
	if err != nil {
		return fmt.Errorf("read perf report: %w", err)
	}
	out, err := composeReleaseNotes(baseNotes, string(linuxDoc), perfJSON)
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.Output, []byte(out), 0o600)
}

func parseFlags() (config, error) {
	var cfg config
	flag.StringVar(&cfg.Repo, "repo", "", "GitHub repo in owner/name form")
	flag.StringVar(&cfg.Commit, "commit", "", "Tagged release commit SHA")
	flag.StringVar(&cfg.LinuxNote, "linux-note", "", "Path to linux compatibility policy doc")
	flag.StringVar(&cfg.PerfReport, "perf-report", "", "Path to perf report JSON")
	flag.StringVar(&cfg.Output, "output", "", "Output markdown file")
	flag.StringVar(&cfg.TokenEnv, "token-env", "GITHUB_TOKEN", "Environment variable containing GitHub token")
	flag.StringVar(&cfg.GitHubAPI, "github-api", "https://api.github.com", "GitHub API base URL")
	flag.Parse()
	if cfg.Repo == "" || cfg.Commit == "" || cfg.LinuxNote == "" || cfg.PerfReport == "" || cfg.Output == "" {
		return config{}, errors.New("required flags: --repo, --commit, --linux-note, --perf-report, --output")
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

func composeReleaseNotes(baseNotes, linuxDoc string, perfJSON []byte) (string, error) {
	snippet, err := extractLinuxSnippet(linuxDoc)
	if err != nil {
		return "", err
	}
	perfSummary, err := renderPerformanceSummary(perfJSON)
	if err != nil {
		return "", err
	}

	parts := []string{
		"## Linux compatibility",
		snippet,
		"## Performance",
		perfSummary,
		strings.TrimSpace(baseNotes),
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")) + "\n", nil
}

func extractLinuxSnippet(doc string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Suggested release-note snippet:" {
			continue
		}
		var snippet []string
		for _, candidate := range lines[i+1:] {
			candidate = strings.TrimRight(candidate, " ")
			if strings.HasPrefix(candidate, "> ") {
				snippet = append(snippet, candidate)
				continue
			}
			if len(snippet) > 0 && strings.TrimSpace(candidate) == "" {
				break
			}
		}
		if len(snippet) == 0 {
			break
		}
		return strings.Join(snippet, "\n"), nil
	}
	return "", errors.New("linux release-note snippet not found")
}

func renderPerformanceSummary(src []byte) (string, error) {
	var report perfReport
	if err := json.Unmarshal(src, &report); err != nil {
		return "", fmt.Errorf("parse perf report: %w", err)
	}
	parseTypical, ok := findBench(report.ParseBench, "typical")
	if !ok {
		return "", errors.New("missing parse typical benchmark")
	}
	formatTypical, ok := findBench(report.FormatBench, "typical")
	if !ok {
		return "", errors.New("missing format typical benchmark")
	}

	growthHint := "no"
	if report.Memory.UnboundedGrowthHint {
		growthHint = "yes"
	}

	lines := []string{
		fmt.Sprintf("- Baseline: %s/%s, Go %s, CPUs %d", report.GOOS, report.GOARCH, report.GoVersion, report.CPUs),
		fmt.Sprintf("- Parse + diagnostics (typical): p50 %.2f ms, p95 %.2f ms", parseTypical.Stats.P50MS, parseTypical.Stats.P95MS),
		fmt.Sprintf("- Full document format (typical): p50 %.2f ms, p95 %.2f ms", formatTypical.Stats.P50MS, formatTypical.Stats.P95MS),
		fmt.Sprintf("- LSP memory loop: heap_alloc delta %d bytes, heap_inuse delta %d bytes, unbounded growth hint: %s", report.Memory.HeapAllocGrowth, report.Memory.HeapInuseGrowth, growthHint),
	}
	return strings.Join(lines, "\n"), nil
}

func findBench(reports []benchReport, set string) (benchReport, bool) {
	for _, report := range reports {
		if report.Set == set {
			return report, true
		}
	}
	return benchReport{}, false
}

func (g githubClient) fetchReleasePRBody(ctx context.Context, repo, commit string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/pulls", g.baseURL, repo, commit), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	//nolint:gosec // The GitHub API base URL is validated before the client is created.
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", fmt.Errorf("list associated pull requests: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}
	var pulls []pullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pulls); err != nil {
		return "", fmt.Errorf("decode associated pull requests: %w", err)
	}
	for _, pr := range pulls {
		if pr.MergedAt == nil {
			continue
		}
		if strings.HasPrefix(pr.Title, "chore: release ") {
			return strings.TrimSpace(pr.Body), nil
		}
	}
	return "", errors.New("release pull request not found for tagged commit")
}
