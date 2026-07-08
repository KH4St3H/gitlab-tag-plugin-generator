## How it works

For each project in the input, the service calls the GitLab tags API. Projects
where the tag exists (HTTP 200) are emitted; projects where the tag or the
project itself is missing (HTTP 404) are skipped. Any other GitLab error fails
the whole request with HTTP 500 so the controller keeps the previous state
instead of pruning apps for projects that merely failed to be checked.


## Local development

```sh
export PLUGIN_TOKEN=dev-token GITLAB_TOKEN=glpat-...
go run .

curl -s http://localhost:8080/api/v1/getparams.execute \
  -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -d '{
        "applicationSetName": "test",
        "input": {"parameters": {
          "tag": "v1.0.0",
          "projects": ["gitlab-org/gitlab-runner", "gitlab-org/does-not-exist"]
        }}
      }' | jq
```

Run tests with `go test ./...`.
