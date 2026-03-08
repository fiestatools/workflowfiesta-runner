//go:build nolocalui

package localui

// RequestApproval falls back to a terminal prompt when Fyne is not compiled in.
func RequestApproval(req ApprovalRequest) bool {
	return headlessApproval(req)
}
