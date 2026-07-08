package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type fakeGitLab struct {
	// tagged maps "project|tag" to the commit sha for tags that exist.
	tagged map[string]string
	err    error
}

func (f *fakeGitLab) TagInfo(_ context.Context, project, tag string) (*tagInfo, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	sha, ok := f.tagged[project+"|"+tag]
	if !ok {
		return nil, false, nil
	}
	info := &tagInfo{Name: tag}
	info.Commit.ID = sha
	info.Commit.ShortID = sha[:7]
	return info, true, nil
}

func testServer(gitlab tagChecker) *server {
	return newServer(config{PluginToken: "secret", Concurrency: 4}, gitlab)
}

func execute(t *testing.T, srv *server, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/getparams.execute", bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func requestBody(projects []string, tag string) map[string]any {
	return map[string]any{
		"applicationSetName": "test",
		"input": map[string]any{
			"parameters": map[string]any{"projects": projects, "tag": tag},
		},
	}
}

func TestFiltersProjectsByTag(t *testing.T) {
	srv := testServer(&fakeGitLab{tagged: map[string]string{
		"group/app1|v1.0.0": "aaaaaaaabbbbbbbb",
		"group/app3|v1.0.0": "ccccccccdddddddd",
	}})

	rec := execute(t, srv, "secret", requestBody([]string{"group/app1", "group/app2", "group/app3"}, "v1.0.0"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp serviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	params := resp.Output.Parameters
	if len(params) != 2 {
		t.Fatalf("expected 2 parameter sets, got %d: %v", len(params), params)
	}
	// Input order must be preserved.
	if params[0]["project"] != "group/app1" || params[1]["project"] != "group/app3" {
		t.Errorf("unexpected order: %v", params)
	}
	want := map[string]string{
		"project": "group/app1", "name": "app1", "tag": "v1.0.0",
		"sha": "aaaaaaaabbbbbbbb", "shortSha": "aaaaaaa",
	}
	for k, v := range want {
		if params[0][k] != v {
			t.Errorf("params[0][%q] = %q, want %q", k, params[0][k], v)
		}
	}
}

func TestNoMatchesReturnsEmptyList(t *testing.T) {
	srv := testServer(&fakeGitLab{tagged: map[string]string{}})
	rec := execute(t, srv, "secret", requestBody([]string{"group/app1"}, "v9.9.9"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); !bytes.Contains([]byte(got), []byte(`"parameters":[]`)) {
		t.Errorf("expected empty parameters list, got %s", got)
	}
}

// The matrix generator interpolates SCM provider parameters into the plugin
// input as a single string per repository; that form must work too.
func TestAcceptsSingleProjectString(t *testing.T) {
	srv := testServer(&fakeGitLab{tagged: map[string]string{
		"platform/payments|production": "deadbeefcafe1234",
	}})
	body := map[string]any{
		"applicationSetName": "matrix",
		"input": map[string]any{
			"parameters": map[string]any{"projects": "platform/payments", "tag": "production"},
		},
	}
	rec := execute(t, srv, "secret", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp serviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Output.Parameters) != 1 || resp.Output.Parameters[0]["project"] != "platform/payments" {
		t.Errorf("unexpected parameters: %v", resp.Output.Parameters)
	}
}

func TestRejectsBadToken(t *testing.T) {
	srv := testServer(&fakeGitLab{})
	for _, token := range []string{"", "wrong"} {
		rec := execute(t, srv, token, requestBody([]string{"group/app1"}, "v1.0.0"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d, want 401", token, rec.Code)
		}
	}
}

func TestRejectsMissingParameters(t *testing.T) {
	srv := testServer(&fakeGitLab{})
	cases := []map[string]any{
		{"input": map[string]any{"parameters": map[string]any{"tag": "v1"}}},
		{"input": map[string]any{"parameters": map[string]any{"projects": []string{"a/b"}}}},
		{"input": map[string]any{"parameters": map[string]any{"projects": []string{}, "tag": "v1"}}},
		{"input": map[string]any{"parameters": map[string]any{"projects": 42, "tag": "v1"}}},
	}
	for i, body := range cases {
		rec := execute(t, srv, "secret", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("case %d: status = %d, want 400 (body: %s)", i, rec.Code, rec.Body.String())
		}
	}
}

func TestGitLabErrorFailsRequest(t *testing.T) {
	srv := testServer(&fakeGitLab{err: errors.New("gitlab is down")})
	rec := execute(t, srv, "secret", requestBody([]string{"group/app1"}, "v1.0.0"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGitLabClient(t *testing.T) {
	gitlab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Project paths must arrive URL-encoded: /projects/group%2Fapp/...
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/" + url.PathEscape("group/app") + "/repository/tags/v1.0.0":
			fmt.Fprint(w, `{"name":"v1.0.0","commit":{"id":"abc123def456","short_id":"abc123d"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gitlab.Close()

	client := newGitLabClient(gitlab.URL, "glpat-test", 5*time.Second)

	info, found, err := client.TagInfo(context.Background(), "group/app", "v1.0.0")
	if err != nil || !found {
		t.Fatalf("expected tag to be found, got found=%v err=%v", found, err)
	}
	if info.Commit.ID != "abc123def456" {
		t.Errorf("sha = %q", info.Commit.ID)
	}

	_, found, err = client.TagInfo(context.Background(), "group/app", "v2.0.0")
	if err != nil || found {
		t.Errorf("expected tag to be absent, got found=%v err=%v", found, err)
	}

	badClient := newGitLabClient(gitlab.URL, "wrong-token", 5*time.Second)
	_, _, err = badClient.TagInfo(context.Background(), "group/app", "v1.0.0")
	if err == nil {
		t.Error("expected error on non-200/404 response")
	}
}
