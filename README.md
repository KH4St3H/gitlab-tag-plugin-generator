# gitlab-tag-plugin-generator

An [ArgoCD ApplicationSet plugin generator](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators-Plugin/)
that filters a list of GitLab projects by whether a given tag exists in each
repository. Only projects that have the tag produce an Application.

Typical use case: deploy only the services that have been tagged for an
environment (e.g. only projects with a `production` tag get a production app).

## How it works

```
ApplicationSet controller ──POST /api/v1/getparams.execute──▶ this service
                                                                   │
                                                     GET /projects/:id/repository/tags/:tag
                                                                   ▼
                                                                GitLab
```

For each project in the input, the service calls the GitLab tags API. Projects
where the tag exists (HTTP 200) are emitted; projects where the tag or the
project itself is missing (HTTP 404) are skipped. Any other GitLab error fails
the whole request with HTTP 500 so the controller keeps the previous state
instead of pruning apps for projects that merely failed to be checked.

### Input parameters

| Parameter  | Type                 | Description                                           |
| ---------- | -------------------- | ----------------------------------------------------- |
| `projects` | `[]string` or `string` | GitLab project paths (`group/project`) or numeric IDs |
| `tag`      | `string`             | Tag name that must exist for a project to be emitted  |

`projects` also accepts a single string so a matrix generator can interpolate
one project per invocation (see below).

### Output parameters (one set per matching project)

| Parameter  | Example             | Description                     |
| ---------- | ------------------- | ------------------------------- |
| `project`  | `platform/payments` | Full project path as given      |
| `name`     | `payments`          | Last path segment               |
| `tag`      | `production`        | Tag name as returned by GitLab  |
| `sha`      | `abc123…`           | Commit SHA the tag points to    |
| `shortSha` | `abc123d`           | Abbreviated commit SHA          |

## Combining with the SCM Provider generator (matrix)

Instead of hardcoding a project list, let the
[SCM Provider generator](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators-SCM-Provider/)
discover every project in a GitLab group and use this plugin as a per-project
tag filter inside a
[matrix generator](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators-Matrix/):

```yaml
generators:
  - matrix:
      generators:
        - scmProvider:
            gitlab:
              api: https://git.oceantim.com
              group: platform
              includeSubgroups: true
              tokenRef:
                secretName: gitlab-token
                key: token
        - plugin:
            configMapRef:
              name: gitlab-tag-plugin
            input:
              parameters:
                tag: production
                projects: "{{ .organization }}/{{ .repository }}"
```

The matrix calls the plugin once per discovered repository; repositories
where the plugin returns no parameter sets (tag absent) are dropped. See
[`examples/applicationset-matrix-scm.yaml`](examples/applicationset-matrix-scm.yaml)
for the full manifest, including which template fields each generator
contributes and the `sha` key collision to be aware of.

## Configuration (environment variables)

| Variable                  | Required | Default              | Description                                        |
| ------------------------- | -------- | -------------------- | -------------------------------------------------- |
| `PLUGIN_TOKEN`            | yes      | —                    | Bearer token the ApplicationSet controller must send |
| `GITLAB_TOKEN`            | yes      | —                    | GitLab token with `read_api`/`read_repository` scope |
| `GITLAB_URL`              | no       | `https://gitlab.com` | Base URL of your GitLab instance                   |
| `PORT`                    | no       | `8080`               | Listen port                                        |
| `CONCURRENCY`             | no       | `10`                 | Max parallel GitLab requests                       |
| `REQUEST_TIMEOUT_SECONDS` | no       | `30`                 | Per-request GitLab timeout                         |

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

## Build & deploy

```sh
docker build -t registry.example.com/gitlab-tag-plugin-generator:latest .
docker push registry.example.com/gitlab-tag-plugin-generator:latest
```

1. Edit `deploy/secret.yaml` with a random `plugin.token` and your GitLab
   token (or manage the secret with your usual secret tooling), then apply:

   ```sh
   kubectl apply -f deploy/secret.yaml
   kubectl apply -f deploy/plugin-configmap.yaml
   kubectl apply -f deploy/deployment.yaml   # set the image first
   ```

2. Reference the plugin from an ApplicationSet — see
   [`examples/applicationset.yaml`](examples/applicationset.yaml).

Notes:

- Everything lives in the `argocd` namespace; the plugin ConfigMap and the
  token secret **must** be there for the controller to find them.
- The secret needs the `app.kubernetes.io/part-of: argocd` label so the
  controller is allowed to read `plugin.token`.
- `requeueAfterSeconds` on the generator controls how often the tag check is
  re-evaluated (default 30 minutes if unset).
