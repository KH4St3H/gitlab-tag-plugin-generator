package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errMissingPluginToken = errors.New("PLUGIN_TOKEN environment variable is required")
	errMissingGitLabToken = errors.New("GITLAB_TOKEN environment variable is required")
	errInvalidConcurrency = errors.New("CONCURRENCY must be a positive integer")
	errInvalidTimeout     = errors.New("REQUEST_TIMEOUT_SECONDS must be a positive integer")
)

type gitLabClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newGitLabClient(baseURL, token string, timeout time.Duration) *gitLabClient {
	return &gitLabClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}
}

// tagInfo holds the subset of the GitLab tag API response we expose as
// generator parameters. https://docs.gitlab.com/ee/api/tags.html
type tagInfo struct {
	Name   string `json:"name"`
	Commit struct {
		ID      string `json:"id"`
		ShortID string `json:"short_id"`
	} `json:"commit"`
}

func (c *gitLabClient) TagInfo(ctx context.Context, project, tag string) (*tagInfo, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/repository/tags/%s",
		c.baseURL, url.PathEscape(project), url.PathEscape(tag))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("gitlab request for %q: %w", project, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var info tagInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, false, fmt.Errorf("decode gitlab response for %q: %w", project, err)
		}
		return &info, true, nil
	case http.StatusNotFound:
		// Covers both "tag not found" and "project not found" — either way
		// the project must not produce an application.
		return nil, false, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("gitlab returned %d for %q: %s", resp.StatusCode, project, strings.TrimSpace(string(body)))
	}
}
