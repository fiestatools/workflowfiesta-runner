//go:build linux

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"workflowfiesta-runner/internal/localconfig"
)

// runWithSandbox runs the script with optional Landlock filesystem isolation
// and optional network namespace restriction on Linux.
func runWithSandbox(ctx context.Context, cfg *localconfig.LocalConfig, script string, env []string, dir string, outputChan chan<- string) (int, error) {
	if cfg.Sandbox != "kernel" {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
		cmd.Env = env
		cmd.Dir = dir
		return execAndStream(ctx, cmd, outputChan)
	}

	// Kernel sandbox: apply Landlock in a goroutine with a locked OS thread.
	// The locked thread is destroyed when the goroutine exits, preventing the
	// restricted thread from being reused by the Go runtime.
	type result struct {
		code int
		err  error
	}
	resultCh := make(chan result, 1)

	go func() {
		// Lock this goroutine to its OS thread.  When this goroutine returns,
		// Go destroys the OS thread rather than returning it to the pool,
		// so the Landlock restrictions do not contaminate other goroutines.
		runtime.LockOSThread()
		// Intentionally do NOT call runtime.UnlockOSThread().

		if err := applyLandlock(cfg); err != nil {
			resultCh <- result{-1, err}
			return
		}

		var cmd *exec.Cmd
		if cfg.Network == "none" {
			cmd = buildUnsharedNetCmd(ctx, script)
		} else {
			cmd = exec.CommandContext(ctx, "/bin/sh", "-c", script)
		}
		cmd.Env = env
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		code, err := execAndStream(ctx, cmd, outputChan)
		resultCh <- result{code, err}
	}()

	res := <-resultCh
	return res.code, res.err
}

// landlockCreateRuleset calls the landlock_create_ruleset syscall.
// Pass attr=nil and flags=LANDLOCK_CREATE_RULESET_VERSION to probe the ABI version.
func landlockCreateRuleset(attr *unix.LandlockRulesetAttr, flags uint32) (int, error) {
	var attrPtr uintptr
	var attrSize uintptr
	if attr != nil {
		attrPtr = uintptr(unsafe.Pointer(attr))
		attrSize = unsafe.Sizeof(*attr)
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, attrPtr, attrSize, uintptr(flags))
	if errno != 0 {
		return 0, errno
	}
	return int(fd), nil
}

// landlockAddPathBeneathRule calls the landlock_add_rule syscall for path-beneath rules.
func landlockAddPathBeneathRule(rulesetFd int, attr *unix.LandlockPathBeneathAttr) error {
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFd),
		unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(attr)),
		0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// landlockRestrictSelf calls the landlock_restrict_self syscall.
func landlockRestrictSelf(rulesetFd int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFd), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// applyLandlock restricts the calling thread to the allowed paths via Landlock.
func applyLandlock(cfg *localconfig.LocalConfig) error {
	// Probe for Landlock ABI version (requires kernel ≥ 5.13).
	abiVersion, err := landlockCreateRuleset(nil, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if err != nil {
		log.Warnf("[local] Landlock not available (kernel < 5.13?): %v — running without kernel-level isolation", err)
		return nil
	}
	if abiVersion < 1 {
		log.Warnf("[local] Landlock ABI version %d is too old — running without kernel-level isolation", abiVersion)
		return nil
	}

	// Access rights handled by this ruleset.
	const rwAccess = unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM

	const roAccess = unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR

	rulesetAttr := unix.LandlockRulesetAttr{Access_fs: rwAccess}
	rulesetFd, err := landlockCreateRuleset(&rulesetAttr, 0)
	if err != nil {
		return fmt.Errorf("LandlockCreateRuleset: %w", err)
	}
	defer unix.Close(rulesetFd)

	// Allow read-only access to essential system paths.
	systemPaths := []string{"/usr", "/bin", "/lib", "/lib64", "/etc", "/proc", "/dev/null", "/dev/urandom", "/tmp"}
	for _, p := range systemPaths {
		if err := addLandlockPath(rulesetFd, p, roAccess); err != nil {
			log.Warnf("[local] Landlock: skipping system path %s: %v", p, err)
		}
	}

	// Allow configured paths.
	for _, rawPath := range cfg.AllowedPaths {
		isRO := strings.HasSuffix(rawPath, ":ro")
		path := strings.TrimSuffix(rawPath, ":ro")
		path = expandTildeLocal(path)

		access := uint64(rwAccess)
		if isRO {
			access = roAccess
		}
		if err := addLandlockPath(rulesetFd, path, access); err != nil {
			log.Warnf("[local] Landlock: skipping configured path %s: %v", path, err)
		}
	}

	if err := landlockRestrictSelf(rulesetFd); err != nil {
		return fmt.Errorf("LandlockRestrictSelf: %w", err)
	}

	log.Debugf("[local] Landlock ruleset applied (ABI v%d)", abiVersion)
	return nil
}

func addLandlockPath(rulesetFd int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // path doesn't exist; silently skip
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)

	attr := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}
	return landlockAddPathBeneathRule(rulesetFd, &attr)
}

// buildUnsharedNetCmd wraps the script in `unshare -n` to isolate the network namespace.
func buildUnsharedNetCmd(ctx context.Context, script string) *exec.Cmd {
	// Try unshare (requires user namespaces, available on most modern Linux).
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		log.Warn("[local] 'unshare' not found — running with network access despite network: none")
		return exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}
	return exec.CommandContext(ctx, unshare, "-n", "/bin/sh", "-c", script)
}

func expandTildeLocal(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}
