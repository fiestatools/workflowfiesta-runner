package localui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// headlessApproval prints the job details to stdout and reads a y/n response
// with an auto-deny timeout. Used when Headless=true or in nolocalui builds.
func headlessApproval(req ApprovalRequest) ApprovalResult {
	fmt.Printf("\n╔══════════════════════════════════════════╗\n")
	fmt.Printf("║   WorkflowFiesta · Job Request           ║\n")
	fmt.Printf("╚══════════════════════════════════════════╝\n")
	if req.RunnerName != "" {
		fmt.Printf("Runner : %s\n", req.RunnerName)
	}
	fmt.Printf("Job ID : %s\n", req.JobID)
	fmt.Printf("Script :\n%s\n", truncateScript(req.Script, 15))
	neverTimeout := req.Timeout <= 0
	if neverTimeout {
		fmt.Printf("\nAllow? [y/N] (waiting indefinitely): ")
	} else {
		fmt.Printf("\nAllow? [y/N] (auto-deny in %ds): ", int(req.Timeout.Seconds()))
	}

	resultCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			resultCh <- scanner.Text()
		} else {
			resultCh <- ""
		}
	}()

	if neverTimeout {
		response := <-resultCh
		approved := strings.ToLower(strings.TrimSpace(response)) == "y"
		if approved {
			fmt.Println("[allowed]")
			return ApprovalAllow
		}
		fmt.Println("[denied]")
		return ApprovalDeny
	}

	select {
	case response := <-resultCh:
		approved := strings.ToLower(strings.TrimSpace(response)) == "y"
		if approved {
			fmt.Println("[allowed]")
			return ApprovalAllow
		}
		fmt.Println("[denied]")
		return ApprovalDeny
	case <-time.After(req.Timeout):
		fmt.Println("\n[auto-denied: timeout]")
		return ApprovalDeny
	}
}

// truncateScript returns the first n lines of script, adding a note if truncated.
func truncateScript(script string, maxLines int) string {
	lines := strings.Split(script, "\n")
	if len(lines) <= maxLines {
		return script
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}
