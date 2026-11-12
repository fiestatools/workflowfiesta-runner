# Kubernetes deployment

Run the headless WorkflowFiesta runner inside your own Kubernetes cluster, so
workflow script steps execute on your infrastructure.

In Kubernetes mode the runner:

1. Polls the WorkflowFiesta API for pending jobs (every 3s) and heartbeats
   (every 30s).
2. Creates a one-shot `batch/Job` per script step, using the Docker image from
   the step's Environment.
3. Streams the pod's logs back to the API in real time.
4. Deletes the Job once it exits (a 5-minute TTL cleans up anything orphaned).

No Docker socket is mounted and no privileged container is required.

## Contents

| Path | Purpose |
|---|---|
| `Dockerfile` | Debian 12 slim + the runner compiled from this repo |
| `namespace.yaml` | `workflowfiesta` namespace |
| `serviceaccount.yaml` | ServiceAccount for the runner pod |
| `rbac.yaml` | Namespace-scoped Role/RoleBinding (jobs + pods + pods/log) |
| `deployment.yaml` | The runner Deployment |

This image differs from the repo-root `Dockerfile` (plain Docker / host use):
it runs as a non-root user, uses tini as PID 1, defaults to
`CONTAINER_RUNTIME=kubernetes`, and ships a shell plus git/jq.

## Deploy

```sh
# 1. Register the runner — one-time, using a code from Runners → "Set up a runner"
kubectl run wf-register --rm -it --restart=Never \
  --image=ghcr.io/fiestatools/workflowfiesta-runner-k8s:latest \
  -- register --code RNR-xxxxxxx-xxxxxxx-xxx

# 2. Store the token and runner ID it printed
kubectl apply -f namespace.yaml
kubectl -n workflowfiesta create secret generic wf-runner-creds \
  --from-literal=token=<TOKEN> \
  --from-literal=runner-id=<RUNNER_ID>

# 3. Deploy
kubectl apply -f serviceaccount.yaml
kubectl apply -f rbac.yaml
kubectl apply -f deployment.yaml

# 4. Confirm — the runner should flip from pending to online in the UI
kubectl -n workflowfiesta logs deploy/workflowfiesta-runner -f
```

For a self-hosted WorkflowFiesta instance, set `WORKFLOWFIESTA_API_URL` in
`deployment.yaml` to that instance's API URL before applying (and pass
`--api-url` to `register` in step 1).

Pin `deployment.yaml` to a release tag rather than `latest` for anything you
care about.

## Build locally

The build context is the **repo root**, because the binary is compiled from
source:

```sh
docker build -f deploy/k8s/Dockerfile -t workflowfiesta-runner-k8s .

# with a version stamped into `workflowfiesta-runner version`
docker build -f deploy/k8s/Dockerfile --build-arg VERSION=v0.11.9 \
  -t workflowfiesta-runner-k8s .
```

Published images are built by `.github/workflows/publish-k8s-image.yml` on
release, to `ghcr.io/fiestatools/workflowfiesta-runner-k8s`.

## Configuration

Set on the Deployment's container; see the root README for the full list.

| Variable | Default here | Notes |
|---|---|---|
| `WORKFLOWFIESTA_API_URL` | `https://api.workflowfiesta.com` | Change for self-hosted |
| `WORKFLOWFIESTA_TOKEN` | from `wf-runner-creds` | Required |
| `WORKFLOWFIESTA_RUNNER_ID` | from `wf-runner-creds` | Required |
| `WORKFLOWFIESTA_RUNNER_NAME` | `k8s-runner` | Shown in the UI |
| `WORKFLOWFIESTA_LABELS` | `linux,x86_64,kubernetes` | Baked into the image; override for job routing |
| `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS` | `4` | Concurrent script Jobs |
| `CONTAINER_RUNTIME` | `kubernetes` | Set `local` to run scripts in the runner pod itself |
| `KUBERNETES_NAMESPACE` | own namespace (fieldRef) | Where script Jobs are created |
| `KUBERNETES_IMAGE_PULL_SECRET` | unset | Needed if Environments use private images |

## Notes

- **Environment images must be pullable from your cluster.** A step's
  Environment `dockerImage` is used verbatim as the Job's image, so either
  point your Environments at images your cluster can reach or set
  `KUBERNETES_IMAGE_PULL_SECRET`.
- **One replica only.** Two pods sharing a runner ID would both claim jobs and
  overwrite each other's heartbeat, hence `replicas: 1` and
  `strategy: Recreate`.
- **No probes.** The runner only makes outbound calls and exposes no listener;
  liveness is observable through the 30s heartbeat in the UI.
- **RBAC is namespace-scoped.** Script Jobs run under the namespace's `default`
  ServiceAccount, so user scripts do not inherit the runner's Job-creation
  permissions.
