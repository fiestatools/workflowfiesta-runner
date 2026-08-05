//go:build !windows

package installer

import "context"

// installGitBash is a no-op on non-Windows platforms
func installGitBash(_ context.Context, _ func(string)) error {
	return nil
}
