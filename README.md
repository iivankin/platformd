# platformd

`platformd` is a lightweight, single-VPS application platform distributed as one self-contained file. It embeds its control plane, HTTPS ingress, React admin UI, OCI Registry, private S3-compatible object storage, and private container runtime bundle.

## Repository shape

- `main.go` — public `init` dispatch and private daemon entrypoint.
- `internal/` — small Go packages for product domains and infrastructure adapters.
- `_frontend/` — the single React/shadcn Bun package. The leading underscore keeps Go package discovery out of JavaScript dependencies without introducing a workspace.
- `internal/ui/dist/` — generated frontend assets embedded into the Go binary.
- `docs/implementation-checklist.md` — requirement-to-evidence tracking for release acceptance.

This is intentionally one Go module and one Bun package, not a workspace monorepo.

## Development

```bash
bun --cwd=_frontend install --frozen-lockfile
make check
make test
make build
```

After changing Go code, rebuild before running `dist/platformd`; an old binary is not valid test evidence.

## Frontend checks

```bash
bun --cwd=_frontend run typecheck
bun --cwd=_frontend run check
bun --cwd=_frontend test
bun --cwd=_frontend run build:web
```

## Security

Never commit installation secrets or test credentials. Cloudflare Origin keys, master keys, API tokens, and backup credentials belong in root-only local files or secret input channels.
