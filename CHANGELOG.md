# Changelog

## 0.1.0 — unreleased

Initial implementation.

- Managed workspace bootstrap (`init`, `doctor`, `update`, `env`): pinned
  checksum-verified uv, traust checkout at a stable tag, `uv sync`, config
  home via `install_traust`, optional scanner toolchain profiles.
- Skill catalog parsed from SKILL.md frontmatter with a shipped standalone
  profile and drift guard (`skills`, `catalog check`).
- Headless skill execution through claude code, codex, pi, opencode, crush
  and an experimental goose adapter (`run`), with a live TUI run monitor,
  model-floor pre-flight, budget warn/degrade/refuse bands, run journals,
  artifact discovery + schema validation + rendering, and spend declaration.
- Read-only report browsing (`report`) and ledger views (`ledger`).
- Two container variants: the slim `FROM scratch` artifact (`Containerfile`,
  the binary only) and a self-contained, air-gap-ready image
  (`Containerfile.toolkit-plus-feeds`, published as `traust-cli-toolkit-plus-feeds`)
  that bakes the uv-managed venv, the pinned traust checkout, `TRAUST_CONFIG_HOME`,
  the `secure-code-audit` toolchain, an agent harness, and the offline
  vulnerability feeds (grype DB + osv-scanner offline databases, with `feeds.yaml`
  set to air-gapped) via `init` at build time — `run` works with only harness
  credentials at runtime and no external vuln lookups. Built by a separate,
  non-hermetic Konflux component.

### Fixes from first field use

- Budget policy parsing accepts the shipped sequence-form `bands:`
  (`- {at: 1.5, action: warn}`) in addition to the map form.
- Harness failures now surface the stderr tail (provider 401s are visible
  without opening the journal) and are recorded in `result.json` as
  `harness_error`.
- Pre-flight model availability check (crush: verified against
  `crush models`): unrunnable models fail fast with actionable guidance
  instead of an authentication error two seconds into the run.
- The run monitor no longer looks frozen on quiet harnesses: the status bar
  shows how long since the last output, and ctrl+c requires a confirming
  second press within six seconds — a single stray ctrl+c cannot kill a
  long-running audit.
- Force-quitting the monitor (or any early exit) now tears the harness down
  and joins the runner before returning: no more "run produced no result",
  and no send-on-closed-channel races.
- Failed, refused and aborted runs still discover and validate their
  artifacts — partial output and checkpoint state are visible exactly when a
  run dies mid-flight.
- The run summary (artifacts, journal path, harness error, final message)
  prints after the interactive monitor exits, not only in plain mode.
- Line wrapping adapts to the terminal width everywhere. Monitor transcript
  lines wrap (ANSI-aware) instead of truncating, and re-flow on resize;
  the header and status bar clip to fit one line at any width; the report
  browser rebuilds its table columns and re-renders its markdown at the
  current width; plain (non-TTY) output and summary lines wrap to the
  terminal width instead of a fixed 200 characters.
- crush runs now execute through `crush server` in client mode: thinking,
  tool calls and cumulative token usage stream live into the monitor
  (run_complete is correlated by RunID), budget bands and spend
  declarations work on real usage, and aborts cancel the remote session
  through the cancel endpoint before teardown. Permission and question
  requests cannot be satisfied unattended and fail closed (exit 2) naming
  the tool. Runs fall back to `crush run` when the server cannot start.
- Full-permission consent: `traust-cli init` asks whether to run skills
  with all tools auto-approved (the harness's skills require unattended
  bash and file writes) and records the answer in the workspace state;
  headless installs pass `--full-permission`. Runs translate the consent
  into the right flag per harness — claude
  `--dangerously-skip-permissions`, crush `--yolo` / the server's
  permissions-skip endpoint, codex
  `--dangerously-bypass-approvals-and-sandbox`, pi's full toolset — and
  surface it in the notes, the run journal and `doctor`. Without consent,
  permission requests still fail closed (exit 2) with guidance.
- Every remaining print path now respects the terminal width: the
  full-permission and standalone-profile warnings, the run summary's
  harness-error block (source indentation from harness error boxes is
  stripped so re-wrapped lines start at the left edge) and artifact lines,
  doctor details, init step logs, and the skills table (which drops its
  description column below ~77 columns instead of overflowing).
- Interactive screens now gate on a real terminal check (ioctl) instead of
  "is a character device": `traust-cli skills < /dev/null` used to open the
  picker and hang forever, because /dev/null is a character device.
- Cobra help output respects the terminal width: flag usages wrap with
  continuations aligned under each description (FlagUsagesWrapped at the
  measured width), Long descriptions re-wrap, the trailing use-hint breaks
  on narrow terminals, cobra's built-in completion commands are hidden from
  the command list (still invocable), over-long command summaries were
  shortened, and the init --forge default URL moved out of the flag line
  (an unbreakable token that could not fit pflag's description column).
- Runs execute in the background by default: `traust-cli run` spawns a
  detached worker process (setsid, survives the terminal) and attaches to
  it; `--detach` fires and forgets. The run journal is the event bus:
  `attach` replays it and follows it live (Ctrl-C detaches without
  cancelling — x twice cancels), `runs` lists run states, `stop` cancels
  through a graceful SIGINT/SIGTERM/SIGKILL ladder, `logs` renders the
  transcript and `result` prints the stored outcome with the run's own
  exit code. Attaching to a finished run replays the full transcript.
- The monitor now shows tool results (crush tool_result parts, truncated),
  which are the bulk of a run's output; journal raw lines no longer
  duplicate parsed events, so attach replays render once.
- Fixed the detach hang: the attach monitor's relay goroutine waited for
  the journal tailer to finish, but a detached run keeps tailing — so the
  process (and the terminal) stayed open until a second ctrl+c killed it.
  The relay now abandons the feed the moment the monitor exits; one ctrl+c
  detaches cleanly, printing the re-attach hint and exiting 0.
- Multi-stage flows: `flow audit-triage` (audit then triage, outputting
  triaged findings), `flow remediate-verify` (patch then verify against a
  patched checkout with the generated patches applied) and `flow full`
  (the whole loop). Stages chain through their artifacts — the audit
  report feeds triage (--auto for headless), triaged findings feed patch,
  verify audits a fresh clone with the patches applied — and every stage
  is an ordinary background run. `flow status` lists flows and stage
  detail; interactive flows follow stage-by-stage with Ctrl-C detaching
  without cancelling.
- `peek <run-or-flow>`: a one-shot snapshot of what is happening right now
  — state, usage and the last dozen journal entries, printed and gone. A
  flow peek shows the stage chain and recurses into the current stage's
  run.
- The init wizard's full-permission confirm now defaults to Yes (the
  recommended choice) and its warning spells out that runs and flows
  (audit → triage → patch → verify) fail closed without it; doctor's
  scoped-mode message says the same.
- init installs the secure-code-audit scanner toolchain by default (one
  profile covers every flow stage — a superset of the vuln-scan and triage
  profiles) and no longer asks: the wizard question was a decision users
  could not make before knowing what they would run. --toolchain none
  stays for opt-out, and already-installed profiles are skipped so re-init
  stays fast.
- Runs pre-flight the skill's toolchain profile (admin toolchain doctor)
  and surface a non-fatal warning naming the profile, the coverage cost,
  and the fix when it is incomplete — degraded audits now say why they
  are degraded instead of silently producing thinner evidence.
- Run ids widened to a 6-hex random suffix: the 4-hex suffix collided
  ~8% of the time once a dozen runs shared a second, and a collision
  meant a shared run directory.
- Fixed the audit-triage flow failing on a successful audit: campaign-
  layout skills resolve their output root through locations.yaml and write
  findings/<org>/<repo>/<repo>-security-audit.json several levels deep —
  depth-one artifact discovery never saw it. Artifact discovery now also
  scans the configured findings tree (locations.yaml analysis_results,
  resolved relative to the config home, plus workspace defaults)
  recursively, skipping non-report trees (graph, metrics, manifests); and
  init anchors analysis_results inside the managed workspace when (and
  only when) it is empty, so a fresh workspace is self-contained instead
  of sending the agent hunting for any analysis-results checkout it can
  find.
- `run` passes skill flags through verbatim (git-style): traust-cli's own
  flags are parsed only before the skill name, so `run triage <report>
  --auto` works instead of failing with "unknown flag". Flags after the
  skill name — --auto, --diff, --since, --votes — belong to the skill.
- Authentication failures now explain themselves: when a harness error
  is a provider 401, the run appends guidance naming the resolved model
  and its registry role, the model-registry.yaml fix, and the reminder
  that --model must come before the skill name (a model can be listed
  AND unauthenticated — availability checks cannot see credentials).
- Error results that arrive with a zero process exit code (claude error
  subtypes, crush run_complete errors) no longer read as successful
  runs; the result's error text is recorded as the harness error.
- The live monitor now streams instead of showing fragments. Crush
  rewrites message parts in place as they grow (text fills out, tool
  inputs stream in) and delivers tool results in separate role=tool
  messages — the old index-based emit captured each part's first frame
  (one-line text fragments, tool calls with no input) and dropped every
  tool result. Parts are now tracked by msgid:partIndex with keyed
  replace events: the monitor updates the keyed line in place (live
  streaming text, tool inputs filling in), thinking renders as one
  growing line, and plain output (attach --no-tty, logs, peek) collapses
  each part's updates to its final content.
- An SSE stream ending without run_complete no longer kills the run: the
  crush session lives in the server, not the connection, so the client
  now reconnects with the same client id (part state survives, replayed
  snapshots dedup) up to five times — and if the run finished while
  disconnected, its final state is recovered from the session record
  instead of failing a 45-minute run with "unexpected EOF".
- Artifact validation is scoped to reports: skill-internal JSON in the
  findings tree (triage state, layers, verdict queues) has no report
  schema and was failing `reporting validate` noisily; it is now recorded
  as informational. Rendering is scoped to the audit-report family —
  triage JSONs have no title field and crashed the report renderer; their
  skills write their own Markdown companions.
- Harness adapters verified against the installed clients and updated
  for their current wire formats: pi >= 1.4 carries the final assistant
  message and usage on turn_end (message.usage), opencode >= 1.19 uses
  top-level event types with part payloads and part.tokens on
  step_finish, and claude's thinking blocks and system bookkeeping
  (thinking-token estimates) are mapped or silenced instead of leaking
  as raw JSON. Recognized-but-ignored protocol events no longer print.
- Migrated the TUI stack to charmbracelet v2 (bubbletea, bubbles,
  lipgloss, huh, glamour — now under charm.land/v2 module paths).
  View() returns tea.View with alt-screen and mouse mode on the view
  instead of program options; viewport dimensions moved behind
  SetWidth/SetHeight; KeyMsg became the KeyPressMsg concrete type.
  Lipgloss v2 renders full-fidelity ANSI unconditionally, so every
  styled print goes through the profile-aware lipgloss writers —
  non-TTY output is stripped exactly as it was on v1, verified both
  ways (TTY keeps color, pipes stay plain).
