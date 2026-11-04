package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dslq/internal/rpc"
)

var extensionID string // injected from the extension's pinned public key at build time

func main() {
	if len(os.Args) < 2 || extensionID == "" || os.Args[1] != "chrome-extension://"+extensionID+"/" {
		os.Exit(1)
	}
	for {
		var length uint32
		if e := binary.Read(os.Stdin, binary.LittleEndian, &length); e != nil {
			return
		}
		if length == 0 || length > rpc.MaxMessage {
			return
		}
		b := make([]byte, length)
		if _, e := io.ReadFull(os.Stdin, b); e != nil {
			return
		}
		var req rpc.Request
		if json.Unmarshal(b, &req) != nil {
			return
		}
		response, e := forward(b, req.Method != "browser.ping")
		if e != nil {
			response, _ = json.Marshal(rpc.Response{JSONRPC: "2.0", Protocol: 1, ID: req.ID, Error: &rpc.Error{Code: -32001, Message: "客户端未连接，请打开 DSL Q 并检查浏览器集成设置"}})
		}
		if len(response) > rpc.MaxMessage {
			return
		}
		if binary.Write(os.Stdout, binary.LittleEndian, uint32(len(response))) != nil {
			return
		}
		if _, e = os.Stdout.Write(response); e != nil {
			return
		}
	}
}

func forward(b []byte, launch bool) ([]byte, error) {
	h, _ := os.UserHomeDir()
	socket := filepath.Join(h, "Library", "Application Support", "DSL Q", "engine.sock")
	testMode := os.Getenv("DSLQ_INTEGRATION_TEST") == "1" && os.Getenv("DSLQ_TEST_DATA_DIR") != ""
	if testMode {
		socket = filepath.Join(os.Getenv("DSLQ_TEST_DATA_DIR"), "engine.sock")
	}
	connect := func() (net.Conn, error) { return net.DialTimeout("unix", socket, time.Second) }
	c, e := connect()
	if e != nil && launch {
		executable, _ := os.Executable()
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
		if !strings.HasSuffix(bundle, ".app") {
			return nil, errors.New("host must be inside application bundle")
		}
		args := []string{"-g", bundle}
		if testMode {
			args = []string{"-g", "--env", "DSLQ_TEST_DATA_DIR=" + os.Getenv("DSLQ_TEST_DATA_DIR"), "--env", "DSLQ_TEST_KEY=" + os.Getenv("DSLQ_TEST_KEY"), bundle, "--args", "--integration-test"}
		}
		if e = exec.Command("/usr/bin/open", args...).Run(); e != nil {
			return nil, e
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			c, e = connect()
			if e == nil {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	if e != nil {
		return nil, e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(22 * time.Second))
	if _, e = c.Write(append(b, '\n')); e != nil {
		return nil, e
	}
	s := bufio.NewScanner(c)
	s.Buffer(make([]byte, 64<<10), rpc.MaxMessage)
	if !s.Scan() {
		return nil, errors.New("empty response")
	}
	return append([]byte{}, s.Bytes()...), nil
}
