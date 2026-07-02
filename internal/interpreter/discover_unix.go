//go:build !windows

package interpreter

import "os/exec"

func resolve(name string) (string, Status) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", StatusMissing
	}
	return p, StatusFound
}
