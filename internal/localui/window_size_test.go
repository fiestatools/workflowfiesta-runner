//go:build !nolocalui

package localui

import "testing"

func TestFixLegacyOversizedWindowSize_DarwinRetina(t *testing.T) {
	spec := statusWindowSizeSpec
	w, h := fixLegacyOversizedWindowSizeForPlatform(960, 1160, spec, "darwin")
	if w != 480 || h != 580 {
		t.Errorf("darwin legacy halve: got %vx%v, want 480x580", w, h)
	}
}

func TestFixLegacyOversizedWindowSize_Windows2x(t *testing.T) {
	spec := statusWindowSizeSpec
	w, h := fixLegacyOversizedWindowSizeForPlatform(888, 1160, spec, "windows")
	if w != 444 || h != 580 {
		t.Errorf("windows legacy 2x halve: got %vx%v, want 444x580", w, h)
	}
}

func TestClampWindowSize(t *testing.T) {
	spec := statusWindowSizeSpec
	w, h := clampWindowSize(2000, 2000, spec)
	if w != spec.maxW || h != spec.maxH {
		t.Errorf("clamp max: got %vx%v, want %vx%v", w, h, spec.maxW, spec.maxH)
	}
	w, h = clampWindowSize(100, 100, spec)
	if w != spec.minW || h != spec.minH {
		t.Errorf("clamp min: got %vx%v, want %vx%v", w, h, spec.minW, spec.minH)
	}
}

func TestNormalizeLoadedWindowSize_ReasonableUnchanged(t *testing.T) {
	spec := statusWindowSizeSpec
	w, h := normalizeLoadedWindowSize(500, 600, spec)
	if w != 500 || h != 600 {
		t.Errorf("reasonable size changed: got %vx%v", w, h)
	}
}
