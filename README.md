# vigiaDev

`vigiaDev` starts local development environments in dependency order, checks
real readiness, records resource ownership, and shuts down only what it owns.

## Quick start

```bash
uv sync --extra dev
uv run vigiadev init
uv run vigiadev validate
uv run vigiadev up
```

Use `vigiadev up --no-tui` in CI or a terminal without Textual support. The
configuration lookup order is `--config`, `vigiadev.local.yaml`,
`vigiadev.yaml`, then legacy `dev.yaml`.

## Safety model

- `up` never invokes `docker compose down`.
- Healthy listeners are reused by default.
- Unknown listeners are never terminated unless `--kill-ports` is explicit
  and the service policy is `kill`.
- `.vigiadev/run.json` and `.vigiadev/vigiadev.lock` record and protect each
  owned session.
- `down` targets only process groups and the Compose project recorded by the
  session manifest.

See [`vigiadev.example.yaml`](vigiadev.example.yaml) for the complete MVP
configuration shape.

