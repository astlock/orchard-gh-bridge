package bridge

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/breakawaydata/orchard-gh-bridge/config"
	"github.com/breakawaydata/orchard-gh-bridge/orchard"
)

func TestTargetRunners(t *testing.T) {
	tests := []struct {
		name                     string
		min, max, assigned, want int
	}{
		{"no minRunners", 0, 4, 2, 2},
		{"idle pool", 3, 10, 0, 3},
		{"pool on top of assigned jobs", 3, 10, 2, 5},
		{"capped at maxRunners", 3, 4, 2, 4},
		{"maxRunners unknown yet", 3, 0, 2, 5},
		{"never below assigned jobs", 3, 1, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(Config{ScaleSetName: "test", MinRunners: tt.min, Logger: testLogger()})
			b.SetMaxRunners(tt.max)
			if got := b.targetRunners(tt.assigned); got != tt.want {
				t.Errorf("targetRunners(%d) with min=%d max=%d = %d, want %d", tt.assigned, tt.min, tt.max, got, tt.want)
			}
		})
	}
}

func TestHandleDesired_MinRunnersBootsIdlePool(t *testing.T) {
	mock := newMockOrchard()
	mock.workers = []orchard.Worker{
		{Name: "mini", Resources: map[string]uint64{resourceTartVMs: 2}, Labels: map[string]string{"pool": "mac"}},
	}
	b := New(Config{
		ScaleSetName:  "test",
		VMConfig:      config.VMConfig{Labels: map[string]string{"pool": "mac"}},
		OrchardClient: mock,
		Capacity:      NewCapacity(10),
		State:         NewStateView(mock, time.Minute, DefaultWorkerStaleAfter),
		Logger:        testLogger(),
		MinRunners:    2,
	})
	b.SetMaxRunners(5)
	var created atomic.Int32
	b.testCreateOneVM = func(context.Context, uint64, uint64, map[string]string, []any) error {
		created.Add(1)
		return nil
	}

	got, err := b.HandleDesiredRunnerCount(context.Background(), 0)
	if err != nil {
		t.Fatalf("HandleDesiredRunnerCount: %v", err)
	}
	if got != 2 || created.Load() != 2 {
		t.Errorf("with no jobs: active = %d, created = %d, want 2 ready runners", got, created.Load())
	}
}

func TestHandleDesired_MinRunnersLimitedByFreeSlots(t *testing.T) {
	mock := newMockOrchard()
	mock.workers = []orchard.Worker{
		{Name: "mini", Resources: map[string]uint64{resourceTartVMs: 2}, Labels: map[string]string{"pool": "mac"}},
	}
	b := New(Config{
		ScaleSetName:  "test",
		VMConfig:      config.VMConfig{Labels: map[string]string{"pool": "mac"}},
		OrchardClient: mock,
		Capacity:      NewCapacity(10),
		State:         NewStateView(mock, time.Minute, DefaultWorkerStaleAfter),
		Logger:        testLogger(),
		MinRunners:    5,
	})
	var created atomic.Int32
	b.testCreateOneVM = func(context.Context, uint64, uint64, map[string]string, []any) error {
		created.Add(1)
		return nil
	}

	if _, err := b.HandleDesiredRunnerCount(context.Background(), 0); err != nil {
		t.Fatalf("HandleDesiredRunnerCount: %v", err)
	}
	if created.Load() != 2 {
		t.Errorf("created = %d, want 2 (only two worker slots)", created.Load())
	}
}
