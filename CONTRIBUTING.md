# Contributing to specd

Thanks for improving `specd`.

## Repository model

- Public mirror: <https://github.com/chatinfra/specd.git>
- Canonical source: `go/specd/` inside the ChatInfra monorepo

The public mirror exists for inspection, forks, local host edits, and pull requests. It is downstream of the monorepo. Maintainers import accepted public PRs into canonical `go/specd` first, then synchronize the mirror.

Canonical `go/specd` keeps the monorepo module path used by every module under `go/` (the `super/go/<tool>` form). The mirror sync rewrites published `*.go`, `go.mod`, and `*.md` module-path references so the public repository declares `module github.com/chatinfra/specd` and public-facing examples use the mirror module path, for example:

```sh
go install github.com/chatinfra/specd/cmd/specd@latest
```

Maintainers apply the inverse transform when importing an accepted public PR, so canonical stays on the monorepo module path. Files outside `*.go`, `go.mod`, and `*.md` are published byte-for-byte without transform.

Because Markdown is transformed too, a published `*.md` file never mentions the canonical monorepo module path. Write module documentation so every sentence carrying a module path stays true after the rewrite — the mechanical half is enforced by the mirror tooling tests, but a sentence that transforms into a false claim is caught only in review.

## Fork-and-PR flow

```sh
git clone git@github.com:<you>/specd.git
cd specd
git checkout -b my-specd-change
go test ./...
git push -u origin my-specd-change
```

Then open a pull request against `chatinfra/specd`. Include any API, configuration, or trust-boundary implications in the PR description.

`specd` command results are YAML and `--json` is unsupported, so a change that adds or alters structured output must also update the matching schema under `spec/outputs/` and keep `specd schemas` consistent with the files that actually exist. A change to the HTTP surface must also update `openapi/openapi-specd.yaml`, which is the wire-contract source of truth.

## Host-local edits

OpenCode hosts keep one editable mirror checkout at `/data/opencode/src/specd`, built and executed through the `/data/opencode/bin/specd` launcher. Use this for diagnostics or emergency local patches:

```sh
sudo -u opencode git -C /data/opencode/src/specd status
sudo -u opencode /data/opencode/bin/specd --help
sudo -u opencode /data/opencode/bin/specd validate-config
```

Reconfigure preserves dirty host checkouts and logs a warning instead of resetting local work. To return to mirror updates, commit/stash/revert local edits, then re-run reconfigure so the clean checkout can fast-forward. Restart any `specd serve` process after editing source so the launcher rebuilds and the new binary is picked up.

## Maintainer import and mirror sync

Maintainers import accepted public changes into canonical `go/specd`, preserving the monorepo as source of truth. For reviewed public mirror commits, generate an `mbox` patch and apply it with the monorepo helper so patch hunks are reverse-transformed back to the canonical module path:

```sh
git -C /path/to/chatinfra-specd-mirror format-patch -1 --stdout <accepted-commit> > /path/to/pr.patch
bin/import_sched_public_pr --tool specd /path/to/pr.patch /path/to/monorepo/go/specd
```

`bin/import_sched_public_pr` lives in the monorepo next to the mirror sync tooling. It rewrites patch hunk content for `*.go`, `go.mod`, and `*.md`, refuses binary or non-allowlisted path-bearing patches, and then runs `git am` in the target canonical worktree. The same helper serves the companion CLI mirrors with `--tool sched`, `--tool jmap`, `--tool xmpp`, or `--tool voice`.

For a one-off text-only patch that touches only `*.go`, `go.mod`, and `*.md`, the equivalent `git format-patch | sed | git am` flow is:

```sh
canonical_module='super/go'/'specd'
public_module_regex='github[.]com/chatinfra'/'specd'
git -C /path/to/chatinfra-specd-mirror format-patch -1 --stdout <accepted-commit> \
  | sed -E "/^[ +-]/ s#${public_module_regex}#${canonical_module}#g" \
  | git -C /path/to/monorepo/go/specd am -
```

Prefer the helper for normal imports; it validates patch shape before applying. After the monorepo change lands, run the mirror sync tooling from the monorepo root to update the public repository:

```sh
bin/sync_go_github --tool specd
```

The sync treats `./go/specd` as canonical source when run from the monorepo root, clones or reuses the public mirror checkout under `$SUPER_TMP_DIR/specd-public-mirror-checkout` or `./tmp/specd-public-mirror-checkout` via the SSH remote `git@github.com:chatinfra/specd.git`, refuses dirty canonical or mirror state, requires mirror `HEAD` to match its fetched upstream exactly, copies only this module's subtree into the public mirror checkout, commits generated changes, and pushes the mirror branch. Use `--dry-run` to run the same preflight checks and prepare the transformed staging tree without touching the mirror checkout.

Verify a published result by cloning the public mirror over `https://` and running `go build -trimpath ./cmd/specd` — it must build standalone, since OpenCode host launchers clone and build the mirror with no monorepo context.
