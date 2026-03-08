//go:build darwin

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/localconfig"
)

// runWithSandbox on macOS wraps the script with sandbox-exec and a generated
// Seatbelt profile when sandbox: kernel is configured.
func runWithSandbox(ctx context.Context, cfg *localconfig.LocalConfig, script string, env []string, dir string, outputChan chan<- string) (int, error) {
	if cfg.Sandbox != "kernel" {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
		cmd.Env = env
		cmd.Dir = dir
		return execAndStream(ctx, cmd, outputChan)
	}

	profile, err := buildSeatbeltProfile(cfg)
	if err != nil {
		return -1, fmt.Errorf("build sandbox profile: %w", err)
	}

	sandboxExec, lookErr := exec.LookPath("sandbox-exec")
	if lookErr != nil {
		log.Warn("[local] sandbox-exec not found — running without macOS kernel sandbox")
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
		cmd.Env = env
		cmd.Dir = dir
		return execAndStream(ctx, cmd, outputChan)
	}

	cmd := exec.CommandContext(ctx, sandboxExec, "-p", profile, "/bin/sh", "-c", script)
	cmd.Env = env
	cmd.Dir = dir
	return execAndStream(ctx, cmd, outputChan)
}

// buildSeatbeltProfile generates a macOS Seatbelt (sandbox-exec) TinyScheme profile
// from the LocalConfig allowed paths and network settings.
func buildSeatbeltProfile(cfg *localconfig.LocalConfig) (string, error) {
	var sb strings.Builder

	sb.WriteString("(version 1)\n")
	sb.WriteString("(deny default)\n\n")

	// Allow reading essential system paths.
	systemReadPaths := []string{"/usr", "/bin", "/sbin", "/lib", "/System", "/Library", "/private/etc", "/dev"}
	for _, p := range systemReadPaths {
		fmt.Fprintf(&sb, "(allow file-read* (subpath %q))\n", p)
	}
	sb.WriteString("(allow file-read* (literal \"/\"))\n")
	sb.WriteString("(allow process-exec)\n")
	sb.WriteString("(allow process-fork)\n")
	sb.WriteString("(allow signal)\n\n")

	// Allow configured user paths.
	for _, rawPath := range cfg.AllowedPaths {
		isRO := strings.HasSuffix(rawPath, ":ro")
		path := strings.TrimSuffix(rawPath, ":ro")
		path = expandTildeDarwin(path)

		if isRO {
			fmt.Fprintf(&sb, "(allow file-read* (subpath %q))\n", path)
		} else {
			fmt.Fprintf(&sb, "(allow file-read* file-write* (subpath %q))\n", path)
		}
	}
	sb.WriteString("\n")

	// Network rules.
	switch cfg.Network {
	case "none":
		sb.WriteString("(deny network*)\n")
	case "localhost":
		sb.WriteString("(allow network-bind (local ip))\n")
		sb.WriteString("(allow network-inbound (local ip))\n")
		sb.WriteString("(deny network-outbound (remote ip \"*:*\"))\n")
		sb.WriteString("(allow network-outbound (remote ip \"localhost:*\"))\n")
	default:
		sb.WriteString("(allow network*)\n")
	}

	return sb.String(), nil
}

func expandTildeDarwin(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
