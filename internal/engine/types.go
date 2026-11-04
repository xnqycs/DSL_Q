package engine

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ChunkSize int64 = 4 << 20
const ProtocolVersion = 1

type Settings struct {
	NotifyOnStart     bool     `json:"notifyOnStart"`
	NotifyOnFinish    bool     `json:"notifyOnFinish"`
	Connections       int      `json:"connections"`
	MaxTasks          int      `json:"maxTasks"`
	SpeedLimit        int64    `json:"speedLimit"`
	Directory         string   `json:"directory"`
	Proxy             string   `json:"proxy"`
	PreventIdleSleep  bool     `json:"preventIdleSleepWhileDownloading"`
	AutoCapture       bool     `json:"autoCapture"`
	CaptureMinBytes   int64    `json:"captureMinBytes"`
	CaptureExtensions []string `json:"captureExtensions"`
	ExcludedHosts     []string `json:"excludedHosts"`
}

func Defaults() Settings {
	h, _ := os.UserHomeDir()
	return Settings{NotifyOnStart: true, NotifyOnFinish: true, Connections: 8, MaxTasks: 3, Directory: filepath.Join(h, "Downloads"), CaptureMinBytes: 20 << 20,
		CaptureExtensions: DefaultCaptureExtensions(), ExcludedHosts: []string{}}
}

func (s Settings) Validate() error {
	if s.Connections < 1 || s.Connections > 32 {
		return errors.New("分段并发必须为 1–32")
	}
	if s.MaxTasks < 1 || s.MaxTasks > 16 {
		return errors.New("同时下载任务数必须为 1–16")
	}
	if s.SpeedLimit < 0 || s.CaptureMinBytes < 0 {
		return errors.New("速度限制和接管阈值不能为负数")
	}
	if !filepath.IsAbs(s.Directory) {
		return errors.New("请选择绝对路径的下载目录")
	}
	if s.Proxy != "" {
		u, e := url.Parse(s.Proxy)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return errors.New("代理必须是 HTTP、HTTPS 或 SOCKS5 地址")
		}
	}
	return nil
}

type Request struct {
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Filename    string            `json:"filename,omitempty"`
	Directory   string            `json:"directory,omitempty"`
	Connections int               `json:"connections,omitempty"`
	HandoffID   string            `json:"handoffId,omitempty"`
}

func (r *Request) Validate() error {
	u, e := url.Parse(r.URL)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("仅支持不含内嵌用户名密码的 HTTP/HTTPS 链接")
	}
	if r.Connections < 0 || r.Connections > 32 {
		return errors.New("任务并发必须为 1–32，或 0（跟随设置）")
	}
	if r.Directory != "" && !filepath.IsAbs(r.Directory) {
		return errors.New("下载目录必须为绝对路径")
	}
	allowed := map[string]bool{"Cookie": true, "Referer": true, "User-Agent": true, "Authorization": true, "Accept": true, "Accept-Language": true, "Origin": true}
	clean := map[string]string{}
	for k, v := range r.Headers {
		k = http.CanonicalHeaderKey(k)
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("请求头包含非法换行")
		}
		if allowed[k] {
			clean[k] = v
		}
	}
	r.Headers = clean
	return nil
}

type Chunk struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Done  bool  `json:"done"`
}

type Task struct {
	ContentType       string    `json:"contentType,omitempty"`
	RunID             string    `json:"runId,omitempty"`
	Started           bool      `json:"started"`
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Source            string    `json:"source"`
	Path              string    `json:"path"`
	State             string    `json:"state"`
	Total             int64     `json:"total"`
	Received          int64     `json:"received"`
	Speed             float64   `json:"speed"`
	Connections       int       `json:"connections"`
	ActiveConnections int       `json:"activeConnections"`
	Chunks            []Chunk   `json:"chunks"`
	Error             string    `json:"error"`
	CreatedAt         time.Time `json:"createdAt"`
	Request           Request   `json:"request"`
	ETag              string    `json:"etag"`
	Modified          string    `json:"modified"`
	Resumable         bool      `json:"resumable"`
	Range             bool      `json:"range"`
	TempPath          string    `json:"tempPath"`
	HandoffState      string    `json:"handoffState,omitempty"`
	BodyComplete      bool      `json:"bodyComplete"`
	ForceSingle       bool      `json:"forceSingle"`
}

// Snapshot intentionally excludes credentials and internal resume metadata.
type TaskView struct {
	ContentType       string    `json:"contentType"`
	RunID             string    `json:"runId"`
	Started           bool      `json:"started"`
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Source            string    `json:"source"`
	Path              string    `json:"path"`
	State             string    `json:"state"`
	Total             int64     `json:"total"`
	Received          int64     `json:"received"`
	Speed             float64   `json:"speed"`
	Connections       int       `json:"connections"`
	ActiveConnections int       `json:"activeConnections"`
	Chunks            []Chunk   `json:"chunks"`
	Error             string    `json:"error"`
	CreatedAt         time.Time `json:"createdAt"`
	Resumable         bool      `json:"resumable"`
}

func active(s string) bool {
	return s == "probing" || s == "downloading" || s == "retrying" || s == "finishing"
}

func view(t *Task) TaskView {
	c := append([]Chunk{}, t.Chunks...)
	return TaskView{t.ContentType, t.RunID, t.Started, t.ID, t.Name, t.Source, t.Path, t.State, t.Total, t.Received, t.Speed, t.Connections, t.ActiveConnections, c, t.Error, t.CreatedAt, t.Resumable}
}
