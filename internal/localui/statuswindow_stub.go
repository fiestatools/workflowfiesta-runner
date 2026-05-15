//go:build nolocalui

package localui

// StatusWindow is a no-op stub used in server/headless builds (nolocalui tag).
type StatusWindow struct{}

func NewStatusWindow(_, _ string) *StatusWindow { return &StatusWindow{} }
func (sw *StatusWindow) Show()                         {}
func (sw *StatusWindow) SetConnecting()                {}
func (sw *StatusWindow) SetReconnecting()              {}
func (sw *StatusWindow) SetConnected(_ bool)           {}
func (sw *StatusWindow) SetOnAPIConnected(_ func())    {}
func (sw *StatusWindow) APIConnected() bool            { return false }
func (sw *StatusWindow) SetIdle()                      {}
func (sw *StatusWindow) SetJobRunning(_, _, _ string)  {}
func (sw *StatusWindow) SetJobComplete(_, _ string, _ int) {}
func (sw *StatusWindow) AppendLog(_ string)            {}
func (sw *StatusWindow) SetOnOpenSettings(_ func())    {}
func (sw *StatusWindow) SetStopAgentHandler(_ func(string) error) {}
