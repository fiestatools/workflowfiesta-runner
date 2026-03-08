//go:build !nolocalui && linux

package localui

func isNonLinux() bool { return false }

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
