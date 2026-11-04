package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreviewDoesNotCreateTaskOrFile(t *testing.T) {
	m, root := managerForTest(t)
	f := &fixture{size: 8 << 20}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	p, e := m.Preview(context.Background(), Request{URL: server.URL + "/file?secret=token"})
	if e != nil || p.Total != f.size {
		t.Fatalf("preview %+v %v", p, e)
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("preview created a task")
	}
	if _, e = os.Stat(filepath.Join(root, "files")); !os.IsNotExist(e) {
		t.Fatal("preview touched destination")
	}
	if p.Source != server.URL+"/file" {
		t.Fatal("query leaked into preview")
	}
}

func TestAwaitConfirmationPersistsWithoutStarting(t *testing.T) {
	m, root := managerForTest(t)
	f := &fixture{size: 8 << 20}
	var bodies atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			bodies.Add(1)
		}
		f.handler(w, r)
	}))
	defer server.Close()
	task, e := m.Add(context.Background(), Request{URL: server.URL + "/file", HandoffID: "confirm-test", Filename: "archive.zip"}, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Handoff("confirm-test", "commit"); e != nil {
		t.Fatal(e)
	}
	if taskFor(m, task.ID).State != "awaiting_confirmation" {
		t.Fatal("handoff queued without consent")
	}
	time.Sleep(350 * time.Millisecond)
	if bodies.Load() != 0 {
		t.Fatal("download started before confirmation")
	}
	if _, e = os.Stat(filepath.Join(root, "files")); !os.IsNotExist(e) {
		t.Fatal("unconfirmed task allocated files")
	}
	for _, action := range []string{"resume", "restart", "pause"} {
		if m.Action(task.ID, action) == nil {
			t.Fatal("bypassed confirmation", action)
		}
	}
	m.Close()
	reopened, e := New(filepath.Join(root, "data"), make([]byte, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if taskFor(reopened, task.ID).State != "awaiting_confirmation" {
		t.Fatal("restart lost confirmation")
	}
	bad := filepath.Join(root, "not-directory")
	os.WriteFile(bad, []byte("user"), 0600)
	if _, e = reopened.Confirm(Confirmation{ID: task.ID, Directory: bad, Filename: "archive.zip"}); e == nil {
		t.Fatal("invalid destination accepted")
	}
	if taskFor(reopened, task.ID).State != "awaiting_confirmation" {
		t.Fatal("failed confirmation changed state")
	}
	dir := filepath.Join(root, "custom")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "renamed.zip"), []byte("existing"), 0600)
	p := Confirmation{ID: task.ID, Directory: dir, Filename: "renamed.zip", Connections: 2}
	confirmed, e := reopened.Confirm(p)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := reopened.Confirm(p)
	if e != nil || duplicate.Path != confirmed.Path {
		t.Fatal("confirmation not idempotent")
	}
	done := finish(t, reopened, task.ID)
	verify(t, done.Path, f.size)
	if done.Name != "renamed (1).zip" || done.Connections != 2 || !done.Started || done.RunID == "" {
		t.Fatalf("incorrect result %+v", done)
	}
}

func TestCancelPendingRetainsHandoffReceipt(t *testing.T) {
	m, root := managerForTest(t)
	f := &fixture{size: 1024}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	task, e := m.Add(context.Background(), Request{URL: server.URL + "/file", HandoffID: "cancel-pending"}, true)
	if e != nil {
		t.Fatal(e)
	}
	m.Handoff("cancel-pending", "commit")
	if e = m.Action(task.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("cancelled draft remains visible")
	}
	if _, e = m.Confirm(Confirmation{ID: task.ID, Filename: "bad", Directory: root}); e == nil {
		t.Fatal("cancelled draft started")
	}
	for _, action := range []string{"commit", "status"} {
		state, e := m.Handoff("cancel-pending", action)
		if e != nil || state["state"] != "committed" {
			t.Fatal("handoff recovery receipt lost", state, e)
		}
	}
	if m.tasks[task.ID].Request.URL != "" {
		t.Fatal("discard retained credentials")
	}
	m.Close()
	reopened, e := New(filepath.Join(root, "data"), make([]byte, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	state, e := reopened.Handoff("cancel-pending", "commit")
	if e != nil || state["state"] != "committed" || len(reopened.Snapshot().Tasks) != 0 {
		t.Fatal("restart resurrected cancelled draft")
	}
}

func TestCancelRunningAndRunIdentity(t *testing.T) {
	m, _ := managerForTest(t)
	f := &fixture{size: 24 << 20, delay: 2 * time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	task, e := m.Add(context.Background(), Request{URL: server.URL + "/file"}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return taskFor(m, task.ID).Started })
	first := taskFor(m, task.ID).RunID
	if e = m.Action(task.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.runs[task.ID] == nil })
	m.Action(task.ID, "resume")
	if taskFor(m, task.ID).RunID != first {
		t.Fatal("pause/resume changed run identity")
	}
	if e = m.Action(task.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	if taskFor(m, task.ID).State != "cancelled" {
		t.Fatal("cancel did not terminate")
	}
	if _, e = os.Stat(m.tasks[task.ID].TempPath); !os.IsNotExist(e) {
		t.Fatal("cancel left partial file")
	}
	if e = m.Action(task.ID, "restart"); e != nil {
		t.Fatal(e)
	}
	if taskFor(m, task.ID).RunID == first {
		t.Fatal("restart reused notification identity")
	}
	done := finish(t, m, task.ID)
	verify(t, done.Path, f.size)
	// Cancelling an installed file must never delete it or change its result.
	if e = m.Action(task.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	verify(t, done.Path, f.size)
}

func TestLegacyPreparedFileCanBeConfirmed(t *testing.T) {
	m, root := managerForTest(t)
	f := &fixture{size: 1024}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	v, e := m.Add(context.Background(), Request{URL: server.URL + "/file", HandoffID: "legacy-prepared"}, true)
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Dir(v.Path), 0700)
	oldPart := filepath.Join(filepath.Dir(v.Path), ".dslq-"+v.ID+".part")
	if e = os.WriteFile(oldPart, nil, 0600); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.tasks[v.ID].TempPath = oldPart
	m.store.put(v.ID, m.tasks[v.ID])
	m.mu.Unlock()
	m.Handoff("legacy-prepared", "commit")
	if _, e = m.Confirm(Confirmation{ID: v.ID, Directory: filepath.Join(root, "elsewhere"), Filename: "legacy.bin"}); e != nil {
		t.Fatal(e)
	}
	done := finish(t, m, v.ID)
	verify(t, done.Path, f.size)
	if _, e = os.Stat(oldPart); !os.IsNotExist(e) {
		t.Fatal("legacy empty part leaked")
	}
}
