package engine

import (
	"reflect"
	"testing"
	"time"
)

func TestSnapshotStableOrderAndIndependentValues(t *testing.T) {
	m, _ := managerForTest(t)
	created := time.Now()
	m.mu.Lock()
	for _, id := range []string{"c", "a", "b"} {
		m.tasks[id] = &Task{ID: id, State: "paused", CreatedAt: created, Chunks: []Chunk{{Start: 0, End: 9}}}
	}
	m.mu.Unlock()
	first := m.Snapshot()
	for range 50 {
		got := m.Snapshot()
		if got.Tasks[0].ID != "a" || got.Tasks[1].ID != "b" || got.Tasks[2].ID != "c" || !reflect.DeepEqual(got, first) {
			t.Fatal("unchanged snapshot order is unstable for identical timestamps")
		}
	}
	m.mu.Lock()
	m.tasks["a"].Chunks[0].Done = true
	m.settings.CaptureExtensions[0] = "changed"
	m.mu.Unlock()
	if first.Tasks[0].Chunks[0].Done || first.Settings.CaptureExtensions[0] == "changed" {
		t.Fatal("later engine changes mutated a previously queued snapshot")
	}
}
