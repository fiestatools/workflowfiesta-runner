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
  3. Create batch/v1 Job
  4. Watch Pods with label selector job-name=<jobName>
  5. Wait for Pod to reach Running / Succeeded / Failed phase
  6. Stream Pod logs (follow=true)
  7. Poll for container Terminated status (up to 60 s)
  8. Return (exitCode, error)
  9. Delete Job with foreground propagation (deferred)
```

### 1. Kubernetes Client

`newK8sClient` tries in-cluster config first (`rest.InClusterConfig()`), then falls back to `KUBECONFIG` via the default client config loading rules. This means the executor works both inside a cluster (with a ServiceAccount) and outside (with a developer kubeconfig).

### 2. Job Name

```
wf-<6 random hex digits>
```

The name is sanitized: lowercased, non-alphanumeric characters replaced with `-`, truncated to 63 characters. This satisfies DNS label requirements.

### 3. Job Spec

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: wf-<suffix>
  namespace: <KUBERNETES_NAMESPACE>
  labels:
    app.kubernetes.io/managed-by: workflowfiesta-runner
spec:
  backoffLimit: 0          # no retries
  ttlSecondsAfterFinished: 300  # auto-cleanup after 5 min (belt-and-suspenders)
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: runner
          image: <input.Image>
          command: ["/bin/sh", "-c", "<input.Script>"]
          env: <input.EnvVars>
      imagePullSecrets:    # added only when KUBERNETES_IMAGE_PULL_SECRET is set
        - name: <secret>
```

Resource limits are not set at the Job level; apply a `LimitRange` to the namespace if you need them.

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
