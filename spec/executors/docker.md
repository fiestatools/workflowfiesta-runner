# Docker Executor

The Docker executor (`internal/executor/docker.go`) runs each job script inside a fresh, isolated Docker container using the Docker Engine API.

---

## Activation

The Docker executor is selected when:
- `CONTAINER_RUNTIME=docker` (explicit), or
- `CONTAINER_RUNTIME` is unset, `KUBERNETES_SERVICE_HOST` is not set, and `GOOS != "windows"` (default on Linux/macOS).

---

## Execution Flow

```
Execute(ctx, input)
  1. Connect to Docker Engine via unix://<DockerSocket>
  2. Pull image
  3. Create container
  4. Start container
  5. Attach log stream (follow=true)
  6. Stream output → input.OutputChan (goroutine)
  7. Wait for container exit (with timeout)
  8. Close log stream, drain goroutine
  9. Remove container
 10. Return (exitCode, error)
```

### 1. Docker Client

A new `dockerclient.Client` is created per job using `dockerclient.WithAPIVersionNegotiation()` so the runner works with Docker Engine versions that differ from the compiled client library version.

Socket path: `unix://<cfg.DockerSocket>` (default `/var/run/docker.sock`).

### 2. Image Pull

```go
cli.ImagePull(ctx, input.Image, image.PullOptions{})
```

The pull output is read and discarded (`io.Copy(io.Discard, reader)`). If the pull fails (image not found, auth error, network error), `Execute` returns immediately with an error.

### 3. Container Configuration

```go
container.Config{
    Image: input.Image,
    Cmd:   []string{"/bin/bash", "-c", input.Script},
    Env:   []string{"KEY=VALUE", ...},
}
container.HostConfig{
    Resources: container.Resources{
        Memory:    512 * 1024 * 1024,  // 512 MB
        CPUShares: 512,                // relative weight (1024 = 1 full CPU)
    },
}
```

Resource limits:
- **Memory:** 512 MB hard limit.
- **CPU shares:** 512 (half a CPU share unit; soft limit only).

No volumes are mounted. No network configuration is specified, so the container uses Docker's default bridge network.

### 4. Log Streaming

`ContainerLogs` with `Follow: true` is called after the container starts. Output is streamed in a goroutine in 4 KB chunks. The Docker multiplexed stream header (8 bytes) is stripped from each chunk before sending to `OutputChan`.

The goroutine is synchronized before `Execute` returns: the log stream is explicitly closed, and the goroutine is waited on via `<-streamDone`. This prevents a panic from writing to a closed `OutputChan`.

### 5. Wait and Timeout

`ContainerWait` is called with a timeout context derived from `input.Timeout`. The first of three cases fires:
- `statusCh`: normal exit, records `exitCode`.
- `errCh`: engine error, records `execErr`.
- `timeoutCtx.Done()`: calls `ContainerStop` and returns a timeout error.

### 6. Cleanup

`ContainerRemove(Force: true)` is deferred, ensuring the container is removed regardless of success, failure, or timeout.

---

## Error Handling

| Condition | Behavior |
|---|---|
| Image pull fails | Return `(-1, "image pull: <err>")` |
| Container create fails | Return `(-1, "container create: <err>")` |
| Container start fails | Return `(-1, "container start: <err>")` |
| Script exits non-zero | Return `(exitCode, nil)` — non-zero is not an executor error |
| Timeout | Stop container, return `(-1, "timeout after <duration>")` |

---

## Requirements

- Docker Engine must be running and accessible at `DOCKER_SOCKET`.
- If running the runner inside Docker itself, mount the socket: `-v /var/run/docker.sock:/var/run/docker.sock`.
- The runner process must have permission to access the socket (typically `docker` group membership or root).
