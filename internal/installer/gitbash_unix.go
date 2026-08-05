//go:build !windows

package installer

import "context"

// installGitBash is a no-op on non-Windows platforms — bash and coreutils
// are already part of the base system there.
func installGitBash(_ context.Context, _ func(string)) error {
	return nil
}
