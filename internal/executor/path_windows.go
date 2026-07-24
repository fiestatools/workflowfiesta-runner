//go:build windows

package executor

import (
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// sanitizeWindowsPATH removes Windows Store App Execution Alias directories from
// a PATH string. These stubs hang in headless contexts; real interpreters
// installed alongside them expose the same binaries via a non-stub path.
func sanitizeWindowsPATH(raw string) string {
	entries := filepath.SplitList(raw)
	out := entries[:0]
	for _, e := range entries {
		if isWindowsAppsDir(e) {
			log.Debugf("[local] PATH: stripped Windows Store stub dir: %s", e)
		} else {
			out = append(out, e)
		}
	}
	if len(out) < len(entries) {
		log.Warnf("[local] %d Windows Store App Execution Alias path(s) removed from subprocess PATH; "+
			"install a real interpreter if scripts invoke python/node/ruby directly", len(entries)-len(out))
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

func isWindowsAppsDir(dir string) bool {
	lower := strings.ToLower(filepath.Clean(dir))
	return strings.Contains(lower, `microsoft\windowsapps`)
}
