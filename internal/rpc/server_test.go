package rpc

import (
	"context"
	"dslq/internal/engine"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProtocolAndBrowserBoundary(t *testing.T) {
	m, e := engine.New(filepath.Join(t.TempDir(), "data"), make([]byte, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	request := Request{JSONRPC: "2.0", Protocol: 1, ID: json.RawMessage(`"test"`), Method: "snapshot"}
	if Dispatch(context.Background(), m, request, true).Error == nil {
		t.Fatal("browser read private snapshot")
	}
	if Dispatch(context.Background(), m, request, false).Error != nil {
		t.Fatal("desktop snapshot rejected")
	}
	request.Protocol = 2
	if Dispatch(context.Background(), m, request, false).Error == nil {
		t.Fatal("incompatible protocol accepted")
	}
	request.Protocol = 1
	request.Method = "browser.settings"
	response := Dispatch(context.Background(), m, request, true)
	data, _ := json.Marshal(response.Result)
	var fields map[string]any
	json.Unmarshal(data, &fields)
	if _, ok := fields["proxy"]; ok {
		t.Fatal("browser received private settings")
	}
	request.Method = "browser.prepare"
	request.Params = json.RawMessage(`{"url":"http://localhost/file"}`)
	if Dispatch(context.Background(), m, request, true).Error == nil {
		t.Fatal("handoff without id accepted")
	}
}
