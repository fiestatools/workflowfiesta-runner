# Script-job image for the Kubernetes executor.
#
# The executor runs `/bin/bash -c`. public debian:bookworm-slim is dash and
# has no bash, curl, or python3, so chat run_bash and workflow scripts fail
# before they do any work. This image adds those three tools and keeps apt,
# so a script can still install something else in the same command.
#
# Build from the repo root:
#   docker build --platform linux/amd64 -f deploy/k8s/job.Dockerfile \
#     -t workflowfiesta-script-job .
#
# Pin the pushed digest as KUBERNETES_DEFAULT_IMAGE and as the Environment
# dockerImage. Script steps copy the Environment image and ignore the
# runner default.

FROM public.ecr.aws/docker/library/debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      bash \
      ca-certificates \
      curl \
      python3 \
 && rm -rf /var/lib/apt/lists/*

# No USER. The executor does not set a securityContext, and scripts still
# apt-get install extra packages. A non-root user would make that fail.
WORKDIR /workspace
CMD ["bash"]
