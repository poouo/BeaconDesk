package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOwner = "poouo"
	DefaultRepo  = "BeaconDesk"
)

type Options struct {
	Owner          string
	Repo           string
	CurrentVersion string
	UserAgent      string
	HTTPClient     *http.Client
}

type Result struct {
	CurrentVersion string
	LatestVersion  string
	LatestName     string
	ReleaseURL     string
	PublishedAt    time.Time
	NeedsUpdate    bool
	Comparable     bool
}

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

func Latest(ctx context.Context, opts Options) (Result, error) {
	if opts.Owner == "" {
		opts.Owner = DefaultOwner
	}
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "BeaconDesk"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", opts.Owner, opts.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Result{}, errors.New("no GitHub release found")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Result{}, err
	}
	if release.TagName == "" {
		return Result{}, errors.New("GitHub release response missing tag_name")
	}

	result := Result{
		CurrentVersion: opts.CurrentVersion,
		LatestVersion:  release.TagName,
		LatestName:     release.Name,
		ReleaseURL:     release.HTMLURL,
		PublishedAt:    release.PublishedAt,
	}
	if cmp, ok := CompareVersions(opts.CurrentVersion, release.TagName); ok {
		result.Comparable = true
		result.NeedsUpdate = cmp < 0
	}
	return result, nil
}

// CompareVersions compares two release tags. It accepts tags such as v1.2.3,
// 1.2.3, and 1.2.3-beta. Non-release values such as "dev" are not comparable.
func CompareVersions(current string, latest string) (int, bool) {
	left, ok := parseVersion(current)
	if !ok {
		return 0, false
	}
	right, ok := parseVersion(latest)
	if !ok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		switch {
		case left[i] < right[i]:
			return -1, true
		case left[i] > right[i]:
			return 1, true
		}
	}
	return 0, true
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if v == "" || v == "dev" || v == "none" || v == "unknown" {
		return out, false
	}
	if idx := strings.IndexAny(v, "+-"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, part := range parts {
		if part == "" {
			return out, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
