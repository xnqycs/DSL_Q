package rpc

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"dslq/internal/engine"
)

func TestSnapshotsOnlyPublishChanges(t *testing.T) {
	empty := engine.Snapshot{Protocol: 1, Tasks: []engine.TaskView{}, Settings: engine.Defaults()}
	queued := empty
	queued.Tasks = []engine.TaskView{{ID: "file", RunID: "run", State: "queued"}}
	progress := queued
	progress.Tasks = []engine.TaskView{{ID: "file", RunID: "run", State: "downloading", Started: true, Received: 10, Speed: 40}}
	progress.ActiveCount = 1
	chunk := progress
	chunk.Tasks = append([]engine.TaskView{}, progress.Tasks...)
	chunk.Tasks[0].Chunks = []engine.Chunk{{Start: 0, End: 9, Done: true}}
	completed := chunk
	completed.Tasks = append([]engine.TaskView{}, chunk.Tasks...)
	completed.Tasks[0].State = "completed"
	completed.Tasks[0].Speed = 0
	completed.ActiveCount = 0
	settings := completed
	settings.Settings.NotifyOnFinish = false
	retry := settings
	retry.Tasks = append([]engine.TaskView{}, settings.Tasks...)
	retry.Tasks[0].RunID = "retry"
	want := []engine.Snapshot{empty, queued, progress, chunk, completed, settings, retry, empty}

	events, done := make(chan engine.Snapshot), make(chan struct{})
	writes := make(chan engine.Snapshot, len(want)*4)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		forwardSnapshots(events, done, func(r Response) error {
			if r.Method != "snapshot" || r.Protocol != 1 || r.JSONRPC != "2.0" {
				t.Error("snapshot envelope changed")
			}
			writes <- r.Params.(engine.Snapshot)
			return nil
		})
	}()
	for _, snapshot := range want {
		for range 4 {
			events <- snapshot
		}
	}
	close(done)
	<-stopped
	close(writes)
	var got []engine.Snapshot
	for snapshot := range writes {
		got = append(got, snapshot)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published %d snapshots, want %d distinct state transitions", len(got), len(want))
	}
}

func TestSlowSnapshotConsumerReceivesLatestCompletion(t *testing.T) {
	events, done := make(chan engine.Snapshot, 1), make(chan struct{})
	defer close(done)
	blocked, release := make(chan struct{}), make(chan struct{})
	writes := make(chan engine.Snapshot, 4)
	go forwardSnapshots(events, done, func(r Response) error {
		snapshot := r.Params.(engine.Snapshot)
		if snapshot.Tasks[0].State == "queued" {
			close(blocked)
			<-release
		}
		writes <- snapshot
		return nil
	})
	snapshot := func(state string) engine.Snapshot {
		return engine.Snapshot{Protocol: 1, Tasks: []engine.TaskView{{ID: "file", State: state}}}
	}
	queueSnapshot(events, snapshot("queued"))
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not start writing")
	}
	queued := make(chan struct{})
	go func() {
		queueSnapshot(events, snapshot("downloading"))
		queueSnapshot(events, snapshot("completed"))
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("slow UI blocked the engine")
	}
	close(release)
	for _, state := range []string{"queued", "completed"} {
		select {
		case got := <-writes:
			if got.Tasks[0].State != state {
				t.Fatalf("received %s, want %s", got.Tasks[0].State, state)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("lost %s without a subsequent engine tick", state)
		}
	}
}

func TestSnapshotStreamStopsOnWriteFailure(t *testing.T) {
	events := make(chan engine.Snapshot, 1)
	events <- engine.Snapshot{Protocol: 1}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		forwardSnapshots(events, make(chan struct{}), func(Response) error { return errors.New("closed pipe") })
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot writer did not stop after the pipe closed")
	}
}
