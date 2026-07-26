package speech

import (
	"errors"
	"testing"
)

func TestGateEnforcesPerUserAndGlobalLimits(t *testing.T) {
	gate := NewGate(2, 1)
	releaseA, err := gate.Acquire("user-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Acquire("user-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second user-a acquire error = %v, want ErrBusy", err)
	}
	releaseB, err := gate.Acquire("user-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Acquire("user-c"); !errors.Is(err, ErrBusy) {
		t.Fatalf("third global acquire error = %v, want ErrBusy", err)
	}
	releaseA()
	releaseA()
	releaseC, err := gate.Acquire("user-c")
	if err != nil {
		t.Fatal(err)
	}
	releaseB()
	releaseC()
	global, users := gate.Snapshot()
	if global != 0 || users != 0 {
		t.Fatalf("gate snapshot = (%d, %d), want empty", global, users)
	}
}

func TestSpeedToAliyunRate(t *testing.T) {
	for speed, expected := range map[float64]int{
		0.5: -500,
		1.0: 0,
		1.5: 250,
		2.0: 500,
	} {
		if actual := speedToAliyunRate(speed); actual != expected {
			t.Fatalf("speedToAliyunRate(%v) = %d, want %d", speed, actual, expected)
		}
	}
}
