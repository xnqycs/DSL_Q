package engine

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Preview struct {
	Name        string `json:"name"`
	Total       int64  `json:"total"`
	ContentType string `json:"contentType"`
	Source      string `json:"source"`
	Resumable   bool   `json:"resumable"`
}

func (m *Manager) Preview(ctx context.Context, req Request) (Preview, error) {
	if e := req.Validate(); e != nil {
		return Preview{}, e
	}
	m.mu.Lock()
	proxy := m.settings.Proxy
	m.mu.Unlock()
	p, e := probe(ctx, req, proxy)
	if e != nil {
		return Preview{}, e
	}
	u, _ := url.Parse(req.URL)
	name := p.name
	if name == "" {
		name, _ = url.PathUnescape(filepath.Base(u.Path))
	}
	return Preview{safeName(name), p.total, p.contentType, u.Scheme + "://" + u.Host + u.EscapedPath(), p.resumable && p.total > ChunkSize}, nil
}

type Confirmation struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Directory   string `json:"directory"`
	Connections int    `json:"connections"`
}

func (m *Manager) allocateLocked(t *Task, dir, name string) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return errors.New("无法创建下载目录")
	}
	t.Path = m.uniquePathLocked(dir, name, t.ID)
	t.Name = filepath.Base(t.Path)
	t.TempPath = filepath.Join(dir, ".dslq-"+t.ID+".part")
	f, e := os.OpenFile(t.TempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("下载目录不可写")
	}
	if e = f.Close(); e != nil {
		_ = os.Remove(t.TempPath)
		return errors.New("无法创建临时文件")
	}
	return nil
}

func (m *Manager) Confirm(p Confirmation) (TaskView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[p.ID]
	if t == nil || t.State == "discarded" {
		return TaskView{}, errors.New("待确认任务已取消或不存在")
	}
	if m.closing {
		return TaskView{}, errors.New("核心正在退出")
	}
	if t.State == "prepared" {
		return TaskView{}, errors.New("浏览器交接尚未完成")
	}
	if t.State != "awaiting_confirmation" {
		return view(t), nil
	} // double-click/retry is idempotent
	if p.Connections < 0 || p.Connections > 32 {
		return TaskView{}, errors.New("任务并发必须为 0–32")
	}
	if !filepath.IsAbs(p.Directory) {
		return TaskView{}, errors.New("请选择绝对路径的下载目录")
	}
	if strings.TrimSpace(p.Filename) == "" {
		return TaskView{}, errors.New("请输入文件名")
	}
	// Releases made before confirmation allocated an empty part during prepare.
	// Reclaim only that task-owned empty file; never replace unexpected data.
	if t.TempPath != "" {
		info, err := os.Stat(t.TempPath)
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() != 0 || filepath.Base(t.TempPath) != ".dslq-"+t.ID+".part" {
				return TaskView{}, errors.New("旧版待确认任务的临时文件状态异常，请取消后重新添加")
			}
			if err = os.Remove(t.TempPath); err != nil {
				return TaskView{}, errors.New("无法清理旧版临时文件，请检查目录权限")
			}
		} else if !os.IsNotExist(err) {
			return TaskView{}, errors.New("无法检查旧版临时文件")
		}
	}
	next := *t
	if e := m.allocateLocked(&next, p.Directory, safeName(p.Filename)); e != nil {
		return TaskView{}, e
	}
	next.Request.Directory = p.Directory
	next.Request.Filename = next.Name
	next.Request.Connections = p.Connections
	next.Connections = p.Connections
	next.State = "queued"
	if e := m.store.put(next.ID, &next); e != nil {
		os.Remove(next.TempPath)
		return TaskView{}, errors.New("无法保存下载任务")
	}
	*t = next
	return view(t), nil
}

func (m *Manager) cancelTask(id string) error {
	m.mu.Lock()
	t := m.tasks[id]
	if t == nil || t.State == "completed" || t.State == "discarded" {
		m.mu.Unlock()
		return nil
	}
	if t.State == "prepared" || t.State == "cancelling" {
		m.mu.Unlock()
		return errors.New("浏览器交接尚未完成")
	}
	old := t.State
	targetState := "cancelled"
	if t.State == "awaiting_confirmation" {
		targetState = "discarded"
	}
	t.State = "cancelling"
	if e := m.store.put(id, t); e != nil {
		t.State = old
		m.mu.Unlock()
		return e
	}
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
	t.State = targetState
	// Serialize cleanup with restart/resume. Completion won the lock before cancel
	// only if it was already installed; that case returned above without removal.
	if t.TempPath != "" {
		if e := os.Remove(t.TempPath); e != nil && !os.IsNotExist(e) {
			t.Error = "无法清理临时文件"
			m.store.put(id, t)
			return errors.New(t.Error)
		}
	}
	t.Speed = 0
	t.ActiveConnections = 0
	t.Chunks = []Chunk{}
	t.Received = 0
	t.BodyComplete = false
	if t.State == "discarded" {
		t.Request = Request{HandoffID: t.Request.HandoffID}
	} // retain handoff receipt, not credentials
	return m.store.put(id, t)
}
