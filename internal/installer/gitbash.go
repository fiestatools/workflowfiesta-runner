package installer

import "context"

func InstallGitBash(ctx context.Context, emit func(string)) error {
	return installGitBash(ctx, emit)
}
