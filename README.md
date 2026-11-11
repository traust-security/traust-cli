# traust-cli

[![ci](https://github.com/traust-security/traust-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/traust-security/traust-cli/actions/workflows/ci.yml)

A single Go binary that is the front door to the
[traust](https://github.com/traust-security/traust) security workflow: it sets
up the environment, drives harness skills headlessly through the agent of
your choice, validates the output, and shows you the results.

```bash
traust-cli init                          # uv + traust checkout + config home, one command
traust-cli skills                        # browse the catalog (TUI)
traust-cli run threat-model /src/myapp   # first audit, through YOUR agent harness
traust-cli report myapp-threat-model.md  # validate, lint, browse
```

The CLI is deliberately a thin, honest layer. Every deterministic operation
(`reporting validate`, `registry models resolve`, `toolchain setup`, …) is
delegated to the traust engine CLI from the managed venv — the CLI adds the
user experience, not a second engine.

## Why

Running traust today means cloning the harness repo, `uv sync`, creating
`TRAUST_CONFIG_HOME` from templates, optionally installing a scanner
toolchain, then driving skills by hand from inside an agent. traust-cli wraps
all of that, and adds the pieces the harness documents as gaps:

- **Model-floor pre-flight** — a run's model defaults to the registry's floor
  resolution for the skill's role (`traust registry models resolve`);
  explicit overrides are warned about and journaled
  (traust/docs/model-routing.md documents the missing runtime enforcement).
- **Budget bands** — the shipped `budget-policy.yaml` per-run output-token
  caps and warn/degrade/refuse bands, enforced live (warn 1.5×, degrade 2×,
  refuse 3× by default; `--budget-override "<reason>"` records an exception).
- **S9 isolation, centralized** — agents always run from the harness root,
  never inside the audited checkout, and never load target-repo agent
  configuration. Enforcing this in one launcher is stronger than hoping every
  script does it right.
- **Spend declaration** — every run with reported usage appends a
  `registry models spend` row automatically.
- **Run journals** — spec, raw stream, normalized events and result under
  `~/.traust/cli/runs/<id>/`.
- **Corpus registration** — a run that writes artifacts under an
  unregistered findings tree registers it in `corpus-config.yaml` with
  safe defaults (the harness's own workflow only closes that loop
  interactively, via `/corpus-intake`).

## Harness support

Priority order: **claude code → codex → pi → omp → opencode → crush → goose**.
Antigravity (agy) is deferred: it has no headless execution path.

| Harness | Headless invocation | Live stream | Usage | Permission posture |
|---|---|---|---|---|
| claude | `claude -p --output-format stream-json` | ✓ | ✓ | acceptEdits + the skill's `allowed-tools` (fail-closed); `--unsafe-full-permission` for the rest |
| codex | `codex exec - --json --output-last-message` | ✓ | ✓ | `workspace-write` sandbox, approvals never |
| pi | `pi --mode json` | ✓ | ✓ | tool grants mapped onto pi's builtin tools |
| omp | `omp --mode json -p` | ✓ | ✓ | tool grants mapped onto omp's builtin tools incl. the task/wait subagent facility; `--unsafe-full-permission` auto-approves the coding + subagent toolset; extension/skill/rule discovery, LSP and prewalk model-switching disabled |
| opencode | `opencode run --format json --auto` | ✓ | ✓ | `--auto` |
| crush | `crush server` (client mode): workspace + SSE events, clean cancel, reconnect on dropped streams (runs survive connection loss); falls back to `crush run` | ✓ live | ✓ | workspace permission level; unanswered permission/question requests fail closed (exit 2) |
| goose | `goose run -i - --no-session` (prompt on stdin) | — | — | goose's own configuration; its config is treated as authoritative — `OPENAI_API_KEY`/`OPENAI_HOST` from `~/.config/goose/config.yaml` override ambient values (goose reads the key from the environment only) |

Every adapter prints its honest caveats before a run. `--unsafe-full-permission`
is opt-in and journaled.

## Install

```bash
go install github.com/traust-security/traust-cli@latest
```

or grab a static binary from the [releases](https://github.com/traust-security/traust-cli/releases)
(linux/macOS/windows, amd64/arm64, checksummed). The only other thing you
need is an agent harness you are logged into.

Konflux also publishes the same binary as a signed OCI artifact (linux/amd64,
with an SBOM, provenance and a signature) for air-gapped and platform use:

```bash
podman run --rm quay.io/redhat-user-workloads/source-code-intelligence-tenant/traust/traust-cli:latest --version
```

See [docs/konflux.md](docs/konflux.md) for digest pinning, extracting the
binary out of the image, and the build and version contract.

A second, **self-contained, air-gap-ready image** (`Containerfile.toolkit-plus-feeds`,
published as `traust-cli-toolkit-plus-feeds`) bakes every runtime dependency
`init` bootstraps — the uv-managed venv, the pinned traust checkout,
`TRAUST_CONFIG_HOME`, the `secure-code-audit` toolchain, an agent harness — plus
the offline vulnerability feeds (grype DB and the osv-scanner offline databases),
so `traust-cli run` works with only harness credentials injected at runtime, no
`~/.traust` mount and no external vuln lookups:

```bash
podman run --rm -e ANTHROPIC_API_KEY=... -v "$PWD/target:/work:ro,Z" \
  quay.io/redhat-user-workloads/source-code-intelligence-tenant/traust/traust-cli-toolkit-plus-feeds:latest \
  run secure-code-audit /work
```

## Commands

| Command | What it does |
|---|---|
| `init` | Bootstrap the managed workspace: pinned uv (checksum-verified, never `curl \| sh`), traust checkout at a stable tag, `uv sync`, `TRAUST_CONFIG_HOME` from templates via `install_traust`, and the `secure-code-audit` scanner toolchain (covers every flow stage; `--toolchain none` skips, already-installed profiles are skipped but re-checked — a stale or missing tool warns instead of failing silently). Idempotent; nothing is written to your shell profile (use `eval "$(traust-cli env)"` if you want it). |
| `doctor` | CLI checks + `install_traust --doctor`; `--json` for CI. |
| `update` | Move the pin to a newer tag and re-sync. |
| `skills` | Catalog parsed from the same SKILL.md frontmatter the harness reads; TUI picker on a TTY, table otherwise; `--json`. |
| `run [flags] <skill> [args…]` | Compose the SKILL.md into a prompt, pre-flight the model floor, and launch the run **in the background** (a detached worker process that survives the terminal), then attach. Everything after the skill name passes through to the skill verbatim (`run triage report.json --fp-rules org.txt --auto`). `-d/--detach` starts without attaching; `--json`/`--no-monitor` keep synchronous CI semantics. The journal records everything; artifacts are validated and spend declared. |
| `attach <run>` | Live monitor over a background run: full transcript replay + live tail with thinking, tool calls and tool results. **Ctrl-C detaches without cancelling** (the run keeps going); `x` twice cancels. Works on finished runs too (full replay + result). Non-TTY degrades to a plain tail. |
| `runs` (`ls`/`ps`) | List runs — running first, then newest — with state (running/done/failed/needs-input/budget-stop/aborted/orphaned), age and tokens; `--json`. |
| `stop <run>` | Graceful cancel: SIGINT (context-driven teardown: crush cancel endpoint, server shutdown) → SIGTERM → SIGKILL. |
| `peek <run-or-flow>` | One-shot snapshot: state, usage and the last dozen journal entries — for runs and flows alike (a flow peek shows the stage chain plus the current stage's snapshot). |
| `logs <run>` | The run's rendered journal transcript. |
| `result <run>` | The stored result: summary, artifacts, usage, error — exits with the run's own code. |
| `report <file-or-dir>` | Validate (`reporting validate`), lint threat models (`reporting lint`), render (`reporting render`), and browse findings (severity-colored TUI). |
| `ledger whoami\|findings\|layers\|verify` | Read-only views over a ledger service (`--url` or `TRAUST_LEDGER_URL`; the token comes from the env var named by `--token-env`). No submissions, no countersign. |

### Flows (multi-stage pipelines)

```bash
traust-cli flow audit-triage <repo>              # audit, then triage (--auto); output = triaged findings
traust-cli flow remediate-verify <triage.json> <repo>   # patch the findings, then verify the patched code
traust-cli flow full <repo>                      # the whole loop end to end
traust-cli flow status [id]                      # list flows / show stage detail
```

Every stage is an ordinary background run (own journal, result, attach
session); each stage's inputs are derived from the previous stage's
artifacts — the audit report feeds triage, the triaged findings feed patch,
and verify runs against a fresh checkout with the generated patches
applied. Ctrl-C detaches without cancelling; `flow status` shows progress
and `attach <id>` follows the current stage.

### Exit codes (a contract)

| Code | Meaning |
|---|---|
| 0 | run completed |
| 1 | error (harness failure, failed artifact validation, aborted) |
| 2 | the skill needs interactive input a headless run cannot provide |
| 3 | the budget refuse band was hit |

## Security notes

- The audit target is always data, never configuration (harness rule S9):
  the agent's cwd is the harness root; target-repo `.claude/`/`AGENTS.md`/
  hooks are never loaded as agent config.
- Credentials: the CLI never stores secrets. Harness logins are the
  harness's own; the ledger token is read from an environment variable you
  name.
- uv is installed from a pinned, checksum-verified GitHub release — no
  piped-to-shell installers.

## Development

```bash
make check        # build + vet + test
make lint         # staticcheck
go test ./...     # integration tests run against fake harness binaries in
                  # testdata/fake-harnesses/ — no API keys needed
```

The `catalog check` command compares the embedded standalone profile
(`internal/catalog/data/standalone.yaml`, seeded from traust's
docs/standalone-usage.md and model-classes.md) against a traust checkout;
CI runs it on every push to catch drift.

### Releasing

`VERSION` and `version.args` move together — `make check-version` (part of
`make check`) fails on drift, and the release workflow fails a tag that is not
`v$(cat VERSION)`. Konflux builds every push and stamps the same value into
the binary, the image label and the released tag; see
[docs/konflux.md](docs/konflux.md).

## License

Apache-2.0 — see [LICENSE](LICENSE). Part of the
[traust](https://github.com/traust-security) family.
