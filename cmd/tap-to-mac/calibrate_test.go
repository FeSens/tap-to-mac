package main

import "testing"

func near(a, b, eps float64) bool {
	if a-b > eps || b-a > eps {
		return false
	}
	return true
}

func TestWeakestRealTap_DropsLowest(t *testing.T) {
	amps := []float64{0.014, 0.022, 0.025, 0.030, 0.031}
	got := weakestRealTap(amps)
	if !near(got, 0.022, 0.0001) {
		t.Fatalf("got %v want 0.022 (2nd-lowest)", got)
	}
}

func TestWeakestRealTap_TolerantToCalibrationFalsePositive(t *testing.T) {
	// One spurious low sample (e.g. background blip) shouldn't drag the
	// threshold all the way to noise.
	amps := []float64{0.003, 0.022, 0.025, 0.030, 0.031}
	got := weakestRealTap(amps)
	if !near(got, 0.022, 0.0001) {
		t.Fatalf("got %v want 0.022 (after dropping the noise sample)", got)
	}
}

func TestRecommendedThreshold_OnUserData(t *testing.T) {
	// From the user's actual `tap-to-mac watch` data — gentle taps in the
	// 0.014-0.031 range, dropping the lowest gives 0.022, * 0.7 = 0.0154.
	amps := []float64{0.0194, 0.0220, 0.0225, 0.0308, 0.0307}
	got := recommendedThreshold(amps)
	if got < 0.013 || got > 0.018 {
		t.Fatalf("got %v want ~0.0156", got)
	}
}

func TestRecommendedThreshold_ClampedToFloor(t *testing.T) {
	amps := []float64{0.001, 0.002, 0.003, 0.004, 0.005}
	got := recommendedThreshold(amps)
	if got != calibrationFloor {
		t.Fatalf("expected floor (%v), got %v", calibrationFloor, got)
	}
}
