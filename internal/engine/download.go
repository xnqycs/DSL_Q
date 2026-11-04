package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type probeResult struct {
	total                             int64
	ranges                            bool
	etag, modified, name, contentType string
	resumable                         bool
}

var errRangeIgnored = errors.New("服务器忽略分段，改用单路下载")

type transferError struct {
	message string
	retry   bool
	delay   time.Duration
}

func (e *transferError) Error() string { return e.message }

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	return c.Conn.Read(p)
}

func client(proxy string) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	t := &http.Transport{DisableCompression: true, ForceAttemptHTTP2: true, MaxIdleConns: 128, MaxIdleConnsPerHost: 32, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, e := dialer.DialContext(ctx, network, addr)
			if e != nil {
				return nil, e
			}
			return idleConn{c}, nil
		}}
	if proxy != "" {
		u, _ := url.Parse(proxy)
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("重定向次数过多")
		}
		original := via[0].URL
		if !strings.EqualFold(req.URL.Host, original.Host) || req.URL.Scheme != original.Scheme {
			for _, k := range []string{"Authorization", "Cookie", "Referer", "Origin"} {
				req.Header.Del(k)
			}
		}
		return nil
	}}
}

func request(ctx context.Context, c *http.Client, r Request, start, end int64, validator string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if e != nil {
		return nil, errors.New("链接格式错误")
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "DSLQ/0.1")
	}
	req.Header.Set("Accept-Encoding", "identity")
	if start >= 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		if validator != "" {
			req.Header.Set("If-Range", validator)
		}
	}
	resp, e := c.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transferError{"网络连接失败，请检查网络或代理", true, 0}
	}
	return resp, nil
}

func statusError(r *http.Response) error {
	message := fmt.Sprintf("服务器返回 HTTP %d", r.StatusCode)
	if r.StatusCode == 401 || r.StatusCode == 403 {
		message = "下载授权已失效，请在浏览器重新下载以更新凭据"
	}
	retry := r.StatusCode == 408 || r.StatusCode == 429 || r.StatusCode >= 500
	var delay time.Duration
	if v := r.Header.Get("Retry-After"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			delay = time.Duration(n) * time.Second
		} else if t, e := http.ParseTime(v); e == nil {
			delay = time.Until(t)
		}
	}
	return &transferError{message, retry, delay}
}

func parseRange(s string) (start, end, total int64, err error) {
	var extra string
	n, e := fmt.Sscanf(s, "bytes %d-%d/%d%s", &start, &end, &total, &extra)
	if n != 3 || (e != nil && e != io.EOF) || start < 0 || end < start || total <= end {
		return 0, 0, 0, errors.New("服务器返回无效的 Content-Range")
	}
	return start, end, total, nil
}

func probe(ctx context.Context, r Request, proxy string) (*probeResult, error) {
	c := client(proxy)
	defer c.CloseIdleConnections()
	resp, e := request(ctx, c, r, 0, 0, "")
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	p := &probeResult{total: resp.ContentLength, etag: resp.Header.Get("ETag"), contentType: resp.Header.Get("Content-Type")}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resp.Header.Get("Content-Range") == "bytes */0" {
		p.total = 0
	} else if resp.StatusCode == 206 {
		a, b, total, e := parseRange(resp.Header.Get("Content-Range"))
		if e != nil || a != 0 || b != 0 {
			return nil, errors.New("服务器未正确响应范围探测")
		}
		if resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
			return nil, errors.New("服务器压缩响应不支持安全分段")
		}
		p.total = total
		p.ranges = true
	} else if resp.StatusCode != 200 {
		return nil, statusError(resp)
	}
	if _, params, e := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); e == nil {
		p.name = params["filename"]
	}
	// An HTTP date is a strong validator only when Date is at least 60 s newer.
	if p.etag != "" && !strings.HasPrefix(p.etag, "W/") {
		p.resumable = p.ranges
	} else {
		p.etag = ""
		lm, le := http.ParseTime(resp.Header.Get("Last-Modified"))
		date, de := http.ParseTime(resp.Header.Get("Date"))
		if le == nil && de == nil && date.Sub(lm) >= 60*time.Second {
			p.modified = resp.Header.Get("Last-Modified")
			p.resumable = p.ranges
		}
	}
	return p, nil
}

func (m *Manager) run(ctx context.Context, id string, r *running) {
	err := m.download(ctx, id)
	m.mu.Lock()
	t := m.tasks[id]
	if t != nil {
		t.ActiveConnections = 0
		t.Speed = 0
		if err != nil && ctx.Err() == nil && active(t.State) {
			t.State = "failed"
			t.Error = err.Error()
		}
		if err != nil {
			t.Received = completedBytes(t)
		}
		if e := m.store.put(id, t); e != nil {
			t.State = "failed"
			t.Error = "无法保存任务状态，请检查磁盘空间"
		}
	}
	delete(m.runs, id)
	close(r.done)
	m.mu.Unlock()
}

func (m *Manager) download(ctx context.Context, id string) error {
	m.mu.Lock()
	t := m.tasks[id]
	req := t.Request
	proxy := m.settings.Proxy
	bodyDone := t.BodyComplete
	m.mu.Unlock()
	if bodyDone {
		return m.finalize(id)
	}
	var p *probeResult
	e := retry(ctx, func() error { var e error; p, e = probe(ctx, req, proxy); return e }, nil)
	if e != nil {
		return e
	}
	m.mu.Lock()
	t = m.tasks[id]
	if ctx.Err() != nil {
		m.mu.Unlock()
		return ctx.Err()
	}
	resume := completedBytes(t) > 0
	if resume && t.Resumable && (p.total != t.Total || p.etag != t.ETag || p.modified != t.Modified || !p.ranges) {
		m.mu.Unlock()
		return errors.New("源文件已经变化，请选择“从头下载”，不会混合新旧内容")
	}
	if !t.Resumable || !p.resumable {
		t.Chunks = []Chunk{}
		t.Received = 0
		resume = false
	}
	t.ContentType = p.contentType
	t.Total = p.total
	t.ETag = p.etag
	t.Modified = p.modified
	t.Range = p.ranges && p.resumable && p.total > ChunkSize && !t.ForceSingle
	t.Resumable = t.Range
	if !resume && p.name != "" && req.Filename == "" {
		t.Path = m.uniquePathLocked(filepath.Dir(t.Path), safeName(p.name), id)
		t.Name = filepath.Base(t.Path)
	}
	if t.Range && len(t.Chunks) == 0 {
		for start := int64(0); start < p.total; start += ChunkSize {
			end := start + ChunkSize - 1
			if end >= p.total {
				end = p.total - 1
			}
			t.Chunks = append(t.Chunks, Chunk{Start: start, End: end})
		}
	}
	flags := os.O_RDWR | os.O_CREATE
	if !resume {
		flags |= os.O_TRUNC
	}
	f, e := os.OpenFile(t.TempPath, flags, 0600)
	if e != nil {
		m.mu.Unlock()
		return errors.New("无法打开临时文件，请检查目录权限")
	}
	if resume {
		info, err := f.Stat()
		if err != nil {
			f.Close()
			m.mu.Unlock()
			return errors.New("无法读取临时文件")
		}
		for _, c := range t.Chunks {
			if c.Done && info.Size() <= c.End {
				f.Close()
				m.mu.Unlock()
				return errors.New("临时文件不完整，请从头下载")
			}
		}
	}
	segmented := t.Range
	t.State = "downloading"
	t.Received = completedBytes(t)
	e = m.store.put(id, t)
	m.mu.Unlock()
	defer f.Close()
	if e != nil {
		return errors.New("无法保存续传元数据")
	}
	c := client(proxy)
	defer c.CloseIdleConnections()
	if segmented {
		e = m.segments(ctx, id, f, c)
		if errors.Is(e, errRangeIgnored) && ctx.Err() == nil {
			m.mu.Lock()
			t = m.tasks[id]
			t.ForceSingle = true
			t.Range = false
			t.Resumable = false
			t.Chunks = []Chunk{}
			t.Received = 0
			e = m.store.put(id, t)
			m.mu.Unlock()
			if e == nil {
				e = m.sequential(ctx, id, f, c)
			}
		}
	} else {
		e = m.sequential(ctx, id, f, c)
	}
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e = f.Sync(); e != nil {
		return errors.New("文件同步失败，请检查磁盘空间")
	}
	m.mu.Lock()
	t = m.tasks[id]
	if ctx.Err() != nil {
		m.mu.Unlock()
		return ctx.Err()
	}
	t.State = "finishing"
	t.BodyComplete = true
	e = m.store.put(id, t)
	m.mu.Unlock()
	if e != nil {
		return errors.New("无法保存完成状态")
	}
	return m.finalize(id)
}

func retry(ctx context.Context, fn func() error, onWait func(bool)) error {
	for attempt := 0; ; attempt++ {
		e := fn()
		if e == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var te *transferError
		if !errors.As(e, &te) || !te.retry || attempt >= 3 {
			return e
		}
		delay := time.Second * time.Duration(1<<attempt)
		if te.delay > delay {
			delay = te.delay
		}
		if onWait != nil {
			onWait(true)
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		if onWait != nil {
			onWait(false)
		}
	}
}

type chunkResult struct {
	index int
	err   error
}

func (m *Manager) segments(ctx context.Context, id string, f *os.File, c *http.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan chunkResult, 32)
	busy := map[int]bool{}
	var first error
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		t := m.tasks[id]
		limit := t.Connections
		if limit == 0 {
			limit = m.settings.Connections
		}
		if first == nil && ctx.Err() == nil {
			for i, ch := range t.Chunks {
				if len(busy) >= limit {
					break
				}
				if ch.Done || busy[i] {
					continue
				}
				busy[i] = true
				req := t.Request
				validator := t.ETag
				if validator == "" {
					validator = t.Modified
				}
				total := t.Total
				go func(i int, ch Chunk) {
					err := retry(ctx, func() error { return m.fetchChunk(ctx, id, f, c, req, ch, total, validator) }, nil)
					results <- chunkResult{i, err}
				}(i, ch)
			}
		}
		t.ActiveConnections = len(busy)
		remaining := false
		for _, ch := range t.Chunks {
			if !ch.Done {
				remaining = true
				break
			}
		}
		m.mu.Unlock()
		if len(busy) == 0 {
			if first != nil {
				return first
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !remaining {
				return nil
			}
		}
		select {
		case result := <-results:
			delete(busy, result.index)
			if result.err != nil && first == nil {
				first = result.err
				cancel()
			}
			if result.err == nil {
				m.mu.Lock()
				m.tasks[id].Chunks[result.index].Done = true
				if e := m.store.put(id, m.tasks[id]); e != nil && first == nil {
					first = errors.New("无法保存分段进度")
					cancel()
				}
				m.mu.Unlock()
			}
		case <-ticker.C:
		}
	}
}

func (m *Manager) fetchChunk(ctx context.Context, id string, f *os.File, c *http.Client, req Request, ch Chunk, total int64, validator string) error {
	resp, e := request(ctx, c, req, ch.Start, ch.End, validator)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		if (strings.HasPrefix(validator, "\"") && resp.Header.Get("ETag") == validator) || (!strings.HasPrefix(validator, "\"") && resp.Header.Get("Last-Modified") == validator) {
			return errRangeIgnored
		}
		return errors.New("服务器已忽略分段或源文件发生变化，请从头下载")
	}
	if resp.StatusCode != 206 {
		return statusError(resp)
	}
	a, b, n, e := parseRange(resp.Header.Get("Content-Range"))
	if e != nil || a != ch.Start || b != ch.End || n != total {
		return errors.New("分段响应范围不匹配，已停止下载")
	}
	if v := resp.Header.Get("Content-Encoding"); v != "" && v != "identity" {
		return errors.New("分段响应被压缩，已停止下载")
	}
	if v := resp.Header.Get("ETag"); strings.HasPrefix(validator, "\"") && v != "" && v != validator {
		return errors.New("分段 ETag 已变化，已停止下载")
	}
	var transferred int64
	defer func() {
		if e != nil {
			m.mu.Lock()
			m.tasks[id].Received -= transferred
			m.mu.Unlock()
		}
	}()
	transferred, e = m.copy(ctx, id, f, resp.Body, ch.Start, ch.End-ch.Start+1)
	if e != nil {
		return e
	}
	var extra [1]byte
	nExtra, readErr := resp.Body.Read(extra[:])
	if nExtra != 0 || readErr != io.EOF {
		e = errors.New("分段响应长度不匹配")
		return e
	}
	if e = f.Sync(); e != nil {
		return errors.New("分段写入失败，请检查磁盘空间")
	}
	return nil
}

func (m *Manager) sequential(ctx context.Context, id string, f *os.File, c *http.Client) error {
	m.mu.Lock()
	req := m.tasks[id].Request
	expected := m.tasks[id].Total
	m.tasks[id].ActiveConnections = 1
	m.mu.Unlock()
	return retry(ctx, func() error {
		if e := f.Truncate(0); e != nil {
			return errors.New("无法写入临时文件")
		}
		m.mu.Lock()
		m.tasks[id].Received = 0
		m.mu.Unlock()
		if expected == 0 {
			return nil
		}
		resp, e := request(ctx, c, req, -1, -1, "")
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return statusError(resp)
		}
		if v := resp.Header.Get("Content-Encoding"); v != "" && v != "identity" {
			return errors.New("服务器未提供原始文件响应")
		}
		if expected >= 0 && resp.ContentLength >= 0 && resp.ContentLength != expected {
			return errors.New("源文件大小已变化，请从头下载")
		}
		n, e := m.copy(ctx, id, f, resp.Body, 0, -1)
		if e != nil {
			return e
		}
		if expected >= 0 && n != expected {
			return &transferError{"下载内容不完整", true, 0}
		}
		m.mu.Lock()
		m.tasks[id].Total = n
		m.mu.Unlock()
		return nil
	}, func(wait bool) {
		m.mu.Lock()
		t := m.tasks[id]
		if active(t.State) {
			if wait {
				t.State = "retrying"
			} else {
				t.State = "downloading"
			}
		}
		m.mu.Unlock()
	})
}

func (m *Manager) copy(ctx context.Context, id string, f *os.File, reader io.Reader, offset, length int64) (int64, error) {
	buffer := make([]byte, 32<<10)
	var count int64
	for length < 0 || count < length {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		b := buffer
		if length >= 0 && int64(len(b)) > length-count {
			b = b[:length-count]
		}
		n, e := reader.Read(b)
		if n > 0 {
			if err := m.waitBandwidth(ctx, n); err != nil {
				return count, err
			}
			written, err := f.WriteAt(b[:n], offset+count)
			count += int64(written)
			m.mu.Lock()
			m.tasks[id].Received += int64(written)
			m.mu.Unlock()
			if err != nil || written != n {
				return count, errors.New("写入失败，请检查磁盘剩余空间及目录权限")
			}
		}
		if e == io.EOF {
			if length >= 0 && count < length {
				return count, &transferError{"连接提前结束，将重试", true, 0}
			}
			return count, nil
		}
		if e != nil {
			if ctx.Err() != nil {
				return count, ctx.Err()
			}
			return count, &transferError{"读取下载数据失败，将重试", true, 0}
		}
	}
	return count, nil
}

func (m *Manager) finalize(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if !active(t.State) {
		return context.Canceled
	}
	stamp := fmt.Sprintf("0081;%x;DSL Q;", time.Now().Unix())
	if e := unix.Setxattr(t.TempPath, "com.apple.quarantine", []byte(stamp), 0); e != nil {
		return errors.New("无法设置 macOS 下载隔离属性，请更换保存目录后重试")
	}
	// Link is an atomic no-replace install on the same filesystem. A crash
	// between link/unlink is recovered by comparing the file identities.
	for {
		if e := os.Link(t.TempPath, t.Path); e == nil {
			break
		} else if !os.IsExist(e) {
			return errors.New("无法安装已完成文件，请检查目录权限")
		}
		a, ea := os.Stat(t.TempPath)
		b, eb := os.Stat(t.Path)
		if ea == nil && eb == nil && os.SameFile(a, b) {
			break
		}
		t.Path = m.uniquePathLocked(filepath.Dir(t.Path), t.Name, id)
		t.Name = filepath.Base(t.Path)
		if e := m.store.put(id, t); e != nil {
			return errors.New("无法保存目标文件位置")
		}
	}
	t.State = "completed"
	t.Received = t.Total
	t.Speed = 0
	t.Error = ""
	t.ActiveConnections = 0
	if e := m.store.put(id, t); e != nil {
		return e
	}
	_ = os.Remove(t.TempPath)
	return nil
}
