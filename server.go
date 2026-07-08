package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

// serviceRequest is the payload the ApplicationSet controller sends to a
// plugin generator.
type serviceRequest struct {
	ApplicationSetName string `json:"applicationSetName"`
	Input              struct {
		Parameters map[string]json.RawMessage `json:"parameters"`
	} `json:"input"`
}

// serviceResponse is the payload the controller expects back. Each entry in
// Parameters becomes one generated application.
type serviceResponse struct {
	Output struct {
		Parameters []map[string]string `json:"parameters"`
	} `json:"output"`
}

type server struct {
	cfg    config
	gitlab tagChecker
	mux    *http.ServeMux
}

func newServer(cfg config, gitlab tagChecker) *server {
	s := &server{cfg: cfg, gitlab: gitlab, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/v1/getparams.execute", s.handleGetParams)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) handleGetParams(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	projects, tag, err := parseInput(req.Input.Parameters)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	params, err := s.filterByTag(r, projects, tag)
	if err != nil {
		// Fail the whole request on GitLab errors rather than returning a
		// partial list: a partial result would make the ApplicationSet
		// controller prune applications for projects that merely failed to
		// be checked.
		log.Printf("appset=%s tag=%s error: %v", req.ApplicationSetName, tag, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("appset=%s tag=%s matched %d/%d projects", req.ApplicationSetName, tag, len(params), len(projects))

	var resp serviceResponse
	resp.Output.Parameters = params
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("write response: %v", err)
	}
}

func (s *server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.PluginToken)) == 1
}

// parseInput extracts and validates the generator input parameters:
//
//	projects: GitLab project paths (e.g. "group/app") or numeric IDs; either
//	          a list of strings or a single string (the single-string form is
//	          what a matrix generator produces when interpolating parameters
//	          from an SCM provider generator, e.g. "{{.organization}}/{{.repository}}")
//	tag:      the tag name that must exist for a project to be emitted
func parseInput(raw map[string]json.RawMessage) (projects []string, tag string, err error) {
	if v, ok := raw["tag"]; ok {
		if err := json.Unmarshal(v, &tag); err != nil {
			return nil, "", fmt.Errorf("parameter %q must be a string", "tag")
		}
	}
	if tag == "" {
		return nil, "", fmt.Errorf("parameter %q is required", "tag")
	}

	if v, ok := raw["projects"]; ok {
		if err := json.Unmarshal(v, &projects); err != nil {
			var single string
			if err := json.Unmarshal(v, &single); err != nil {
				return nil, "", fmt.Errorf("parameter %q must be a string or a list of strings", "projects")
			}
			projects = []string{single}
		}
	}
	if len(projects) == 0 {
		return nil, "", fmt.Errorf("parameter %q is required and must not be empty", "projects")
	}
	for _, p := range projects {
		if strings.TrimSpace(p) == "" {
			return nil, "", fmt.Errorf("parameter %q contains an empty entry", "projects")
		}
	}
	return projects, tag, nil
}

// filterByTag checks every project concurrently and returns, in input order,
// one parameter set per project that has the tag.
func (s *server) filterByTag(r *http.Request, projects []string, tag string) ([]map[string]string, error) {
	type result struct {
		params map[string]string
		err    error
	}
	results := make([]result, len(projects))

	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup
	for i, project := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			info, found, err := s.gitlab.TagInfo(r.Context(), project, tag)
			if err != nil {
				results[i] = result{err: err}
				return
			}
			if !found {
				return
			}
			results[i] = result{params: map[string]string{
				"project":  project,
				"name":     appName(project),
				"tag":      info.Name,
				"sha":      info.Commit.ID,
				"shortSha": info.Commit.ShortID,
			}}
		}()
	}
	wg.Wait()

	params := make([]map[string]string, 0, len(projects))
	for _, res := range results {
		if res.err != nil {
			return nil, res.err
		}
		if res.params != nil {
			params = append(params, res.params)
		}
	}
	return params, nil
}

// appName derives a short name from a project path: "group/sub/app" -> "app".
func appName(project string) string {
	if i := strings.LastIndex(project, "/"); i >= 0 {
		return project[i+1:]
	}
	return project
}
