package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrAuthRevoked indicates the runner token is no longer accepted (deleted or
// reset on the server). The runner should clear local credentials and re-register.
var ErrAuthRevoked = errors.New("runner authentication revoked")

// AuthRevokedStatus reports whether an HTTP status means the runner should log out.
// 401 and 403 are definitive auth failures. 404 is only treated as auth-revoked
// when the body contains "runner_not_found" — a deliberate server signal that the
// runner was deleted. Generic 404s (transient blips, rolling deploys) are not.
func AuthRevokedStatus(status int) bool {
	switch status {
	case 401, 403:
		return true
	default:
		return false
	}
}

// IsRunnerNotFound reports whether a 404 response body signals a deleted runner.
func IsRunnerNotFound(body []byte) bool {
	return strings.Contains(string(body), "runner_not_found")
}

// ShouldRevokeAuth reports whether the runner should log out.
// 401/403 always revoke; 404 only when the body signals runner_not_found.
func ShouldRevokeAuth(status int, body []byte) bool {
	if AuthRevokedStatus(status) {
		return true
	}
	return status == http.StatusNotFound && IsRunnerNotFound(body)
}

// AuthRevokedError builds ErrAuthRevoked with context from the API response.
func AuthRevokedError(status int, path string, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	if msg == "" {
		return fmt.Errorf("%w: HTTP %d from %s", ErrAuthRevoked, status, path)
	}
	return fmt.Errorf("%w: HTTP %d from %s: %s", ErrAuthRevoked, status, path, msg)
}

// IsAuthRevoked reports whether err (or a wrapped error) is ErrAuthRevoked.
func IsAuthRevoked(err error) bool {
	return errors.Is(err, ErrAuthRevoked)
}
