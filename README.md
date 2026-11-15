# ChatInfra specd

This module contains the `specd` command: the trusted OpenSpec control-plane daemon used by remote clients and remote `superdir ui` sessions.

`specd` owns OpenSpec repository reads, editable artifact updates, deterministic lifecycle operations, canonical spec browsing, durable jobs, prompt handling, and agent-backed `/opsx-*` workflows. It is a single-token trusted HTTP API meant for private networks, SSH tunnels, or a trusted reverse proxy; it implements no users, tenancy, RBAC, or scoped tokens, and it is not a free-form shell daemon.

## Public mirror

The public repository is <https://github.com/chatinfra/specd.git>. Its root is a mirror of this monorepo's canonical `go/specd/` subtree, so public checkouts contain `go.mod`, `go.sum`, `cmd/specd`, `internal`, tests, the `spec/outputs/` schemas, the `openapi/` contract, and these docs directly at repository root.

`go/specd` in the ChatInfra monorepo remains canonical. Maintainers import accepted public changes back into the monorepo first, then update the public mirror from the canonical subtree. The mirror sync rewrites published Go and Markdown module-path references so mirror checkouts use the public module path in examples such as `github.com/chatinfra/specd/cmd/specd`.

## Build and test

```sh
go test ./...
go build ./cmd/specd
```

The module declares Go 1.24 in `go.mod` and pins its dependencies in `go.sum`, so a fresh mirror clone builds with no monorepo context. From a published mirror checkout, module-path installation uses the same public path shown after sync:

```sh
go install github.com/chatinfra/specd/cmd/specd@latest
```

## `specd` CLI output

`specd`, `specd help`, `specd --help`, and command help such as `specd help serve` print deterministic terminal text. That help is not a structured contract and should not be parsed by automation.

Successful command results are YAML documents on stdout. Failures are coded YAML error envelopes on stderr carrying `error.code`, `error.message`, and optional `error.details`. `--json` is unsupported and is rejected with the `unsupported_flag` code.

Run `specd schemas` to list the supported structured YAML schema IDs and their paths under `spec/outputs/`:

| Schema ID | Path |
| --- | --- |
| `schemas` | `spec/outputs/schemas.schema.yaml` |
| `error` | `spec/outputs/error.schema.yaml` |
| `validate-config` | `spec/outputs/validate-config.schema.yaml` |
| `serve` | `spec/outputs/serve.schema.yaml` |

The HTTP wire contract served by the daemon is `openapi/openapi-specd.yaml`, published at mirror root. Treat that file as the source of truth for endpoints and payloads; this README covers the command surface only.

## Commands

| Command | Purpose |
| --- | --- |
| `specd serve` | Emit a `serve` / `starting` status document, then run the HTTP API until `SIGINT` or `SIGTERM` |
| `specd validate-config` | Validate the resolved configuration and emit a `validate-config` / `ok` document without binding a port |
| `specd schemas` | Print structured-output schema discovery |

OpenSpec archive and sync jobs refuse a change whose root contains `canon-ops/`: the job fails with error code `canon_ops_refused` before the OpenSpec command runs, because those canonical text operations apply only through the repository's own publisher archive.

## Configuration

Global flags carry matching environment defaults:

| Flag | Environment | Purpose |
| --- | --- | --- |
| `--repo-root` | `SPECD_REPO_ROOT` | OpenSpec repository root |
| `--openspec-bin` | `SPECD_OPENSPEC_BIN` | OpenSpec CLI binary (default `openspec`) |
| `--bearer-token` | `SPECD_BEARER_TOKEN` | Opaque bearer token required by every endpoint |
| `--host` | `SPECD_HOST` | Bind host (default `127.0.0.1`) |
| `--port` | `SPECD_PORT` | Bind port (default `2781`) |
| `--job-state-dir` | `SPECD_JOB_STATE_DIR` | Durable job-state directory |
| `--enable-agent-workflows` | `SPECD_ENABLE_AGENT_WORKFLOWS` | Enable agent-backed `/opsx-*` workflow endpoints |
| `--super-root` | `SPECD_SUPER_ROOT` | Superdir worktree root (default `/data/super`) |
| `--command-timeout` | `SPECD_COMMAND_TIMEOUT` | OpenSpec CLI command timeout (default `60s`) |

```sh
specd serve \
  --repo-root /path/to/repo \
  --bearer-token "$SPECD_BEARER_TOKEN" \
  --host 127.0.0.1 \
  --port 2781 \
  --job-state-dir /path/to/repo/tmp/specd-jobs
```

Every endpoint requires the configured bearer token, including health and SSE endpoints. Startup validates that the repository root exists, carries an OpenSpec project layout, and is writable for mutating operations, that the OpenSpec CLI resolves, and that the job-state directory is ready. Set `--enable-agent-workflows` only when the server should accept remote agent-backed `/opsx-*` jobs; deterministic OpenSpec reads and bounded operations remain available without it.

## OpenCode host layout

Source-backed OpenCode hosts use these stable paths:

| Path | Purpose |
| ---- | ------- |
| `/data/opencode/src/specd` | Editable source checkout cloned from the public mirror |
| `/data/opencode/bin/specd` | Stable launcher used by ChatInfra operations and OpenCode runtime shells |
| `/data/opencode/.cache/specd` | Cached build output and source hash |
| `/data/opencode/.cache/go-build` and `/data/opencode/.cache/go-mod` | OpenCode-owned Go build and module caches |

The launcher rebuilds `./cmd/specd` when the source hash changes or the cached binary is missing, then execs the cached binary with the original arguments. ChatInfra-controlled operations run the launcher as the `opencode` user so editable source is not executed as root.

For local host edits:

```sh
sudo -u opencode git -C /data/opencode/src/specd status
sudo -u opencode editor /data/opencode/src/specd/internal/...
sudo -u opencode /data/opencode/bin/specd --help
```

Installer and reconfigure flows preserve dirty `/data/opencode/src/specd` checkouts and log a warning instead of resetting local work. Clean checkouts are fetched and fast-forwarded from the configured mirror.

## Contribution workflow

1. Fork <https://github.com/chatinfra/specd.git>.
2. Clone your fork and create a topic branch.
3. Make changes, run `go test ./...`, and push the branch.
4. Open a pull request against the public mirror.

Accepted public changes are reviewed and imported into canonical `go/specd` in the ChatInfra monorepo before the public mirror is synchronized again. See [CONTRIBUTING.md](./CONTRIBUTING.md) for details.
