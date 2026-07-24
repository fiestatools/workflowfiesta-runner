package interpreter

import "context"

// Status describes the outcome of an interpreter probe.
type Status int

const (
	StatusFound   Status = iota // executable found and responds to version query
	StatusMissing               // not found anywhere on this machine
	StatusStub                  // Windows Store App Execution Alias — skipped
)

// Info holds the result of probing a single interpreter.
type Info struct {
	Name    string // canonical name, e.g. "python3"
	Path    string // absolute path; empty when Status != StatusFound
	Version string // first line of version output; best-effort, may be empty
	Status  Status
}

// Discover scans the host for known interpreter installations.
// All probes run concurrently; ctx controls the overall deadline.
func Discover(ctx context.Context) []Info {
	return discover(ctx)
}
