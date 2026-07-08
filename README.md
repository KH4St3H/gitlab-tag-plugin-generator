# gitlab-tag-plugin-generator

An [ArgoCD ApplicationSet plugin generator](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators-Plugin/)
that filters a list of GitLab projects by whether a given tag exists in each
repository. Only projects that have the tag produce an Application.

## Use cases
The most common use case for this plugin is when you are using SCM generator to generate applications from a Gitlab repositories and you want to pin the resources to an specific tag in that repository(see [Tag Tracking](https://argo-cd.readthedocs.io/en/latest/user-guide/tracking_strategies/#tag-tracking)). The problem arises when you create a new project and the you create the infra project of you application earlier and don't wnat to deploy the project yet. This plugin allows you to filter repositories by that specific tag so their corresponding project won't get created unless that tag exist.

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


## Deploy

Prebuilt images are published to
[`ghcr.io/kh4st3h/gitlab-tag-plugin-generator`](https://github.com/KH4St3H/gitlab-tag-plugin-generator/pkgs/container/gitlab-tag-plugin-generator)
by GitHub Actions on every push to `main` and on `v*` tags.

1. Create the token secret — a random `plugin.token` and your GitLab token
   (see [`deploy/secret.yaml`](deploy/secret.yaml) for details, or manage it
   with your usual secret tooling):

   ```sh
   kubectl -n argocd create secret generic gitlab-tag-plugin-secret \
     --from-literal=plugin.token="$(openssl rand -hex 32)" \
     --from-literal=gitlab.token="<your-gitlab-token>"
   kubectl -n argocd label secret gitlab-tag-plugin-secret app.kubernetes.io/part-of=argocd
   ```

2. Apply the manifests straight from GitHub:

   ```sh
   kubectl apply -k "https://github.com/KH4St3H/gitlab-tag-plugin-generator//deploy?ref=main"
   ```

   (or clone the repo and `kubectl apply -k deploy/`)

3. Reference the plugin from an ApplicationSet — see
   [`examples/applicationset.yaml`](examples/applicationset.yaml).

To build the image yourself instead:

```sh
docker build -t registry.example.com/gitlab-tag-plugin-generator:latest .
```

Notes:

- Everything lives in the `argocd` namespace; the plugin ConfigMap and the
  token secret **must** be there for the controller to find them.
- The secret needs the `app.kubernetes.io/part-of: argocd` label so the
  controller is allowed to read `plugin.token`.
- `requeueAfterSeconds` on the generator controls how often the tag check is
  re-evaluated (default 30 minutes if unset).
