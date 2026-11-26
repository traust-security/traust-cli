# Building traust-cli in Konflux

The binary is built by the Konflux Component `traust-cli` (Application `traust`,
namespace `source-code-intelligence-tenant`) and published as a signed OCI
artifact — the same carrier convention as the `traust` harness artifact, which
is also a `FROM scratch` image with no shell.

GitHub Releases (`goreleaser`, `.github/workflows/release.yml`) remain a
distribution channel. Konflux is the signed, attested, continuously rebuilt
copy: every push, every pull request, and every release.

## What gets published

Both images follow the standard Konflux build → release flow:

| Stage | Slim (`traust-cli`) | Self-contained (`traust-cli-toolkit-plus-feeds`) | Access |
|---|---|---|---|
| Build output (`.tekton/`) | `quay.io/redhat-user-workloads/source-code-intelligence-tenant/traust/traust-cli` | `quay.io/redhat-user-workloads/source-code-intelligence-tenant/traust/traust-cli-toolkit-plus-feeds` | private (tenant) |
| Red Hat managed release (`rhtap-releng-tenant/traust`) | `quay.io/redhat-services-prod/source-code-intelligence-tenant/traust/traust-cli` | `quay.io/redhat-services-prod/source-code-intelligence-tenant/traust/traust-cli-toolkit-plus-feeds` | private (Red Hat) |
| Community mirror (`traust-cli-quay-mirror`) | `quay.io/traust-security/traust-cli` | `quay.io/traust-security/traust-cli-toolkit-plus-feeds` | public (once provisioned) |

| Tag | Produced by | Notes |
|---|---|---|
| `on-pr-<sha>` | pull-request build (build repo only) | expires after 5 days |
| `<sha>` / `<short-sha>` | release pipelines | immutable per commit |
| `<digest>` | release pipelines | immutable per manifest digest |
| `<X.Y.Z>` | release pipelines, from `org.opencontainers.image.version` | stamped from `version.args` — cannot disagree with `--version` |
| `latest` | release pipelines | **rewritten every release — pin digests** |

Konflux releases copy images via `cosign copy` (`push-snapshot`), so each
destination receives the same manifest digest along with Konflux's cosign
signatures, SLSA provenance attestations, and SBOMs.

The slim artifact contains exactly one thing at a fixed path: `/traust-cli`, a
statically linked (`CGO_ENABLED=0`) binary set as `ENTRYPOINT`. There is no
shell, no package manager, and no OS package set, so its only CVE surface is
the Go module graph — which is what the SBOM describes.

**linux/amd64 only.** macOS, Windows and `arm64` builds come from GitHub
Releases; do not point `latest` consumers on those platforms at the image.

## Mirroring to `quay.io/traust-security`

Mirroring to `quay.io/traust-security` is Konflux-native: it runs through a
tenant-managed `ReleasePlan` and `ReleasePlanAdmission`
(`traust-cli-quay-mirror`) in `source-code-intelligence-tenant` using the
official `release-service-catalog` `push-to-external-registry` pipeline,
alongside the existing `traust-releaseplan` → `rhtap-releng-tenant/traust`
release. Auto-release is scoped to `traust-cli` and
`traust-cli-toolkit-plus-feeds` via the `updatedComponentIs(...)` CEL
annotation, and every release is gated by `traust-enterprise-contract-policy`.

The manifests live in `konflux-release-data` under
`tenants-config/cluster/stone-prod-p02/tenants/source-code-intelligence-tenant/`
(`traust-cli-quay-mirror.{release-plan,release-plan-admission,service-account,role-binding}.yaml`).

### One-time Quay and cluster prerequisites

Before the first release to `quay.io/traust-security` can succeed:

1. Create three repositories in `quay.io/traust-security`:
   - `quay.io/traust-security/traust-cli` (**public**)
   - `quay.io/traust-security/traust-cli-toolkit-plus-feeds` (**public**)
   - `quay.io/traust-security/traust-cli-release-oci` (**private**; holds
     short-lived Konflux Trusted Artifacts for the release pipeline, 1d expiry)
2. Create a Quay robot account in `traust-security` with **write** permission on
   those three repositories only.
3. Create the `traust-security-quay-push` secret in
   `source-code-intelligence-tenant` using **only** that robot account, keyed
   to `quay.io/traust-security` (not bare `quay.io`, so `select-oci-auth` never
   collides with the tenant's `quay.io/redhat-user-workloads/...` pull
   secrets). Rewriting the auth key scopes credential lookup only — it does not
   restrict the account's Quay permissions, so never log in with a personal or
   broadly privileged Quay account:

   ```sh
   (
     set -e
     umask 077
     robot='traust-security+<robot>'
     case "$robot" in
       'traust-security+<robot>') echo "replace <robot> with your robot name" >&2; exit 1 ;;
       traust-security+?*) ;;
       *) echo "refusing non-robot account: $robot" >&2; exit 1 ;;
     esac
     dir="$(mktemp -d)"
     trap 'rm -rf "$dir"' EXIT
     podman login --compat-auth-file "$dir/raw.json" -u "$robot" quay.io
     jq -e --arg robot "$robot" '
       .auths["quay.io"] as $entry
       | select(($entry.auth | strings | @base64d | split(":")[0]) == $robot)
       | {auths: {"quay.io/traust-security": $entry}}
     ' "$dir/raw.json" > "$dir/scoped.json"
     oc create secret generic traust-security-quay-push \
       -n source-code-intelligence-tenant \
       --type=kubernetes.io/dockerconfigjson \
       --from-file=.dockerconfigjson="$dir/scoped.json"
   )
   ```

   Verify the stored secret identifies only the restricted robot (without
   printing the token):

   ```sh
   oc get secret traust-security-quay-push -n source-code-intelligence-tenant -o json \
     | jq -r '.data[".dockerconfigjson"] | @base64d | fromjson | .auths | to_entries[] | "\(.key): \(.value.auth | @base64d | split(":")[0])"'
   ```

### Scope

The Konflux `ReleasePlan`/`ReleasePlanAdmission` mirrors **forward releases
only**: for each Snapshot released after onboarding, `push-snapshot` publishes
the five tag templates (`{{ oci_version }}`, `latest`, `{{ git_sha }}`,
`{{ git_short_sha }}`, `{{ digest_sha }}`) plus cosign signatures,
attestations, and SBOMs. Pre-existing tags in `quay.io/redhat-services-prod`
are not backfilled.

## Using it

Run the binary directly (the useful in-image smoke test):

```bash
podman run --rm quay.io/traust-security/traust-cli:latest --version
```

Extract the binary onto the host:

```bash
img=quay.io/traust-security/traust-cli@sha256:<digest>
podman create --name traust-cli-extract "$img"
podman cp traust-cli-extract:/traust-cli ./traust-cli
podman rm traust-cli-extract
chmod +x ./traust-cli
```

(`docker create` / `docker cp` works the same way.)

This (slim) image ships the CLI only, not the harness, the managed venv or the
agent harnesses. Real work inside a container needs `$HOME/.traust` and the
harness credentials mounted, which is why extraction or GitHub Releases is the
normal path for interactive use. For a batteries-included alternative that
needs only harness credentials at runtime, see
[The self-contained image](#the-self-contained-image-traust-cli-toolkit-plus-feeds) below.

## Version stamping contract

- `VERSION` is the source of truth; `version.args` mirrors it as a single
  `CLI_VERSION=X.Y.Z` line. Both move together.
- The PipelineRuns pass `build-args-file: version.args`; the Containerfile's
  `ARG CLI_VERSION` stamps `-X github.com/traust-security/traust-cli/cmd.version`,
  the `org.opencontainers.image.version` label, and the release tag.
- `make check-version` (part of `make check`, and a CI step) rejects drift
  between the two files. `release.yml` rejects a tag that is not
  `v$(cat VERSION)`.
- To release: edit `VERSION` and `version.args`, commit, then tag `vX.Y.Z`.
  Konflux rebuilds on the push and stamps the same value, so a `main` build
  between releases reports the upcoming version — the same behaviour as the
  `traust` harness.
- Never derive the version from `git describe`: hermetic builds have no `.git`
  (and the Containerfile passes `-buildvcs=false`).
- The build arg is `CLI_VERSION`, not `VERSION`: the `go-toolset` builder base
  sets `ENV VERSION=<go patch level>`, and an inherited `ENV` shadows a
  same-named `ARG` during `${...}` expansion — so an `ARG VERSION` would stamp
  the Go version into the binary instead of the release version. The
  non-colliding name is the fix (applies to both `Containerfile` and
  `Containerfile.toolkit-plus-feeds`).

## Pipeline parameters that are not the UI defaults

Everything in `.tekton/` is generated by the Konflux UI except these three
`spec.params` entries on both PipelineRuns:

| Param | Value | Why |
|---|---|---|
| `prefetch-input` | `[{"type": "gomod", "path": "."}]` | hermeto pre-populates the Go module cache |
| `hermetic` | `"true"` | no network during the buildah build; dependencies must come from the prefetch |
| `build-args-file` | `version.args` | version stamping (without it the binary reports `0.0.0`) |

If the UI regenerates these files (bundle digest bumps, tenant task changes),
re-apply those three params and re-check the identity keys: `metadata.name`,
the `application`/`component` labels, `output-image`, and
`taskRunTemplate.serviceAccountName: build-pipeline-traust-cli`.

> Known drift elsewhere in the estate: `traust/Containerfile` documents a
> `version.args` build-args-file that its PipelineRun never sets, so that
> Component's builds fall back to `0.0.0`. This repo sets the parameter
> explicitly — do not drop it.

## Reproduce the build locally

```bash
make check                                  # vet + tests + version gate
make build && ./bin/traust-cli --version    # stamps VERSION
podman build --build-arg CLI_VERSION="$(cat VERSION)" -t traust-cli:dev .
podman run --rm traust-cli:dev --version
```

Approximate the hermetic build (after the builder image is present locally) to
prove nothing reaches the network:

```bash
podman build --network=none --build-arg CLI_VERSION="$(cat VERSION)" -t traust-cli:dev .
```

## Verify a published build

```bash
img=quay.io/traust-security/traust-cli@sha256:<digest>
cosign verify "$img" --key <tenant public key>
skopeo inspect "$img"
syft "$img"                                  # Go module SBOM
podman run --rm "$img" --version
```

Then confirm the EC/Conforma result in the Konflux UI and that
`traust-cli-on-pull-request` is listed as a required check on `main`.

## When it fails

| Symptom | Cause | Fix |
|---|---|---|
| `go.mod requires go >= 1.26.8` / toolchain not available | builder image below the `go` directive in `go.mod`, and `GOTOOLCHAIN=local` fails instead of downloading | bump the builder image tag (Renovate group "Containerfile base images"), or relax the `go` directive |
| `cannot find module ...` / dial errors mid-build | the hermetic prefetch missed a module | confirm the module is publicly fetchable; if it went private, wire the `git-basic-auth`/`netrc` workspaces into both PipelineRuns |
| Binary and `org.opencontainers.image.version` report `0.0.0` | `build-args-file` param lost | re-add it (see the table above) |
| snyk / shell / unicode checks noisy on fixtures | `sast-target-dirs` defaults to `.` | narrow it to `cmd,internal` |
| EC fails `labels.required_labels` | a label was dropped from the scratch stage | restore `vendor`, `url`, `release`, `distribution-scope` |
| EC fails `HasLicense` | license text not at the absolute path | keep `COPY LICENSE /licenses/LICENSE` |

## The self-contained image (traust-cli-toolkit-plus-feeds)

`Containerfile.toolkit-plus-feeds` builds a second, batteries-included,
air-gap-ready variant that bakes every runtime dependency `traust-cli init`
normally bootstraps on first use: `git`, a uv-managed Python 3.12 venv, the
pinned traust checkout, its synced engine/ledger/contracts, `TRAUST_CONFIG_HOME`,
the `secure-code-audit` scanner toolchain, an optional agent harness, **and the
offline vulnerability feeds** (the grype DB and the osv-scanner offline
databases). `traust-cli run` works out of the box with no external vuln lookups
— only the agent harness credentials are injected at runtime.

It is built by a separate Konflux Component, `traust-cli-toolkit-plus-feeds`
(`.tekton/traust-cli-toolkit-plus-feeds-{push,pull-request}.yaml`), built into
`quay.io/redhat-user-workloads/source-code-intelligence-tenant/traust/traust-cli-toolkit-plus-feeds`
and released to both
`quay.io/redhat-services-prod/source-code-intelligence-tenant/traust/traust-cli-toolkit-plus-feeds`
(private) and `quay.io/traust-security/traust-cli-toolkit-plus-feeds` (public
mirror).

The same tag convention and version-stamping contract as the slim image apply.

### Build layout and size

`Containerfile.toolkit-plus-feeds` is three stages: `build` compiles the Go
binary, `assemble` runs `traust-cli init` with the full build toolchain (golang
for govulncheck, python3.11+pip for pip-audit) plus the offline-feed download,
and `full` copies only the finished `/opt/traust` workspace into a clean runtime
base. The go-build/uv/dnf caches and the `chown`/`chmod` churn stay in the
discarded assemble stage, so they never reach the published layers — the single
`COPY --from=assemble --chown=1001:0` writes the tree once (group-writable modes
are set in the assemble stage so no post-copy layer duplicates the multi-GB
tree).

Approximate sizes: **~5.4 GB** by default (grype DB ~3 GB + osv offline DBs
~290 MB), **~2.3 GB** with `--build-arg SKIP_GRYPE_DB=1` (keeps the osv DBs,
drops grype's — grype re-fetches it at runtime when online). The slim image is
~18 MB.

### Offline vulnerability feeds (air-gap)

This image is the air-gapped answer, so it bakes the feeds the dependency
pre-scan needs instead of calling out:

- **grype** — its DB is populated by `init` (schema v6) and lives under the
  workspace cache.
- **osv-scanner** — osv-scanner only downloads the ecosystems it sees, so the
  assemble stage scans a stub carrying one lockfile per major ecosystem (PyPI,
  npm, Go, Maven, RubyGems, crates.io, Packagist, NuGet) with
  `--download-offline-databases`. The databases (~290 MB) land in
  `OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=/opt/traust/.traust/osv-db` and travel
  into the runtime image, and `feeds.yaml`'s osv source is flipped to
  `access: air-gapped` so the engine runs `osv-scanner --offline` against them
  (docs/setup.md "Air-gapped OSV scanning"). Offline OSV coverage is narrower
  than the API (no Go stdlib advisories — govulncheck, which carries its own DB,
  covers Go); refresh the feeds by rebuilding.

### How it differs from the slim image

| | slim (`Containerfile`) | toolkit-plus-feeds (`Containerfile.toolkit-plus-feeds`) |
|---|---|---|
| Base | `FROM scratch` | `ubi9/ubi` (needs `bash`/`dnf`/`git`) |
| Contents | one static binary | binary + venv + traust checkout + toolchain + offline feeds (+ optional harness) |
| CVE surface | Go module graph only | OS + Python + scanners + node — much larger |
| Hermetic build | `hermetic: "true"`, gomod prefetch | **`hermetic: "false"`, no prefetch** (build clones traust, downloads uv, syncs PyPI, installs scanners/npm, pulls vuln DBs) |
| First-run setup | `traust-cli init` + mount `$HOME/.traust` | none; workspace baked at build time |
| Vuln lookups | n/a | offline (grype DB + osv-scanner air-gapped DBs baked) |
| Reproducibility | fully pinned | pin `TRAUST_REF`, base digest, uv (`0.13.4`); scanner/npm/feed versions float |

Because the assemble stage needs the network, the PipelineRuns set
`hermetic: "false"` and leave `prefetch-input` empty. That is a deliberate
trade — the slim image remains the hermetic, Go-module-only artifact.

### Build args

| Arg | Default | Effect |
|---|---|---|
| `CLI_VERSION` | `0.0.0` | stamped via `version.args` (same as slim) |
| `TRAUST_REF` | `latest` | traust tag/branch to bake; pin to a tag for reproducible releases |
| `HARNESS` | `claude` | bundle an agent harness binary (`claude`, `codex`, or `none`); `none` also skips installing `nodejs`/`npm`. Credentials are never baked. |
| `SKIP_GRYPE_DB` | `0` | `1` drops the ~3 GB grype DB (grype re-fetches it at runtime when online); the osv offline DBs are kept. |

`HOME=/opt/traust` is load-bearing: `init` writes the workspace to
$HOME/.traust and stores **absolute** paths (venv, uv-managed Python, config
home, osv DB) in `state.json`/env. Do not override `HOME` when running the image,
or state resolution breaks. `$HOME` itself is owned `1001:0` and
group-writable, and the build keeps npm's cache out of it, so tools that
create per-user state under `$HOME` (opengrep's `~/.opengrep`, claude's
`~/.claude`) work as the runtime user — older images shipped `/opt/traust`
root-owned and broke them (issue #58).

### Build and smoke-test locally

```bash
podman build -f Containerfile.toolkit-plus-feeds \
  --build-arg CLI_VERSION="$(cat VERSION)" \
  --build-arg TRAUST_REF=latest \
  -t traust-cli-toolkit-plus-feeds:dev .

podman run --rm traust-cli-toolkit-plus-feeds:dev --version      # stamped version
podman run --rm traust-cli-toolkit-plus-feeds:dev doctor --json  # state/venv/engine-import + toolchain green; see "not baked" below
podman run --rm traust-cli-toolkit-plus-feeds:dev skills --json  # catalog parses, no creds needed
```

A real run needs harness credentials and a mounted target:

```bash
podman run --rm \
  -e ANTHROPIC_API_KEY=... \
  -v "$PWD/target:/work:ro,Z" \
  traust-cli-toolkit-plus-feeds:dev run secure-code-audit /work
```

The image declares `VOLUME ["/opt/traust/.traust/cli/runs", "/tmp"]`, so run
journals persist across containers (name the volume, e.g.
`-v traust-runs:/opt/traust/.traust/cli/runs`, to keep them beyond one
`podman run --rm`) and `/tmp` stays writable under `podman run --read-only`.
The runs directory is baked into the image (`1001:0`, group-writable) — a
volume mounted at a path absent from the image is created root-owned by some
runtimes (Docker), which would block journal creation for the non-root user.

The image follows Red Hat's container-image guidelines for rootless use: it
runs as the non-root uid 1001 in the root group with group-writable (`g=u`)
files under `/opt/traust`, and the build applies UBI security errata newer
than the pinned base digest. Write behavior verified on rootless podman 5:

| Invocation | `/opt/traust` workspace | runs volume | `/tmp` |
|---|---|---|---|
| default (uid 1001 via the subuid map) | writable | writable | writable |
| `--user 1234:0` (arbitrary uid, root group) | writable | writable | writable |
| `--userns=keep-id` | writable | writable | writable |
| `--read-only` + `SKIP_FEED_UPDATE=1` | read-only | writable | writable |

Under `--userns=keep-id` the process keeps the image's uid 1001 (mapped into
the subuid pool) and gains the invoking user's uid/gid as a supplementary
identity, so host-owned bind mounts stay group-accessible. A read-only
rootfs (with `SKIP_FEED_UPDATE=1`) suits read-only commands — `--version`,
`skills`, `doctor` — whose writes stay on the declared volumes. A full `run`
needs the writable image layer: the harness executes in the baked traust
checkout and writes its reports there, and agent harnesses keep their own
state under `$HOME` (`~/.claude`, `~/.opengrep`), none of which is on a
declared volume.

The one expected non-green `doctor` item is deployment config the image
deliberately does not bake:

- **Deployment config (estate)** — `corpus-config.yaml` (which trees/repos your
  estate analyzes) and the ledger signing key are operator- and estate-specific.
  The image ships the template `TRAUST_CONFIG_HOME`, so `doctor`'s completeness
  check errors with `corpus-config.yaml: trees is empty` until you provide them
  (edit the baked config, or mount your own over `/opt/traust/.traust/config`).
- **Harness credentials** — injected at runtime (env or mounted login).

The scanner toolchain reports green, including the osv-scanner offline DB (baked;
`feeds.yaml` is air-gapped).

### When it fails (toolkit-plus-feeds image)

| Symptom | Cause | Fix |
|---|---|---|
| `init` fails with git/dial errors | PipelineRun ran hermetic | keep `hermetic: "false"` and `prefetch-input: ""` on both PipelineRuns |
| `venv` / `engine-import` fails at runtime | `HOME` overridden, so baked absolute paths don't resolve | run with the image default `HOME=/opt/traust` |
| `osv-scanner NO DB` at runtime | `OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY` overridden, or the osv DB layer was stripped | run with the image default env; rebuild to refresh the DB |
| harness `not found in PATH` at run | `HARNESS=none` built, or npm global bin off PATH | rebuild with `HARNESS=claude`, or mount the harness binary and set `TRAUST_CLI_<ID>_BIN` |
| EC flags the non-hermetic build | this image cannot be hermetic | expected; the slim image is the hermetic artifact |
