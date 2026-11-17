# Kubernetes Executor

The Kubernetes executor (`internal/executor/kubernetes.go`) runs each job script as a Kubernetes `batch/v1` Job, streams logs from the resulting Pod, and cleans up the Job when execution completes.

---

## Activation

The Kubernetes executor is selected when:
- `CONTAINER_RUNTIME=kubernetes` (explicit), or
- `KUBERNETES_SERVICE_HOST` is set in the environment (auto-detected in-cluster).

---

## Execution Flow

```
Execute(ctx, input)
  1. Build Kubernetes client (in-cluster or KUBECONFIG)
  2. Generate a unique Job name (wf-<6-hex-chars>)
  3. Resolve the container image. An explicit `dockerImage` is kept.
     An empty image uses `KUBERNETES_DEFAULT_IMAGE`, then
     `public.ecr.aws/docker/library/debian:bookworm-slim`.
  4. Create batch/v1 Job
  5. Watch Pods with label selector job-name=<jobName>
  6. Wait for Pod to reach Running / Succeeded / Failed phase
  7. Stream Pod logs (follow=true)
  8. Poll for container Terminated status (up to 60 s)
  9. Return (exitCode, error)
  10. Delete Job with foreground propagation (deferred)
```

### 1. Kubernetes Client

`newK8sClient` tries in-cluster config first (`rest.InClusterConfig()`), then falls back to `KUBECONFIG` via the default client config loading rules. This means the executor works both inside a cluster (with a ServiceAccount) and outside (with a developer kubeconfig).

### 2. Job Name

```
wf-<6 random hex digits>
```

The name is sanitized: lowercased, non-alphanumeric characters replaced with `-`, truncated to 63 characters. This satisfies DNS label requirements.

### 3. Job Spec

Before anything is created, the image is checked against
`KUBERNETES_JOB_ALLOWED_IMAGES` / `KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST`, and the
server-requested timeout is capped by `KUBERNETES_JOB_MAX_TIMEOUT_SECONDS`.
A rejected image fails the job without touching the cluster.

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: wf-<suffix>
  namespace: <KUBERNETES_NAMESPACE>
  labels:                                   # same labels on the pod template
    app.kubernetes.io/managed-by: workflowfiesta-runner
    app.kubernetes.io/component: script-job
    <KUBERNETES_JOB_LABELS>
  annotations:
    workflowfiesta.com/job-id: <job id>
    <KUBERNETES_JOB_ANNOTATIONS>
spec:
  backoffLimit: 0                 # no retries
  ttlSecondsAfterFinished: 300    # auto-cleanup after 5 min
  activeDeadlineSeconds: <timeout + 60>   # kube kills the pod even if the runner dies
  template:
    metadata:
      labels: <as above>
      annotations: <as above>
    spec:
      restartPolicy: Never
      serviceAccountName: <KUBERNETES_JOB_SERVICE_ACCOUNT>     # omitted = namespace default
      automountServiceAccountToken: false                       # unless ..._AUTOMOUNT_SERVICE_ACCOUNT_TOKEN=true
      enableServiceLinks: <true; false with profile restricted>
      nodeSelector: <KUBERNETES_JOB_NODE_SELECTOR>
      tolerations: <KUBERNETES_JOB_TOLERATIONS>
      runtimeClassName: <KUBERNETES_JOB_RUNTIME_CLASS>
      securityContext:            # profile restricted only
        runAsNonRoot: true
        runAsUser/runAsGroup/fsGroup: <KUBERNETES_JOB_RUN_AS_USER, default 65532>
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: runner
          image: <input.Image>
          command: ["/bin/sh", "-c", "<input.Script>"]
          env: <input.EnvVars, sorted> (+ HOME=/tmp when the root fs is read-only)
          resources: <KUBERNETES_JOB_{CPU,MEMORY,EPHEMERAL_STORAGE}_{REQUEST,LIMIT}>
          securityContext:        # profile restricted
            allowPrivilegeEscalation: false
            capabilities: {drop: [ALL]}
            readOnlyRootFilesystem: true   # with emptyDir /tmp and /workspace
      imagePullSecrets:           # only when KUBERNETES_IMAGE_PULL_SECRET is set
        - name: <secret>
```

With `KUBERNETES_JOB_SECURITY_PROFILE=restricted` the pod passes the Pod
Security Standards `restricted` level, so the Job namespace can carry
`pod-security.kubernetes.io/enforce: restricted`. Scripts then run as a
non-root UID and cannot `apt-get install`; use an Environment image that
already contains the tools the scripts need.

### 4. Pod Watching

`client.CoreV1().Pods(namespace).Watch` with label selector `job-name=<jobName>` watches for Pod events. Execution continues when the Pod reaches `Running`, `Succeeded`, or `Failed` phase. The watch has a hard 5-minute timeout.

### 5. Log Streaming

`GetLogs` with `Follow: true` streams the Pod logs in 4 KB chunks to `input.OutputChan`. Streaming ends when the Pod's log stream closes (i.e., the container exits).

### 6. Exit Code Retrieval

After log streaming, `getPodExitCode` polls `Pod.Status.ContainerStatuses[0].State.Terminated.ExitCode` at 2-second intervals for up to 60 seconds. If the container does not reach `Terminated` within that window, an error is returned.

### 7. Cleanup

A deferred call deletes the Job with `PropagationPolicy: Foreground`, which waits for dependent Pods to be deleted. The `TTLSecondsAfterFinished: 300` on the Job spec provides a backup cleanup mechanism if the runner process is killed before cleanup runs.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `KUBERNETES_NAMESPACE` | `default` | Namespace for Job creation |
| `KUBERNETES_IMAGE_PULL_SECRET` | *(empty)* | imagePullSecrets name; omitted if not set |
| `KUBERNETES_JOB_SERVICE_ACCOUNT` | *(empty)* | serviceAccountName for script pods; empty = namespace `default` |
| `KUBERNETES_JOB_AUTOMOUNT_SERVICE_ACCOUNT_TOKEN` | `false` | Mount the SA token into script pods |
| `KUBERNETES_JOB_LABELS` / `_ANNOTATIONS` | *(empty)* | `k=v,k2=v2`, added to the Job and pod template; cannot override the managed-by/component labels |
| `KUBERNETES_JOB_NODE_SELECTOR` | *(empty)* | `k=v,k2=v2` |
| `KUBERNETES_JOB_TOLERATIONS` | *(empty)* | JSON array of core/v1 Toleration |
| `KUBERNETES_JOB_RUNTIME_CLASS` | *(empty)* | e.g. `gvisor` or `kata` for a sandboxed runtime |
| `KUBERNETES_JOB_{CPU,MEMORY,EPHEMERAL_STORAGE}_{REQUEST,LIMIT}` | *(empty)* | Kubernetes quantities, validated at startup |
| `KUBERNETES_JOB_SECURITY_PROFILE` | `none` | `restricted` = PSS restricted pod/container securityContext |
| `KUBERNETES_JOB_RUN_AS_USER` | `65532` | UID for the restricted profile (must be > 0) |
| `KUBERNETES_JOB_READ_ONLY_ROOT_FILESYSTEM` | `false` (`true` when restricted) | Adds emptyDir `/tmp` and `/workspace`, sets `HOME=/tmp` |
| `KUBERNETES_JOB_ENABLE_SERVICE_LINKS` | `true` (`false` when restricted) | Inject `*_SERVICE_HOST` env for every Service in the namespace |
| `KUBERNETES_JOB_MAX_TIMEOUT_SECONDS` | `0` (no cap) | Upper bound on the server-requested job timeout |
| `KUBERNETES_JOB_ALLOWED_IMAGES` | *(empty = any)* | Comma-separated image prefixes; others fail before a Job is created |
| `KUBERNETES_JOB_REQUIRE_IMAGE_DIGEST` | `false` | Reject images that are not pinned with `@sha256:` |

Invalid values stop `workflowfiesta-runner run` at startup.

---

## Required RBAC

The ServiceAccount used by the runner Pod needs the following permissions:

```yaml
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "delete", "get"]
  - apiGroups: [""]
    resources: ["pods", "pods/log"]
    verbs: ["list", "watch", "get"]
```

---

## Error Handling

| Condition | Behavior |
|---|---|
| k8s client build fails | Return `(-1, "k8s config: <err>")` |
| Job create fails | Return `(-1, "create job: <err>")` |
| Pod does not start in 5 min | Return `(-1, "pod did not start within 5 minutes")` |
| Log stream error | Log warning; continue to exit code retrieval |
| Pod never terminates (60 s poll) | Return `(-1, "pod did not reach terminated state")` |
| Script exits non-zero | Return `(exitCode, nil)` |
| Context timeout | Returns context error |
