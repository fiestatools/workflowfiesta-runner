package updater

import (
	"context"
	"fmt"
	"io"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
	"github.com/schollz/progressbar/v3"
)

// progressSource wraps a selfupdate.Source and injects a progress callback
// into the binary asset download. It satisfies the selfupdate.Source interface
// and delegates everything to the inner source unchanged — except
// DownloadReleaseAsset for the main binary, where it wraps the returned
// io.ReadCloser in a progressReadCloser.
//
// Validation assets (checksums.txt) are passed through unwrapped because they
// are tiny and tracking them would produce misleading flicker in the progress display.
type progressSource struct {
	inner      selfupdate.Source
	onProgress func(n int, downloaded, total int64)
}

func (p *progressSource) ListReleases(ctx context.Context, repo selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	return p.inner.ListReleases(ctx, repo)
}

func (p *progressSource) DownloadReleaseAsset(ctx context.Context, rel *selfupdate.Release, assetID int64) (io.ReadCloser, error) {
	rc, err := p.inner.DownloadReleaseAsset(ctx, rel, assetID)
	if err != nil {
		return nil, err
	}
	// Only wrap the main binary download. Validation assets (checksums.txt)
	// use a different assetID and are small enough not to need progress.
	if assetID != rel.AssetID || p.onProgress == nil {
		return rc, nil
	}
	return &progressReadCloser{
		ReadCloser: rc,
		total:      int64(rel.AssetByteSize),
		onProgress: p.onProgress,
	}, nil
}

// progressReadCloser wraps an io.ReadCloser and fires onProgress after every
// Read call with the number of bytes just read, the cumulative total, and the
// expected total size.
type progressReadCloser struct {
	io.ReadCloser
	total      int64
	downloaded int64
	onProgress func(n int, downloaded, total int64)
}

func (p *progressReadCloser) Read(buf []byte) (int, error) {
	n, err := p.ReadCloser.Read(buf)
	if n > 0 {
		p.downloaded += int64(n)
		p.onProgress(n, p.downloaded, p.total)
	}
	return n, err
}

// newProgressCallback returns an onProgress function suited to the build type.
//
// CLI / headless: renders a live ANSI progress bar to stdout via progressbar.
// GUI: emits a log line at each 10% boundary via logFn. Fyne's text widget
// cannot render ANSI escape sequences, so a bar would appear as garbage.
func newProgressCallback(total int64, isGUI bool, logFn func(string)) func(n int, downloaded, total int64) {
	if !isGUI {
		bar := progressbar.NewOptions64(total,
			progressbar.OptionSetDescription("[updater] downloading"),
			progressbar.OptionShowBytes(true),
			progressbar.OptionShowCount(),
			progressbar.OptionSetWidth(40),
			progressbar.OptionThrottle(100*time.Millisecond),
			progressbar.OptionOnCompletion(func() { fmt.Println() }),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "=",
				SaucerHead:    ">",
				SaucerPadding: " ",
				BarStart:      "[",
				BarEnd:        "]",
			}),
		)
		return func(n int, _, _ int64) {
			_ = bar.Add(n)
		}
	}

	// GUI: one log line per 10% so the status window shows meaningful progress
	// without being spammed on every Read call.
	var lastPct int
	return func(_ int, downloaded, total int64) {
		if total <= 0 {
			return
		}
		pct := int(float64(downloaded) / float64(total) * 100)
		if pct/10 > lastPct/10 {
			lastPct = pct
			logFn(fmt.Sprintf("[updater] downloading... %d%%", pct))
		}
	}
}
