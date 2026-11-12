# Kubernetes deployment

Run the headless WorkflowFiesta runner in your own Kubernetes cluster, so
workflow script steps execute on your infrastructure.

**You need exactly one value to get running: a runner token.** Everything else
has a working default.

In Kubernetes mode the runner:

1. Polls your WorkflowFiesta API for pending jobs (every 3s) and heartbeats
   (every 30s).
2. Creates a one-shot `batch/Job` per script step, using the Docker image from
   the step's Environment.
3. Streams the pod's logs back to the API in real time.
4. Deletes the Job once it exits (a 5-minute TTL cleans up anything orphaned).

No Docker socket is mounted and no privileged container is required.

---

## Quickstart

### 1. Get a token

In WorkflowFiesta: **Runners → "Set up a runner"**. Name the runner there — that
name is what shows in the UI — and copy the `RNR-…` code.

Exchange the code for a token. The code is single-use. Easiest from any machine
with the binary:

```sh
workflowfiesta-runner register --code RNR-xxxxxxx-xxxxxxx-xxx
```

Or without installing anything, using this image as a one-shot pod:

```sh
kubectl run wf-register --rm -it --restart=Never \
  --image=ghcr.io/fiestatools/workflowfiesta-runner-k8s:v0.11.9-k8s \
  -- register --code RNR-xxxxxxx-xxxxxxx-xxx
```

Self-hosted instances need `--api-url https://api.your-company.com` as well.

It prints several `export` lines. **You only need `WORKFLOWFIESTA_TOKEN`** —
see [What the runner actually reads](#what-the-runner-actually-reads) for why
the others don't matter here. The token is shown once.

### 2. Deploy

```sh
kubectl apply -f namespace.yaml

# Hosted WorkflowFiesta: apply as-is.
# Self-hosted: edit WORKFLOWFIESTA_API_URL first.
kubectl apply -f configmap.yaml

kubectl -n workflowfiesta create secret generic workflowfiesta-runner-token \
  --from-literal=WORKFLOWFIESTA_TOKEN=<TOKEN>

kubectl apply -f serviceaccount.yaml
kubectl apply -f rbac.yaml
kubectl apply -f deployment.yaml
```

For GitOps, copy `secret.example.yaml` instead of the imperative command and
keep it encrypted (sealed-secrets, SOPS, External Secrets). Don't commit a
filled-in copy.

### 3. Verify

```sh
kubectl -n workflowfiesta logs deploy/workflowfiesta-runner -f
```

A healthy start looks like:

```
Starting WorkflowFiesta runner (version v0.11.9)
API URL: https://api.workflowfiesta.com
Runner name: k8s-runner
Executor type: kubernetes

● Connected  k8s-runner → https://api.workflowfiesta.com
```

The runner then flips from **pending** to **online** in the UI. If it stays
pending, the pod never reached `Connected` — see
[Troubleshooting](#troubleshooting).

End-to-end check: run a workflow with a script step bound to an Environment
assigned to this runner, and watch the Job appear and disappear:

```sh
kubectl -n workflowfiesta get jobs -w
```

---

## What the runner actually reads

Taken from `internal/config/config.go` and `cmd/runner/main.go`. This matters
because `register` prints four variables and only one of them is load-bearing.

### Required

| Variable | Notes |
|---|---|
| `WORKFLOWFIESTA_TOKEN` | The only hard requirement. `run` exits immediately with `WORKFLOWFIESTA_TOKEN is required` if it's empty. It is both the credential and the runner's identity — the API client is constructed with just the API URL and this token, and auth is `Authorization: Bearer <token>`. |

### Effectively required in Kubernetes

| Variable | Default | Notes |
|---|---|---|
| `KUBERNETES_NAMESPACE` | `default` | The namespace script Jobs are created in. The built-in default is wrong for this deployment — `default` is where the runner's ServiceAccount has no RBAC. `deployment.yaml` sets it from a `fieldRef` to the pod's own namespace. Keep it that way. |
| `WORKFLOWFIESTA_API_URL` | `https://api.workflowfiesta.com` | Correct as-is for the hosted service. **Must** be set for self-hosted, or the runner silently polls the wrong instance and never picks up work. |

### Optional

| Variable | Default | Effect |
|---|---|---|
| `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS` | `4` | Max simultaneous script Jobs. Non-numeric or `<= 0` values are ignored and the default stands. |
| `WORKFLOWFIESTA_RUNNER_NAME` | `unnamed-runner` (`k8s-runner` in our ConfigMap) | Local only — it appears in this pod's logs. The name in the UI was fixed when the registration code was issued; changing this does not rename anything. |
| `CONTAINER_RUNTIME` | auto | `kubernetes` / `docker` / `local`. Auto-detects `kubernetes` from `KUBERNETES_SERVICE_HOST`, so it's set explicitly here only for visibility. Set `local` to run scripts inside the runner pod instead of creating Jobs. |
| `KUBERNETES_IMAGE_PULL_SECRET` | unset | Name of a Secret attached as an `imagePullSecrets` entry on each script Job. Needed when your Environments use private images. |
| `LOCAL_CONFIG_PATH` | `~/.workflowfiesta/runner.yaml` | Only read in local-executor mode. |

### Accepted but currently inert

Don't bother setting these; they're listed so you don't waste time on them.

| Variable | Why |
|---|---|
| `WORKFLOWFIESTA_RUNNER_ID` | Parsed into config and never read at runtime. Nothing in the API client or run loop uses it — the token identifies the runner. `register` prints it, and the GUI wizard saves it, but this deployment doesn't need it. |
| `WORKFLOWFIESTA_LABELS` | Parsed into `cfg.Labels`, which nothing reads. The heartbeat sends only status, capabilities, OS, arch and version — labels are not transmitted, so they cannot affect job routing today. |
| `DOCKER_SOCKET` | Docker-executor only; ignored in Kubernetes mode. |

### How configuration is wired

`deployment.yaml` pulls everything via `envFrom`:

- `configmap.yaml` → non-secret settings
- `secret.example.yaml` → `WORKFLOWFIESTA_TOKEN`

Keys in both objects are environment-variable names, so **adding a setting
means editing the ConfigMap, not the Deployment**:

```sh
kubectl -n workflowfiesta patch configmap workflowfiesta-runner-config \
  --type merge -p '{"data":{"WORKFLOWFIESTA_MAX_CONCURRENT_JOBS":"8"}}'
kubectl -n workflowfiesta rollout restart deploy/workflowfiesta-runner
```

Env vars are not re-read while the process runs, so a restart is required.

`KUBERNETES_NAMESPACE` and `CONTAINER_RUNTIME` stay as explicit `env` entries —
the first because a `fieldRef` can't live in a ConfigMap, and explicit `env`
beats `envFrom` on conflict, so neither can be overridden by accident.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Pod `CreateContainerConfigError` | The ConfigMap or Secret doesn't exist | `kubectl -n workflowfiesta get cm,secret` — both `workflowfiesta-runner-config` and `workflowfiesta-runner-token` must be present |
| Pod `CrashLoopBackOff`, log says `WORKFLOWFIESTA_TOKEN is required` | Secret exists but the key is wrong | The key must be exactly `WORKFLOWFIESTA_TOKEN`, not `token` |
| Runner stays **pending** in the UI, pod log shows no `Connected` | Wrong API URL, or an expired/already-used registration code | Check `WORKFLOWFIESTA_API_URL`; re-issue the code — codes are single-use |
| Runner **online**, jobs never start | No Environment is assigned to this runner | Assign it in the Environment's **Self-Hosted Runners** section |
| Job creation fails with `jobs.batch is forbidden` | `KUBERNETES_NAMESPACE` is not the runner's own namespace, or RBAC wasn't applied | Confirm the `fieldRef` is intact and `rbac.yaml` is applied **in the same namespace** |
| Script pods stuck `ImagePullBackOff` | The Environment's `dockerImage` isn't pullable from your cluster | Use a reachable image, or set `KUBERNETES_IMAGE_PULL_SECRET` |
| Two runners fighting / flapping status | More than one replica sharing the token | Keep `replicas: 1` |

Useful commands:

```sh
# resolved env inside the running pod
kubectl -n workflowfiesta exec deploy/workflowfiesta-runner -- env | grep -E 'WORKFLOWFIESTA|KUBERNETES|CONTAINER_RUNTIME'

# confirm RBAC actually permits what the runner needs
kubectl -n workflowfiesta auth can-i create jobs \
  --as=system:serviceaccount:workflowfiesta:workflowfiesta-runner
kubectl -n workflowfiesta auth can-i get pods/log \
  --as=system:serviceaccount:workflowfiesta:workflowfiesta-runner
```

---

## Contents

| Path | Purpose |
|---|---|
| `Dockerfile` | Debian 12 slim + the runner compiled from this repo |
| `namespace.yaml` | `workflowfiesta` namespace |
| `configmap.yaml` | Non-secret settings |
| `secret.example.yaml` | Token template for GitOps |
| `serviceaccount.yaml` | ServiceAccount for the runner pod |
| `rbac.yaml` | Namespace-scoped Role/RoleBinding (jobs + pods + pods/log) |
| `deployment.yaml` | The runner Deployment |

This image differs from the repo-root `Dockerfile` (plain Docker / host use):
it runs as a non-root user (uid 10001), uses tini as PID 1, defaults to
`CONTAINER_RUNTIME=kubernetes`, and ships a shell plus git/jq so local-mode
scripts have something to run.

## Build locally

The build context is the **repo root**, because the binary is compiled from
source:

```sh
docker build -f deploy/k8s/Dockerfile -t workflowfiesta-runner-k8s .

# stamp a version into `workflowfiesta-runner version`
docker build -f deploy/k8s/Dockerfile --build-arg VERSION=v0.11.9 \
  -t workflowfiesta-runner-k8s .
```

Published images are built by `.github/workflows/publish-k8s-image.yml` on
release, to `ghcr.io/fiestatools/workflowfiesta-runner-k8s`.

## Security notes

- **RBAC is namespace-scoped** and matches exactly what the Kubernetes executor
  calls: `batch/jobs` create/delete/get/list/watch, `pods` get/list/watch,
  `pods/log` get. No `ClusterRole`.
- **Script pods don't inherit the runner's permissions.** The executor never
  sets `serviceAccountName` on the Jobs it creates, so they run under the
  namespace's `default` ServiceAccount.
- **The runner pod is non-root** with `allowPrivilegeEscalation: false` and all
  capabilities dropped.
- **Scripts are arbitrary code** from your WorkflowFiesta workflows. They run
  in separate Job pods, but in the same namespace — consider a dedicated
  namespace with a ResourceQuota and a restrictive default NetworkPolicy if
  that namespace has access to anything sensitive.
- **No probes.** The runner only makes outbound calls and exposes no listener,
  so there's nothing for a readiness/liveness probe to hit; health is visible
  via the 30s heartbeat in the UI.
