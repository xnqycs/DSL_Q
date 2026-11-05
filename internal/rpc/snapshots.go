package rpc

import (
	"reflect"

	"dslq/internal/engine"
)

// The engine loop is the sole producer. Keep the latest pending state without
// waiting for the UI to read stdout, including the final state of a download.
func queueSnapshot(events chan engine.Snapshot, snapshot engine.Snapshot) {
	select {
	case events <- snapshot:
		return
	default:
	}
	select {
	case <-events:
	default:
	}
	events <- snapshot
}

func forwardSnapshots(events <-chan engine.Snapshot, done <-chan struct{}, write func(Response) error) {
	var previous *engine.Snapshot
	for {
		select {
		case snapshot := <-events:
			// Compare at the consumer, not at enqueue time: an intermediate
			// snapshot may have been replaced while stdout was blocked.
			if previous != nil && reflect.DeepEqual(*previous, snapshot) {
				continue
			}
			if err := write(Response{JSONRPC: "2.0", Protocol: 1, Method: "snapshot", Params: snapshot}); err != nil {
				return
			}
			previous = &snapshot
		case <-done:
			return
		}
	}
}
