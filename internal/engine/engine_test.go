package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func managerForTest(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m, e := New(filepath.Join(root, "data"), make([]byte, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	s := m.Snapshot().Settings
	s.Directory = filepath.Join(root, "files")
	if e = m.UpdateSettings(s); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return m, root
}
func await(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}
func taskFor(m *Manager, id string) TaskView {
	for _, v := range m.Snapshot().Tasks {
		if v.ID == id {
			return v
		}
	}
	return TaskView{}
}
func finish(t *testing.T, m *Manager, id string) TaskView {
	t.Helper()
	var v TaskView
	await(t, func() bool { v = taskFor(m, id); return v.State == "completed" || v.State == "failed" })
	if v.State != "completed" {
		t.Fatalf("download failed: %+v", v)
	}
	return v
}
func pattern(w io.Writer, start, length int64) error {
	buf := make([]byte, 32<<10)
	for length > 0 {
		n := int64(len(buf))
		if n > length {
			n = length
		}
		for i := int64(0); i < n; i++ {
			buf[i] = byte((start + i) % 251)
		}
		if _, e := w.Write(buf[:n]); e != nil {
			return e
		}
		start += n
		length -= n
	}
	return nil
}
func verify(t *testing.T, path string, size int64) {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	actual := sha256.New()
	n, e := io.Copy(actual, f)
	if e != nil || n != size {
		t.Fatalf("size %d want %d: %v", n, size, e)
	}
	expected := sha256.New()
	pattern(expected, 0, size)
	if string(actual.Sum(nil)) != string(expected.Sum(nil)) {
		t.Fatal("SHA-256 mismatch")
	}
}

type fixture struct {
	size                         int64
	noRange, unknown, wrongRange bool
	etag                         atomic.Int32
	peak, live, requests         atomic.Int32
	delay                        time.Duration
	gate                         chan struct{}
	afterGate                    atomic.Bool
	secondPeak                   atomic.Int32
}

func (f *fixture) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/redirect" {
		http.Redirect(w, r, "/file", 302)
		return
	}
	if r.URL.Path == "/cookie" && r.Header.Get("Cookie") != "session=secret" {
		w.WriteHeader(403)
		return
	}
	if r.URL.Path == "/unauthorized" {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"version-%d\"", f.etag.Load()))
	start, end := int64(0), f.size-1
	segmented := !f.noRange && r.Header.Get("Range") != ""
	if segmented {
		if _, e := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); e != nil {
			w.WriteHeader(400)
			return
		}
		if f.size == 0 {
			w.Header().Set("Content-Range", "bytes */0")
			w.WriteHeader(416)
			return
		}
		if end >= f.size {
			end = f.size - 1
		}
		if f.wrongRange && end > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start+1, end, f.size))
		} else {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, f.size))
		}
	}
	if !f.unknown {
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if segmented {
		w.WriteHeader(206)
	} else {
		w.WriteHeader(200)
	}
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	if end > start && segmented {
		f.requests.Add(1)
		n := f.live.Add(1)
		defer f.live.Add(-1)
		for old := f.peak.Load(); n > old; old = f.peak.Load() {
			if f.peak.CompareAndSwap(old, n) {
				break
			}
		}
		if f.afterGate.Load() {
			for old := f.secondPeak.Load(); n > old; old = f.secondPeak.Load() {
				if f.secondPeak.CompareAndSwap(old, n) {
					break
				}
			}
		}
		if f.gate != nil {
			select {
			case <-f.gate:
			case <-r.Context().Done():
				return
			}
		}
	}
	for offset := start; offset <= end; {
		n := int64(32 << 10)
		if n > end-offset+1 {
			n = end - offset + 1
		}
		if e := pattern(w, offset, n); e != nil {
			return
		}
		offset += n
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return
			}
		}
	}
}

func TestDownloadModesAndAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name             string
		size             int64
		noRange, unknown bool
		path             string
	}{
		{"range", 9 << 20, false, false, "/file"}, {"no-range", 3 << 20, true, false, "/file"}, {"unknown", 512 << 10, true, true, "/file"}, {"empty", 0, false, false, "/file"}, {"redirect", 5 << 20, false, false, "/redirect"}, {"cookie", 5 << 20, false, false, "/cookie"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := managerForTest(t)
			f := &fixture{size: tc.size, noRange: tc.noRange, unknown: tc.unknown}
			server := httptest.NewServer(http.HandlerFunc(f.handler))
			defer server.Close()
			v, e := m.Add(context.Background(), Request{URL: server.URL + tc.path, Headers: map[string]string{"Cookie": "session=secret"}}, false)
			if e != nil {
				t.Fatal(e)
			}
			done := finish(t, m, v.ID)
			verify(t, done.Path, tc.size)
		})
	}
}

func Test32ConnectionsAndLiveReduction(t *testing.T) {
	m, _ := managerForTest(t)
	gate := make(chan struct{})
	f := &fixture{size: 160 << 20, gate: gate, delay: time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	v, e := m.Add(context.Background(), Request{URL: server.URL + "/file", Connections: 32}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return f.live.Load() == 32 })
	if e = m.SetConnections(v.ID, 2); e != nil {
		t.Fatal(e)
	}
	close(gate)
	await(t, func() bool { return f.live.Load() <= 2 })
	f.afterGate.Store(true)
	done := finish(t, m, v.ID)
	verify(t, done.Path, f.size)
	if f.peak.Load() != 32 {
		t.Fatalf("peak %d", f.peak.Load())
	}
	if f.secondPeak.Load() > 2 {
		t.Fatalf("did not converge: %d", f.secondPeak.Load())
	}
	for _, invalid := range []int{-1, 33} {
		if m.SetConnections(v.ID, invalid) == nil {
			t.Fatal("invalid concurrency accepted")
		}
	}
}

func TestPauseResumeAndResourceChange(t *testing.T) {
	m, _ := managerForTest(t)
	f := &fixture{size: 24 << 20, delay: time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	v, e := m.Add(context.Background(), Request{URL: server.URL + "/file", Connections: 1}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool {
		v := taskFor(m, v.ID)
		for _, c := range v.Chunks {
			if c.Done {
				return true
			}
		}
		return false
	})
	if e = m.Action(v.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return taskFor(m, v.ID).ActiveConnections == 0 })
	f.etag.Add(1)
	if e = m.Action(v.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return taskFor(m, v.ID).State == "failed" })
	if !strings.Contains(taskFor(m, v.ID).Error, "源文件") {
		t.Fatal(taskFor(m, v.ID).Error)
	}
	if e = m.Action(v.ID, "restart"); e != nil {
		t.Fatal(e)
	}
	done := finish(t, m, v.ID)
	verify(t, done.Path, f.size)
}

func TestDurableResumeAndEncryptedStore(t *testing.T) {
	m, root := managerForTest(t)
	f := &fixture{size: 20 << 20, delay: time.Millisecond}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	v, e := m.Add(context.Background(), Request{URL: server.URL + "/cookie?token=very-secret", Headers: map[string]string{"Cookie": "session=secret"}, Connections: 1}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool {
		for _, c := range taskFor(m, v.ID).Chunks {
			if c.Done {
				return true
			}
		}
		return false
	})
	m.Close()
	data, e := os.ReadFile(filepath.Join(root, "data", "tasks.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "very-secret") || strings.Contains(string(data), "session=secret") {
		t.Fatal("plaintext secret on disk")
	}
	m2, e := New(filepath.Join(root, "data"), make([]byte, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m2.Close()
	done := finish(t, m2, v.ID)
	verify(t, done.Path, f.size)
}

func TestHandoffIdempotenceAndFilenameCollision(t *testing.T) {
	m, _ := managerForTest(t)
	f := &fixture{size: 128 << 10}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	r := Request{URL: server.URL + "/file", Filename: "../../file.zip", HandoffID: "unique-handoff"}
	v, e := m.Add(context.Background(), r, true)
	if e != nil {
		t.Fatal(e)
	}
	v2, e := m.Add(context.Background(), r, true)
	if e != nil || v.ID != v2.ID {
		t.Fatal("duplicate handoff")
	}
	if v.State != "prepared" || v.Name != "file.zip" {
		t.Fatalf("unexpected %+v", v)
	}
	if e = os.WriteFile(v.Path, []byte("existing user file"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Handoff(r.HandoffID, "commit"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Handoff(r.HandoffID, "commit"); e != nil {
		t.Fatal(e)
	}
	done := finish(t, m, v.ID)
	if done.Path == v.Path {
		t.Fatal("overwrote user file")
	}
	verify(t, done.Path, f.size)
	original, _ := os.ReadFile(v.Path)
	if string(original) != "existing user file" {
		t.Fatal("existing file changed")
	}
}

func TestBadRangeAndAuthorization(t *testing.T) {
	for _, path := range []string{"/file", "/unauthorized"} {
		t.Run(path, func(t *testing.T) {
			m, _ := managerForTest(t)
			f := &fixture{size: 8 << 20, wrongRange: true}
			server := httptest.NewServer(http.HandlerFunc(f.handler))
			defer server.Close()
			v, e := m.Add(context.Background(), Request{URL: server.URL + path}, false)
			if e != nil {
				t.Fatal(e)
			}
			await(t, func() bool { return taskFor(m, v.ID).State == "failed" })
			if _, e = os.Stat(v.Path); !os.IsNotExist(e) {
				t.Fatal("bad response installed as complete file")
			}
		})
	}
}

func TestCrossOriginCredentialsAreRemoved(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			leaked.Store(true)
		}
		w.Header().Set("Content-Length", "1")
		w.Write([]byte("x"))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer origin.Close()
	m, _ := managerForTest(t)
	v, e := m.Add(context.Background(), Request{URL: origin.URL, Headers: map[string]string{"Cookie": "secret", "Authorization": "Bearer secret", "Referer": origin.URL + "?secret"}}, false)
	if e != nil {
		t.Fatal(e)
	}
	finish(t, m, v.ID)
	if leaked.Load() {
		t.Fatal("credentials crossed origin")
	}
}

func TestSettingsAndUnwritableDestination(t *testing.T) {
	m, root := managerForTest(t)
	s := m.Snapshot().Settings
	for _, n := range []int{0, 33} {
		s.Connections = n
		if m.UpdateSettings(s) == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	s.Connections = 32
	s.PreventIdleSleep = true
	if e := m.UpdateSettings(s); e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(root, "not-directory")
	os.WriteFile(bad, []byte("x"), 0600)
	if _, e := m.Add(context.Background(), Request{URL: "http://localhost/file", Directory: bad}, false); e == nil {
		t.Fatal("non-directory destination accepted")
	}
}

func TestRetryTruncatedRangeAndIgnoredRangeFallback(t *testing.T) {
	for _, mode := range []string{"503", "truncated", "ignored"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := managerForTest(t)
			f := &fixture{size: 9 << 20}
			var failures atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "" && r.Header.Get("Range") != "bytes=0-0" {
					if mode == "ignored" {
						r.Header.Del("Range")
						f.handler(w, r)
						return
					}
					if failures.Add(1) == 1 {
						if mode == "503" {
							w.Header().Set("Retry-After", "1")
							w.WriteHeader(503)
							return
						}
						var start, end int64
						fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
						w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, f.size))
						w.Header().Set("ETag", "\"version-0\"")
						w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
						w.WriteHeader(206)
						pattern(w, start, 1024)
						return
					}
				}
				f.handler(w, r)
			}))
			defer server.Close()
			v, e := m.Add(context.Background(), Request{URL: server.URL + "/file"}, false)
			if e != nil {
				t.Fatal(e)
			}
			done := finish(t, m, v.ID)
			verify(t, done.Path, f.size)
			if mode == "ignored" && done.Resumable {
				t.Fatal("fallback incorrectly advertises resume")
			}
		})
	}
}

func TestGlobalRateLimitQueueAndPause(t *testing.T) {
	m, _ := managerForTest(t)
	s := m.Snapshot().Settings
	s.MaxTasks = 1
	s.SpeedLimit = 256 << 10
	if e := m.UpdateSettings(s); e != nil {
		t.Fatal(e)
	}
	f := &fixture{size: 512 << 10, noRange: true}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	start := time.Now()
	a, e := m.Add(context.Background(), Request{URL: server.URL + "/a", Connections: 1}, false)
	if e != nil {
		t.Fatal(e)
	}
	b, e := m.Add(context.Background(), Request{URL: server.URL + "/b"}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return taskFor(m, a.ID).State == "downloading" })
	if taskFor(m, b.ID).State != "queued" {
		t.Fatal("max task count exceeded")
	}
	if e = m.Action(b.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	done := finish(t, m, a.ID)
	verify(t, done.Path, f.size)
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatal("global limit not applied")
	}
	if taskFor(m, b.ID).State != "paused" {
		t.Fatal("paused queue item started")
	}
	s.SpeedLimit = 0
	if e = m.UpdateSettings(s); e != nil {
		t.Fatal(e)
	}
	if e = m.Action(b.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	finish(t, m, b.ID)
}

func TestAbortBeforePrepareFinishes(t *testing.T) {
	m, _ := managerForTest(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	f := &fixture{size: 1 << 20}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; f.handler(w, r) }))
	defer server.Close()
	finished := make(chan error, 1)
	go func() {
		_, e := m.Add(context.Background(), Request{URL: server.URL, HandoffID: "cancel-during-probe"}, true)
		finished <- e
	}()
	<-entered
	if _, e := m.Handoff("cancel-during-probe", "abort"); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-finished; e == nil {
		t.Fatal("cancelled prepare created a task")
	}
	if len(m.Snapshot().Tasks) != 0 {
		t.Fatal("orphan prepared task remained")
	}
	if _, e := m.Handoff("cancel-during-probe", "commit"); e == nil {
		t.Fatal("aborted handoff committed")
	}
}

func TestRemovingLimitWakesExistingReservations(t *testing.T) {
	m, _ := managerForTest(t)
	settings := m.Snapshot().Settings
	settings.SpeedLimit = 1024
	if e := m.UpdateSettings(settings); e != nil {
		t.Fatal(e)
	}
	f := &fixture{size: 256 << 10, noRange: true}
	server := httptest.NewServer(http.HandlerFunc(f.handler))
	defer server.Close()
	v, e := m.Add(context.Background(), Request{URL: server.URL}, false)
	if e != nil {
		t.Fatal(e)
	}
	await(t, func() bool { return taskFor(m, v.ID).Received >= 64<<10 })
	settings.SpeedLimit = 0
	start := time.Now()
	if e = m.UpdateSettings(settings); e != nil {
		t.Fatal(e)
	}
	done := finish(t, m, v.ID)
	verify(t, done.Path, f.size)
	if time.Since(start) > 5*time.Second {
		t.Fatal("old bandwidth reservation was not interrupted")
	}
}
