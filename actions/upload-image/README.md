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
          project: PROJECT_ID
          resource: SERVICE_ID
          archive: ${{ runner.temp }}/image.oci
          tag: latest
          environment-url: https://api.example.com
```

`url` is the HTTPS origin of the platformd admin hostname (not a secret; a repository variable is fine). `project` and `resource` are the project and service IDs from the admin UI. The action builds the public upload URL and uses it as the OIDC audience.

`latest` is accepted only from the production branch configured on the service and deploys production. Any other valid image tag creates or replaces a preview when image previews are enabled on the service. If the service has an allowed-workflow list, the current workflow filename must be present in it.

The action uploads 8 MiB chunks by default. `chunk-size` may be changed, but platformd does not impose a total archive-size limit.

## GitHub deployment UI

After a successful upload the action publishes a result in this order:

1. **GitHub Deployment** when `github-token` / `GITHUB_TOKEN` can call the Deployments API (`deployments: write`). Preview and production both create a success status; `environment_url` is the preview URL or the optional `environment-url` input for `latest`. `log_url` points at the platformd admin deploy-logs page for that deployment/preview.
2. **PR comment** for non-`latest` uploads with a preview URL when the token can comment (`pull-requests: write`). The action finds an open PR for the current branch (or uses the PR from `pull_request` events), then creates or updates a comment marked per service. Missing permission or no open PR only skips the comment.
3. Otherwise, if a URL is known, it still sets the `environment-url` output so the job can wire:

   ```yaml
   environment:
     name: production
     url: ${{ steps.upload.outputs.environment-url }}
   ```

A Job Summary is always written: **Open site** (when known), **View logs in platformd** (when the upload endpoint path can be parsed), plus platformd id/digest. The same logs link is exposed as `logs-url`.

Default environment names: `production` for `latest`, `preview-<tag>` for everything else. Override with `environment`.

## Development

Node.js 24 is required. Sources are TypeScript; committed `dist/` is the
CommonJS bundle GitHub Actions executes.

```bash
npm ci
npm run check
```
