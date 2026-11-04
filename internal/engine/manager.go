package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/time/rate"
)

type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type Snapshot struct {
	Tasks       []TaskView `json:"tasks"`
	Settings    Settings   `json:"settings"`
	ActiveCount int        `json:"activeCount"`
	Protocol    int        `json:"protocol"`
}
type Manager struct {
	mu          sync.Mutex
	store       *store
	tasks       map[string]*Task
	runs        map[string]*running
	settings    Settings
	limiter     *rate.Limiter
	rateChanged chan struct{}
	closing     bool
	stop        chan struct{}
	loopDone    chan struct{}
	onSnapshot  func(Snapshot)
}

func New(dir string, key []byte, onSnapshot func(Snapshot)) (*Manager, error) {
	s, e := openStore(dir, key)
	if e != nil {
		return nil, e
	}
	rows, e := s.all()
	if e != nil {
		s.close()
		return nil, e
	}
	m := &Manager{store: s, tasks: map[string]*Task{}, runs: map[string]*running{}, settings: Defaults(), limiter: rate.NewLimiter(rate.Inf, 64<<10), stop: make(chan struct{}), loopDone: make(chan struct{}), onSnapshot: onSnapshot}
	for id, p := range rows {
		if id == "settings" {
			if e = json.Unmarshal(p, &m.settings); e != nil {
				s.close()
				return nil, e
			}
			continue
		}
		var t Task
		if e = json.Unmarshal(p, &t); e != nil {
			s.close()
			return nil, e
		}
		if active(t.State) {
			t.State = "queued"
		}
		t.Speed = 0
		t.ActiveConnections = 0
		t.Received = completedBytes(&t)
		if t.State == "completed" {
			t.Received = t.Total
		}
		m.tasks[id] = &t
	}
	if e = m.settings.Validate(); e != nil {
		s.close()
		return nil, e
	}
	if migrateCaptureExtensions(&m.settings) {
		if e = m.store.put("settings", m.settings); e != nil {
			s.close()
			return nil, e
		}
	}
	m.updateLimiter()
	go m.loop()
	return m, nil
}

func (m *Manager) loop() {
	defer close(m.loopDone)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	previous := map[string]int64{}
	for {
		select {
		case <-m.stop:
			return
		case <-tick.C:
			m.mu.Lock()
			for id, t := range m.tasks {
				delta := t.Received - previous[id]
				if delta < 0 {
					delta = 0
				}
				if active(t.State) {
					t.Speed = t.Speed*.6 + float64(delta)*4*.4
				} else {
					t.Speed = 0
				}
				previous[id] = t.Received
			}
			m.scheduleLocked()
			s := m.snapshotLocked()
			m.mu.Unlock()
			if m.onSnapshot != nil {
				m.onSnapshot(s)
			}
		}
	}
}

func (m *Manager) snapshotLocked() Snapshot {
	s := Snapshot{Tasks: []TaskView{}, Settings: m.settings, Protocol: ProtocolVersion}
	s.Settings.CaptureExtensions = append([]string{}, m.settings.CaptureExtensions...)
	s.Settings.ExcludedHosts = append([]string{}, m.settings.ExcludedHosts...)
	for _, t := range m.tasks {
		if t.HandoffState == "aborted" {
			continue
		}
		s.Tasks = append(s.Tasks, view(t))
		if active(t.State) {
			s.ActiveCount++
		}
	}
	sort.Slice(s.Tasks, func(i, j int) bool { return s.Tasks[i].CreatedAt.Before(s.Tasks[j].CreatedAt) })
	return s
}
func (m *Manager) Snapshot() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshotLocked() }

func (m *Manager) scheduleLocked() {
	if m.closing {
		return
	}
	var queue []*Task
	for _, t := range m.tasks {
		if t.State == "queued" && m.runs[t.ID] == nil {
			queue = append(queue, t)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].CreatedAt.Before(queue[j].CreatedAt) })
	for _, t := range queue {
		if len(m.runs) >= m.settings.MaxTasks {
			break
		}
		ctx, cancel := context.WithCancel(context.Background())
		r := &running{cancel, make(chan struct{})}
		m.runs[t.ID] = r
		t.State = "probing"
		t.Error = ""
		t.Received = completedBytes(t)
		go m.run(ctx, t.ID, r)
	}
}

func completedBytes(t *Task) int64 {
	var n int64
	for _, c := range t.Chunks {
		if c.Done {
			n += c.End - c.Start + 1
		}
	}
	return n
}
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

func safeName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = filepath.Base(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("/:", r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if s == "" || s == "." {
		s = "下载文件"
	}
	for len([]byte(s)) > 180 {
		s = string([]rune(s)[:len([]rune(s))-1])
	}
	return s
}

func (m *Manager) uniquePathLocked(dir, name string, own string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		p := filepath.Join(dir, n)
		_, e := os.Lstat(p)
		used := !os.IsNotExist(e)
		for id, t := range m.tasks {
			if id != own && t.Path == p && t.HandoffState != "aborted" {
				used = true
			}
		}
		if !used {
			return p
		}
	}
}

func (m *Manager) Add(ctx context.Context, req Request, prepare bool) (TaskView, error) {
	if e := req.Validate(); e != nil {
		return TaskView{}, e
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return TaskView{}, errors.New("核心正在退出")
	}
	if req.HandoffID != "" {
		for _, t := range m.tasks {
			if t.Request.HandoffID == req.HandoffID {
				if t.HandoffState == "aborted" {
					m.mu.Unlock()
					return TaskView{}, errors.New("浏览器接管已撤销")
				}
				v := view(t)
				m.mu.Unlock()
				return v, nil
			}
		}
	}
	settings := m.settings
	m.mu.Unlock()
	dir := req.Directory
	if dir == "" {
		dir = settings.Directory
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return TaskView{}, errors.New("无法创建下载目录")
	}
	var metadata *probeResult
	if prepare {
		var e error
		metadata, e = probe(ctx, req, settings.Proxy)
		if e != nil {
			return TaskView{}, e
		}
		if strings.HasPrefix(strings.ToLower(metadata.contentType), "text/html") {
			return TaskView{}, errors.New("链接返回网页，已保留浏览器下载；可能需要重新登录")
		}
	}
	u, _ := url.Parse(req.URL)
	name := req.Filename
	if name == "" && metadata != nil {
		name = metadata.name
	}
	if name == "" {
		name, _ = url.PathUnescape(filepath.Base(u.Path))
	}
	name = safeName(name)
	t := &Task{ID: newID(), Name: name, Source: u.Scheme + "://" + u.Host + u.EscapedPath(), State: "queued", Total: -1, Connections: req.Connections, Chunks: []Chunk{}, CreatedAt: time.Now(), Request: req}
	if prepare {
		t.State = "prepared"
		t.HandoffState = "prepared"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Duplicate native messages may have probed concurrently.
	if req.HandoffID != "" {
		for _, old := range m.tasks {
			if old.Request.HandoffID == req.HandoffID {
				if old.HandoffState == "aborted" {
					return TaskView{}, errors.New("浏览器接管已撤销")
				}
				return view(old), nil
			}
		}
	}
	t.Path = m.uniquePathLocked(dir, name, "")
	t.Name = filepath.Base(t.Path)
	t.TempPath = filepath.Join(dir, ".dslq-"+t.ID+".part")
	f, e := os.OpenFile(t.TempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return TaskView{}, errors.New("下载目录不可写")
	}
	f.Close()
	if e = m.store.put(t.ID, t); e != nil {
		os.Remove(t.TempPath)
		return TaskView{}, errors.New("无法保存下载任务")
	}
	m.tasks[t.ID] = t
	return view(t), nil
}

func (m *Manager) Action(id, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return errors.New("任务不存在")
	}
	switch action {
	case "pause":
		if t.State == "completed" || t.State == "prepared" {
			return nil
		}
		t.State = "paused"
		if r := m.runs[id]; r != nil {
			r.cancel()
		}
	case "resume", "retry":
		if t.State == "completed" {
			return nil
		}
		if t.State == "prepared" {
			return errors.New("浏览器尚未提交此任务")
		}
		t.State = "queued"
		t.Error = ""
	case "restart":
		if m.runs[id] != nil {
			return errors.New("请先暂停任务，等待当前请求停止")
		}
		if t.State == "completed" {
			return errors.New("已完成的任务请重新添加")
		}
		t.Chunks = []Chunk{}
		t.Received = 0
		t.Total = -1
		t.ETag = ""
		t.Modified = ""
		t.Resumable = false
		t.Range = false
		t.BodyComplete = false
		t.ForceSingle = false
		t.State = "queued"
		t.Error = ""
		if e := os.Truncate(t.TempPath, 0); e != nil {
			return errors.New("无法重置临时文件")
		}
	default:
		return errors.New("未知任务操作")
	}
	return m.store.put(id, t)
}

func (m *Manager) SetConnections(id string, n int) error {
	if n < 0 || n > 32 {
		return errors.New("并发必须为 1–32，或 0（跟随设置）")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return errors.New("任务不存在")
	}
	t.Connections = n
	t.Request.Connections = n
	return m.store.put(id, t)
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil {
		m.mu.Unlock()
		return nil
	}
	if t.State == "prepared" {
		m.mu.Unlock()
		return errors.New("浏览器交接尚未完成，请稍后移除任务")
	}
	t.State = "paused"
	r := m.runs[id]
	if r != nil {
		r.cancel()
	}
	m.mu.Unlock()
	if r != nil {
		<-r.done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.store.remove(id); e != nil {
		return e
	}
	delete(m.tasks, id)
	if t.State != "completed" {
		os.Remove(t.TempPath)
	}
	return nil
}

func (m *Manager) Handoff(id, action string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var t *Task
	for _, v := range m.tasks {
		if v.Request.HandoffID == id {
			t = v
			break
		}
	}
	if t == nil {
		if action == "abort" {
			// Record cancellation even if an earlier prepare is still probing.
			// The in-flight Add checks this tombstone before creating its file.
			t = &Task{ID: newID(), State: "paused", Total: -1, Chunks: []Chunk{}, CreatedAt: time.Now(), Request: Request{HandoffID: id}, HandoffState: "aborted"}
			if e := m.store.put(t.ID, t); e != nil {
				return nil, e
			}
			m.tasks[t.ID] = t
			return map[string]any{"state": "aborted"}, nil
		}
		if action == "commit" {
			return nil, errors.New("接管任务不存在，无法提交")
		}
		return map[string]any{"state": "missing"}, nil
	}
	switch action {
	case "commit":
		if t.HandoffState == "aborted" {
			return nil, errors.New("接管已撤销")
		}
		if t.HandoffState == "prepared" {
			t.State = "queued"
			t.HandoffState = "committed"
			if e := m.store.put(t.ID, t); e != nil {
				t.State = "prepared"
				t.HandoffState = "prepared"
				return nil, e
			}
		}
	case "abort":
		if t.HandoffState == "prepared" {
			t.State = "paused"
			t.HandoffState = "aborted"
			if e := m.store.put(t.ID, t); e != nil {
				return nil, e
			}
			os.Remove(t.TempPath)
		}
	case "status":
	default:
		return nil, errors.New("未知接管操作")
	}
	return map[string]any{"state": t.HandoffState, "taskId": t.ID}, nil
}

func (m *Manager) updateLimiter() {
	if m.rateChanged != nil {
		close(m.rateChanged)
	}
	m.rateChanged = make(chan struct{})
	limit := rate.Inf
	if m.settings.SpeedLimit == 0 {
		limit = rate.Inf
	} else {
		limit = rate.Limit(m.settings.SpeedLimit)
	}
	m.limiter = rate.NewLimiter(limit, 64<<10)
}

func (m *Manager) waitBandwidth(ctx context.Context, n int) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.mu.Lock()
		limiter, changed := m.limiter, m.rateChanged
		m.mu.Unlock()
		r := limiter.ReserveN(time.Now(), n)
		if !r.OK() {
			return errors.New("限速请求超过缓冲大小")
		}
		delay := r.Delay()
		if delay <= 0 {
			return nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			r.Cancel()
			return ctx.Err()
		case <-changed:
			timer.Stop()
			r.Cancel()
		case <-timer.C:
			return nil
		}
	}
}
func (m *Manager) UpdateSettings(s Settings) error {
	s.CaptureExtensions = NormalizeCaptureExtensions(s.CaptureExtensions)
	if e := s.Validate(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.store.put("settings", s); e != nil {
		return e
	}
	changed := m.settings.SpeedLimit != s.SpeedLimit
	m.settings = s
	if changed {
		m.updateLimiter()
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	m.closing = true
	var waits []chan struct{}
	for id, r := range m.runs {
		if active(m.tasks[id].State) {
			m.tasks[id].State = "queued"
		}
		r.cancel()
		waits = append(waits, r.done)
	}
	m.mu.Unlock()
	for _, d := range waits {
		<-d
	}
	close(m.stop)
	<-m.loopDone
	m.mu.Lock()
	for _, t := range m.tasks {
		m.store.put(t.ID, t)
	}
	m.store.close()
	m.mu.Unlock()
}
