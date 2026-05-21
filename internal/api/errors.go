package api

import (
	"errors"
	"fmt"
	"strings"
)

// ErrAuthRevoked indicates the runner token is no longer accepted (deleted or
// reset on the server). The runner should clear local credentials and re-register.
var ErrAuthRevoked = errors.New("runner authentication revoked")

// AuthRevokedStatus reports whether an HTTP status means the runner should log out.
func AuthRevokedStatus(status int) bool {
	switch status {
	case 401, 403, 404:
		return true
	default:
		return false
	}
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
