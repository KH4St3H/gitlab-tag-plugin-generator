// gitlab-tag-plugin-generator is an ArgoCD ApplicationSet plugin generator.
//
// It exposes the plugin generator API (POST /api/v1/getparams.execute),
// receives a list of GitLab projects and a tag name as input parameters,
// and returns one parameter set per project in which the tag exists.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	gitlab := newGitLabClient(cfg.GitLabURL, cfg.GitLabToken, cfg.RequestTimeout)
	srv := newServer(cfg, gitlab)

	log.Printf("listening on %s (gitlab: %s, concurrency: %d)", cfg.Addr, cfg.GitLabURL, cfg.Concurrency)
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

type config struct {
	Addr           string
	GitLabURL      string
	GitLabToken    string
	PluginToken    string
	Concurrency    int
	RequestTimeout time.Duration
}

func loadConfig() (config, error) {
	cfg := config{
		Addr:           ":" + envOr("PORT", "8080"),
		GitLabURL:      envOr("GITLAB_URL", "https://gitlab.com"),
		GitLabToken:    os.Getenv("GITLAB_TOKEN"),
		PluginToken:    os.Getenv("PLUGIN_TOKEN"),
		Concurrency:    10,
		RequestTimeout: 30 * time.Second,
	}
	if v := os.Getenv("CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, errInvalidConcurrency
		}
		cfg.Concurrency = n
	}
	if v := os.Getenv("REQUEST_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, errInvalidTimeout
		}
		cfg.RequestTimeout = time.Duration(n) * time.Second
	}
	if cfg.PluginToken == "" {
		return cfg, errMissingPluginToken
	}
	if cfg.GitLabToken == "" {
		return cfg, errMissingGitLabToken
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// tagChecker is implemented by the GitLab client; abstracted for tests.
type tagChecker interface {
	// TagInfo returns the tag details and true if the tag exists in the
	// project, or found=false if either the project or the tag is absent.
	TagInfo(ctx context.Context, project, tag string) (info *tagInfo, found bool, err error)
}
