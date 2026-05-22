//go:build !nolocalui

package localui

import (
	"runtime"
	"time"

	"fyne.io/fyne/v2"
)

// windowSizeSpec defines preference keys and bounds for a resizable Fyne window.
type windowSizeSpec struct {
	prefW, prefH             string
	defaultW, defaultH       float32
	minW, minH, maxW, maxH   float32
}

var (
	statusWindowSizeSpec = windowSizeSpec{
		prefW: "status.window.width", prefH: "status.window.height",
		defaultW: 480, defaultH: 580,
		minW: 360, minH: 420, maxW: 720, maxH: 900,
	}
	approvalWindowSizeSpec = windowSizeSpec{
		prefW: "approval.window.width", prefH: "approval.window.height",
		defaultW: 460, defaultH: 280,
		minW: 380, minH: 240, maxW: 700, maxH: 500,
	}
)

// logicalWindowSize converts the canvas size to the size used by window.Resize.
// macOS Retina stores canvas dimensions in device pixels; Windows may do the same
// when display scaling is above 100%.
func logicalWindowSize(win fyne.Window) fyne.Size {
	c := win.Canvas()
	if c == nil {
		return fyne.NewSize(0, 0)
	}
	sz := c.Size()
	scale := c.Scale()
	if scale < 1 {
		scale = 1
	}

	switch runtime.GOOS {
	case "darwin":
		// Always normalize: canvas size is device pixels on Retina displays.
		return fyne.NewSize(sz.Width/scale, sz.Height/scale)
	case "windows":
		// At 100% DPI, canvas size already matches logical size; at 125%/150% divide.
		if scale > 1.01 {
			return fyne.NewSize(sz.Width/scale, sz.Height/scale)
		}
		return sz
	default:
		if scale > 1.01 {
			return fyne.NewSize(sz.Width/scale, sz.Height/scale)
		}
		return sz
	}
}

func loadWindowSize(prefs fyne.Preferences, spec windowSizeSpec) fyne.Size {
	w := float32(prefs.FloatWithFallback(spec.prefW, float64(spec.defaultW)))
	h := float32(prefs.FloatWithFallback(spec.prefH, float64(spec.defaultH)))
	w, h = normalizeLoadedWindowSize(w, h, spec)
	return fyne.NewSize(w, h)
}

func persistWindowSize(prefs fyne.Preferences, win fyne.Window, spec windowSizeSpec) {
	logical := logicalWindowSize(win)
	w, h := clampWindowSize(logical.Width, logical.Height, spec)
	prefs.SetFloat(spec.prefW, float64(w))
	prefs.SetFloat(spec.prefH, float64(h))
}

// normalizeLoadedWindowSize clamps dimensions and repairs legacy prefs that stored
// raw canvas pixels (≈2× logical size) before platform-aware persistence.
func normalizeLoadedWindowSize(w, h float32, spec windowSizeSpec) (float32, float32) {
	w, h = fixLegacyOversizedWindowSize(w, h, spec)
	return clampWindowSize(w, h, spec)
}

func fixLegacyOversizedWindowSize(w, h float32, spec windowSizeSpec) (float32, float32) {
	return fixLegacyOversizedWindowSizeForPlatform(w, h, spec, runtime.GOOS)
}

func fixLegacyOversizedWindowSizeForPlatform(w, h float32, spec windowSizeSpec, goos string) (float32, float32) {
	switch goos {
	case "darwin":
		// Retina prefs often landed at ~2× logical (e.g. 960×1160 instead of 480×580).
		if w > spec.maxW*1.2 || h > spec.maxH*1.2 {
			if w >= spec.defaultW*1.7 {
				w /= 2
			}
			if h >= spec.defaultH*1.7 {
				h /= 2
			}
		}
	case "windows":
		// High-DPI saves can be ~1.25–2×; halve only when clearly 2× the design default.
		if w >= spec.defaultW*1.85 && h >= spec.defaultH*1.85 {
			w /= 2
			h /= 2
		} else if w > spec.maxW*1.15 && w >= spec.defaultW*1.5 {
			// 125%/150% Windows scaling sometimes saves between 1.25× and 1.85×.
			w /= 1.25
		}
		if h > spec.maxH*1.15 && h >= spec.defaultH*1.5 {
			h /= 1.25
		}
	}
	return w, h
}

func clampWindowSize(w, h float32, spec windowSizeSpec) (float32, float32) {
	if w < spec.minW {
		w = spec.minW
	}
	if w > spec.maxW {
		w = spec.maxW
	}
	if h < spec.minH {
		h = spec.minH
	}
	if h > spec.maxH {
		h = spec.maxH
	}
	return w, h
}

// startWindowSizePersistence polls the window and writes logical size to prefs
// after three consecutive stable readings (avoids saves during content reflow).
func startWindowSizePersistence(prefs fyne.Preferences, win fyne.Window, spec windowSizeSpec) {
	go func() {
		var prevW, prevH float32
		var stable int
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			fyne.Do(func() {
				logical := logicalWindowSize(win)
				if logical.Width == prevW && logical.Height == prevH {
					stable++
					if stable == 3 {
						w, h := clampWindowSize(logical.Width, logical.Height, spec)
						prefs.SetFloat(spec.prefW, float64(w))
						prefs.SetFloat(spec.prefH, float64(h))
					}
				} else {
					prevW, prevH = logical.Width, logical.Height
					stable = 0
				}
			})
		}
	}()
}
