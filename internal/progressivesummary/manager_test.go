package progressivesummary

import (
	"testing"
	"time"
)

func TestManagerUsesSingleProbeAndCooldown(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	manager := New(30 * time.Minute)
	manager.now = func() time.Time { return now }

	probe := manager.Decide("https://cpa.example/v1", "gpt-test")
	if !probe.Requested || !probe.Probe {
		t.Fatalf("first decision = %#v, want probe", probe)
	}
	concurrent := manager.Decide("https://cpa.example/v1", "gpt-test")
	if concurrent.Requested {
		t.Fatalf("concurrent decision = %#v, want baseline", concurrent)
	}

	manager.MarkUnsupported(probe)
	status := manager.Snapshot("https://cpa.example/v1")
	if len(status) != 1 || status[0].State != StateCooldown ||
		status[0].CooldownUntil != now.Add(30*time.Minute).UnixMilli() {
		t.Fatalf("cooldown status = %#v", status)
	}
	if decision := manager.Decide("https://cpa.example/v1", "gpt-test"); decision.Requested {
		t.Fatalf("cooldown decision = %#v, want baseline", decision)
	}

	now = now.Add(30 * time.Minute)
	reprobe := manager.Decide("https://cpa.example/v1", "gpt-test")
	if !reprobe.Requested || !reprobe.Probe {
		t.Fatalf("reprobe decision = %#v", reprobe)
	}
	manager.MarkAccepted(reprobe)
	active := manager.Decide("https://cpa.example/v1", "gpt-test")
	if !active.Requested || active.Probe {
		t.Fatalf("active decision = %#v", active)
	}
}

func TestManagerInconclusiveProbeCanBeRetriedAndReset(t *testing.T) {
	manager := New(30 * time.Minute)
	first := manager.Decide("endpoint", "model")
	manager.MarkInconclusive(first)
	second := manager.Decide("endpoint", "model")
	if !second.Requested || !second.Probe ||
		second.generation == first.generation {
		t.Fatalf("second probe = %#v after %#v", second, first)
	}

	manager.MarkAccepted(second)
	manager.Reset()
	if status := manager.Snapshot("endpoint"); len(status) != 0 {
		t.Fatalf("status after reset = %#v", status)
	}
	next := manager.Decide("endpoint", "model")
	if !next.Requested || !next.Probe {
		t.Fatalf("decision after reset = %#v", next)
	}
}

func TestManagerScopesCompatibilityByEndpointAndModel(t *testing.T) {
	manager := New(30 * time.Minute)
	first := manager.Decide("endpoint-a", "model-a")
	manager.MarkUnsupported(first)

	for _, key := range [][2]string{
		{"endpoint-a", "model-b"},
		{"endpoint-b", "model-a"},
	} {
		decision := manager.Decide(key[0], key[1])
		if !decision.Requested || !decision.Probe {
			t.Fatalf("decision for %v = %#v", key, decision)
		}
	}
}

func TestResetPreventsAnOldInFlightDecisionFromRestoringState(t *testing.T) {
	manager := New(30 * time.Minute)
	oldProbe := manager.Decide("endpoint", "model")
	manager.Reset()

	manager.MarkAccepted(oldProbe)
	manager.MarkUnsupported(oldProbe)
	if status := manager.Snapshot("endpoint"); len(status) != 0 {
		t.Fatalf("old decision restored state after reset: %#v", status)
	}

	next := manager.Decide("endpoint", "model")
	if !next.Requested || !next.Probe {
		t.Fatalf("next decision = %#v, want a fresh probe", next)
	}
}

func TestAcceptedConcurrentRequestCannotOverrideCooldown(t *testing.T) {
	manager := New(30 * time.Minute)
	probe := manager.Decide("endpoint", "model")
	manager.MarkAccepted(probe)

	first := manager.Decide("endpoint", "model")
	second := manager.Decide("endpoint", "model")
	manager.MarkUnsupported(first)
	manager.MarkAccepted(second)

	status := manager.Snapshot("endpoint")
	if len(status) != 1 || status[0].State != StateCooldown {
		t.Fatalf("status = %#v, want cooldown", status)
	}
}
