package rpc

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dslq/internal/engine"
)

const MaxMessage = 1 << 20

type Request struct {
	JSONRPC  string          `json:"jsonrpc"`
	Protocol int             `json:"protocol"`
	ID       json.RawMessage `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
}
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Response struct {
	JSONRPC  string          `json:"jsonrpc"`
	Protocol int             `json:"protocol"`
	ID       json.RawMessage `json:"id,omitempty"`
	Result   any             `json:"result,omitempty"`
	Error    *Error          `json:"error,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   any             `json:"params,omitempty"`
}

func reply(r Request, v any, e error) Response {
	p := Response{JSONRPC: "2.0", Protocol: 1, ID: r.ID, Result: v}
	if e != nil {
		p.Result = nil
		p.Error = &Error{-32000, e.Error()}
	}
	return p
}

func Dispatch(ctx context.Context, m *engine.Manager, r Request, browser bool) Response {
	if r.JSONRPC != "2.0" || r.Protocol != 1 {
		return reply(r, nil, errors.New("通信协议版本不兼容，请更新客户端和扩展"))
	}
	if browser && !strings.HasPrefix(r.Method, "browser.") {
		return reply(r, nil, errors.New("浏览器接口不允许此操作"))
	}
	var value any
	var err error
	switch r.Method {
	case "snapshot":
		value = m.Snapshot()
	case "tasks.add", "browser.prepare":
		var req engine.Request
		if err = json.Unmarshal(r.Params, &req); err == nil {
			if r.Method == "browser.prepare" && req.HandoffID == "" {
				err = errors.New("缺少接管标识")
				break
			}
			if browser {
				req.Directory = ""
				req.Connections = 0
			}
			value, err = m.Add(ctx, req, r.Method == "browser.prepare")
		}
	case "tasks.action":
		var p struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err = json.Unmarshal(r.Params, &p); err == nil {
			err = m.Action(p.ID, p.Action)
		}
		value = true
	case "tasks.connections":
		var p struct {
			ID          string `json:"id"`
			Connections int    `json:"connections"`
		}
		if err = json.Unmarshal(r.Params, &p); err == nil {
			err = m.SetConnections(p.ID, p.Connections)
		}
		value = true
	case "tasks.delete":
		var p struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(r.Params, &p); err == nil {
			err = m.Delete(p.ID)
		}
		value = true
	case "settings.update":
		var s engine.Settings
		if err = json.Unmarshal(r.Params, &s); err == nil {
			err = m.UpdateSettings(s)
		}
		value = true
	case "browser.settings":
		s := m.Snapshot().Settings
		value = map[string]any{"autoCapture": s.AutoCapture, "captureMinBytes": s.CaptureMinBytes, "captureExtensions": s.CaptureExtensions, "excludedHosts": s.ExcludedHosts}
	case "browser.ping":
		value = map[string]any{"name": "DSL Q", "protocol": 1}
	case "browser.commit", "browser.abort", "browser.status":
		var p struct {
			HandoffID string `json:"handoffId"`
		}
		if err = json.Unmarshal(r.Params, &p); err == nil {
			value, err = m.Handoff(p.HandoffID, strings.TrimPrefix(r.Method, "browser."))
		}
	default:
		err = errors.New("未知接口")
	}
	return reply(r, value, err)
}

func Serve(input io.Reader, output io.Writer, dir string) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), MaxMessage)
	if !scanner.Scan() {
		return errors.New("未收到初始化请求")
	}
	var init Request
	if e := json.Unmarshal(scanner.Bytes(), &init); e != nil {
		return e
	}
	var p struct {
		Key string `json:"key"`
	}
	if init.Method != "initialize" || init.Protocol != 1 || json.Unmarshal(init.Params, &p) != nil {
		return errors.New("初始化协议错误")
	}
	key, e := base64.StdEncoding.DecodeString(p.Key)
	if e != nil {
		return errors.New("初始化密钥格式错误")
	}
	var writeMu sync.Mutex
	write := func(v Response) { writeMu.Lock(); defer writeMu.Unlock(); _ = json.NewEncoder(output).Encode(v) }
	// Coalesce snapshots so a slow UI cannot block downloads or shutdown.
	events := make(chan engine.Snapshot, 1)
	eventDone := make(chan struct{})
	defer close(eventDone)
	go func() {
		for {
			select {
			case s := <-events:
				write(Response{JSONRPC: "2.0", Protocol: 1, Method: "snapshot", Params: s})
			case <-eventDone:
				return
			}
		}
	}()
	m, e := engine.New(dir, key, func(s engine.Snapshot) {
		select {
		case events <- s:
		default:
		}
	})
	for i := range key {
		key[i] = 0
	}
	if e != nil {
		write(reply(init, nil, e))
		return e
	}
	defer m.Close()
	socketPath := filepath.Join(dir, "engine.sock")
	_ = os.Remove(socketPath)
	listener, e := net.Listen("unix", socketPath)
	if e != nil {
		write(reply(init, nil, errors.New("无法创建本地通信端点")))
		return e
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if e = os.Chmod(socketPath, 0600); e != nil {
		return e
	}
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go serveBrowser(conn, m)
		}
	}()
	write(reply(init, m.Snapshot(), nil))
	var workers sync.WaitGroup
	for scanner.Scan() {
		var r Request
		if e = json.Unmarshal(scanner.Bytes(), &r); e != nil {
			write(reply(r, nil, errors.New("消息不是有效 JSON")))
			continue
		}
		if r.Method == "shutdown" {
			workers.Wait()
			m.Close()
			write(reply(r, true, nil))
			return nil
		}
		workers.Add(1)
		go func(r Request) {
			defer workers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			write(Dispatch(ctx, m, r, false))
		}(r)
	}
	workers.Wait()
	return scanner.Err()
}

func serveBrowser(c net.Conn, m *engine.Manager) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(25 * time.Second))
	s := bufio.NewScanner(c)
	s.Buffer(make([]byte, 64<<10), MaxMessage)
	if !s.Scan() {
		return
	}
	var r Request
	if json.Unmarshal(s.Bytes(), &r) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	_ = json.NewEncoder(c).Encode(Dispatch(ctx, m, r, true))
}
