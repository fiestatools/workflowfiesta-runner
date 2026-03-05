package executor

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/config"
)

type dockerExecutor struct {
	cfg *config.Config
}

func (d *dockerExecutor) Execute(ctx context.Context, input Input) (int, error) {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("unix://"+d.cfg.DockerSocket),
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return -1, fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	// Pull image
	log.Infof("Pulling image %s", input.Image)
	reader, err := cli.ImagePull(ctx, input.Image, image.PullOptions{})
	if err != nil {
		return -1, fmt.Errorf("image pull: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	// Build env
	env := make([]string, 0, len(input.EnvVars))
	for k, v := range input.EnvVars {
		env = append(env, k+"="+v)
	}

	// Create container
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: input.Image,
			Cmd:   []string{"/bin/sh", "-c", input.Script},
			Env:   env,
		},
		&container.HostConfig{
			Resources: container.Resources{
				Memory:    512 * 1024 * 1024,
				CPUShares: 512,
			},
		},
		nil, nil, "",
	)
	if err != nil {
		return -1, fmt.Errorf("container create: %w", err)
	}

	defer cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return -1, fmt.Errorf("container start: %w", err)
	}

	// Attach for logs
	out, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return -1, fmt.Errorf("container logs: %w", err)
	}
	defer out.Close()

	// Stream output in goroutine
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			if n > 0 && input.OutputChan != nil {
				data := buf[:n]
				if len(data) > 8 {
					data = data[8:]
				}
				input.OutputChan <- string(data)
			}
			if err != nil {
				break
			}
		}
	}()

	timeoutCtx, cancel := context.WithTimeout(ctx, input.Timeout)
	defer cancel()

	statusCh, errCh := cli.ContainerWait(timeoutCtx, resp.ID, container.WaitConditionNotRunning)
	select {
	case status := <-statusCh:
		return int(status.StatusCode), nil
	case err := <-errCh:
		return -1, fmt.Errorf("container wait: %w", err)
	case <-timeoutCtx.Done():
		cli.ContainerStop(context.Background(), resp.ID, container.StopOptions{})
		return -1, fmt.Errorf("timeout after %v", input.Timeout)
	}
}
