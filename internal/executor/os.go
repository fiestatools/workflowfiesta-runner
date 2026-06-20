package executor

import "runtime"

// currentOS mirrors runtime.GOOS but is a variable so tests can override it
// to exercise OS-specific branches without needing a real Windows/Linux host.
var currentOS = runtime.GOOS
