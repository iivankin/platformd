# platformd image upload action

Build the final image as an OCI archive and upload it directly to platformd. No registry and no `docker login` are involved.

```yaml
permissions:
  contents: read
  id-token: write
  deployments: write
  pull-requests: write

jobs:
  deploy:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
      deployments: write
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v4
      - uses: docker/build-push-action@v7
        with:
          context: .
          platforms: linux/amd64
          outputs: type=oci,dest=${{ runner.temp }}/image.oci
      - id: upload
        uses: iivankin/platformd/actions/upload-image@v1
        with:
          url: https://admin.example.com
          project: shop
          resource: api
          archive: ${{ runner.temp }}/image.oci
          tag: latest
```

`url` is the HTTPS origin of the platformd admin hostname (not a secret; a repository variable is fine). `project` and `resource` are the project and service names from the admin UI. The action builds the public upload URL and uses it as the OIDC audience.

`latest` is accepted only from the production branch configured on the service and deploys production. Any other valid image tag creates or replaces a preview when image previews are enabled on the service (with a required root preview domain from Origin certificates; preview hostnames are `preview-{hash}.{previewDomain}`). If the service has an allowed-workflow list, the current workflow filename must be present in it.

The action uploads parallel 95 MiB parts by default (concurrency 4) so large OCI
archives saturate the link without tripping Cloudflare's 100 MiB request-body
limit. `chunk-size` must stay at or below 100 MiB; platformd enforces the same
per-part cap. There is no total archive-size limit.

When the repository has been checked out, the action also reads the subject of
`GITHUB_SHA` and sends it with the upload. platformd shows that commit title in
deployment history and details. Push and workflow-run event payloads are used as
a fallback when the local commit is unavailable.

This action requires a platformd build that accepts parallel part uploads
(`Upload-Offset` is an absolute part start, not a sequential cursor). Older
sequential-only platformd installs are unsupported.

Progress is printed in a Docker/BuildKit style (`#1 pushing … MB/s`, then
`#2 importing/deploying`).

## GitHub deployment UI

After a successful upload the action publishes a result in this order:

1. **GitHub Deployment** when `github-token` / `GITHUB_TOKEN` can call the Deployments API (`deployments: write`). Preview and production both create a success status; `environment_url` comes from platformd (`url`: first attached service domain for `latest`, preview hostname otherwise). `log_url` points at the platformd admin deploy-logs page for that deployment/preview.
2. **PR comment** for non-`latest` uploads with a preview URL when the token can comment (`pull-requests: write`). The action finds an open PR for the current branch (or uses the PR from `pull_request` events), then creates or updates a comment marked per service. Missing permission or no open PR only skips the comment.
3. Otherwise, if a URL is known, it still sets the `environment-url` output so the job can wire:

   ```yaml
   environment:
     name: api
     url: ${{ steps.upload.outputs.environment-url }}
   ```

A Job Summary is always written to `GITHUB_STEP_SUMMARY`: **Open site** / **View logs in platformd** (when known), plus a table with project, resource, tag, GitHub environment, URL, platformd id, and digest. The same logs link is exposed as `logs-url`.

Default environment names: `<resource>` for `latest`, `<resource>/preview-<tag>` for everything else. Override with `environment`.

## Development

Node.js 24 is required. Sources are TypeScript; committed `dist/` is the
ESM bundle GitHub Actions executes.

```bash
npm ci
npm run check
```
