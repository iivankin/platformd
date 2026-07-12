# platformd contributor guide

## Required checks

- Run `make frontend` and then `go test ./...` after changing Go code.
- Run `bun --cwd=_frontend run typecheck`, `bun --cwd=_frontend run check`, `bun --cwd=_frontend test`, and `bun --cwd=_frontend run build:web` after changing frontend code.
- Rebuild `platformd` after every Go change before running the binary.
- Run the privileged Debian integration suite for runtime, networking, systemd, storage, or update changes.

## Architecture

- This repository is one Go module and one Bun package under `_frontend/`. Do not introduce a workspace monorepo.
- The only public CLI command is `platformd init`; private daemon/bootstrap modes must stay undocumented.
- SQLite is authoritative product state. Runtime state is reconstructed and never adopted after restart.
- Keep internal packages small and expose narrow interfaces. Do not leak libpod types outside the runtime adapter.
- Use hard cutovers for schema and internal API changes. Do not add compatibility layers unless explicitly requested.
- Do not introduce a second durable queue, catalog, cache, or state machine when the specification does not require one.

## Frontend

- Use Bun, direct `Bun.build`, `tailwindcss-bun-plugin`, React, shadcn source components, and Ultracite.
- Keep the Redflow base-lyra/stone visual system: JetBrains Mono, square geometry, compact density, and flat surfaces.
- Never nest cards. Prefer section borders, split panes, rows, tabs, and tables.
- Install exact dependency versions.

## Safety

- Never commit TLS private keys, master keys, API tokens, backup credentials, or generated test secrets.
- Add focused regression tests for bugs. Do not add tautological tests.
- Explain non-obvious invariants and necessary workarounds in comments, without overcommenting straightforward code.
