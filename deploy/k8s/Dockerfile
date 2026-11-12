# WorkflowFiesta Runner — Kubernetes image
#
# Purpose: run the headless runner inside a customer's own Kubernetes cluster.
#          In Kubernetes mode the runner creates a one-shot batch/Job per
#          script step, streams the pod's logs back to the API, and deletes the
#          Job when it exits — so no Docker socket is mounted and no privileged
#          container is needed.
#
# Differs from the repo-root Dockerfile (plain Docker / host use) by running as
# a non-root user, reaping children with tini, defaulting to Kubernetes mode,
# and shipping a shell plus git/jq so local-mode scripts have something to run.
#
# Build:  docker build -f deploy/k8s/Dockerfile -t workflowfiesta-runner-k8s .
#         NOTE: the build context is the repo root — the binary is compiled
#         from source here rather than downloaded from a release.
# Deploy: see deploy/k8s/README.md

FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -tags nolocalui \
      -ldflags="-w -s -X main.version=${VERSION}" \
      -o workflowfiesta-runner ./cmd/runner

FROM debian:12-slim

ENV DEBIAN_FRONTEND=noninteractive

# bash/git/jq/openssh-client — available to scripts when the runner is deployed
#   in local mode (CONTAINER_RUNTIME=local) instead of creating Jobs
# tini — PID 1 reaper + signal forwarding for a long-lived polling daemon
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    openssh-client \
    tini \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/workflowfiesta-runner /usr/local/bin/workflowfiesta-runner

# Non-root runtime user. The runner writes saved credentials to
# $HOME/.workflowfiesta (only used by `register`; the Deployment passes the
# token via env instead) and uses /workspace as its working directory.
RUN useradd --create-home --uid 10001 --shell /bin/bash runner \
 && mkdir -p /workspace \
 && chown runner:runner /workspace

USER runner
WORKDIR /workspace

# CONTAINER_RUNTIME is also auto-detected from KUBERNETES_SERVICE_HOST, but
# setting it explicitly keeps the behaviour obvious and overridable.
ENV HOME=/home/runner \
    CONTAINER_RUNTIME=kubernetes \
    WORKFLOWFIESTA_LABELS=linux,x86_64,kubernetes

ENTRYPOINT ["/usr/bin/tini", "--", "workflowfiesta-runner"]
CMD ["run"]
