# Build stage — the static Go binary. CGO is off so the payload stage can be
# scratch; -buildvcs=false because the Konflux build context has no .git (the
# version is stamped from version.args, not git describe).
#
# go-toolset is the RHEL9 builder image; its Go patch level must stay at or
# above the go directive in go.mod (1.26.8), which is why this tag is pinned to
# a patch release rather than the rolling 1.26 minor.
FROM registry.access.redhat.com/ubi9/go-toolset:1.26.10-1793736700@sha256:c2bd0309bf78ae317024fdfc5602a31704e0dba9572f6d4b0bd4b681033dc965 AS build

# The s2i base image runs as uid 1001; the build stage only has to write to its
# own workdir and module cache, so it does not need that runtime identity.
USER 0

# The semver comes from version.args via the Konflux build-args-file param.
# GOTOOLCHAIN=local keeps the build offline-clean: go.mod pins a patch-level
# minimum, and a builder below it must fail loudly rather than try to download
# a toolchain inside a hermetic build.
ARG CLI_VERSION=0.0.0
ENV CGO_ENABLED=0 GOTOOLCHAIN=local

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -buildvcs=false -trimpath \
      -ldflags "-s -w -X github.com/traust-security/traust-cli/cmd.version=${CLI_VERSION}" \
      -o /out/traust-cli ./cmd/traust-cli

# Payload stage — the same OCI artifact convention as traust/Containerfile:
# FROM scratch, no base OS to patch, EC labels re-declared explicitly because
# there is no base to inherit them from.
FROM scratch

ARG CLI_VERSION=0.0.0

LABEL name="traust-cli" \
      org.opencontainers.image.version="${CLI_VERSION}" \
      summary="traust-cli — one binary for the traust security workflow" \
      description="A statically linked Go CLI that bootstraps the traust harness and drives \
its skills headlessly through the agent harness of your choice." \
      version="${CLI_VERSION}" \
      io.k8s.display-name="traust-cli" \
      io.k8s.description="CLI front door for the traust security workflow" \
      io.openshift.tags="ai,security,cli,agents" \
      com.redhat.component="traust-cli" \
      maintainer="Traust maintainers <traust@redhat.com>" \
      vendor="Red Hat, Inc." \
      url="https://catalog.redhat.com/en/search?searchType=containers" \
      release="1" \
      distribution-scope="private"

COPY --from=build /out/traust-cli /traust-cli

# Certification tooling (ecosystem-cert-preflight-checks HasLicense) looks for
# license text at the absolute path /licenses, not under a workdir.
COPY LICENSE ./LICENSE
COPY LICENSE /licenses/LICENSE

USER 1001

ENTRYPOINT ["/traust-cli"]
