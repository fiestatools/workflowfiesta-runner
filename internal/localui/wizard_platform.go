//go:build !nolocalui && !linux

package localui

// isNonLinux returns true on macOS, Windows, etc. where a display is always present.
func isNonLinux() bool { return true }

func splitLines(s string) []string {
	var lines []string
	line := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, line)
			line = ""
		} else {
			line += string(c)
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
