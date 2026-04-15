package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
)

// stubEngine is a minimal Engine implementation for protocol-level tests.
// Most protocol tests don't exercise tools, so defaults are fine.
type stubEngine struct {
	rememberFn func(context.Context, memory.Memory) (engine.RememberResult, error)
	recallFn   func(context.Context, engine.RecallOptions) ([]memory.Memory, error)
	forgetFn   func(context.Context, string, string) error
}

func (s *stubEngine) Remember(ctx context.Context, m memory.Memory) (engine.RememberResult, error) {
	if s.rememberFn != nil {
		return s.rememberFn(ctx, m)
	}
	return engine.RememberResult{Path: "facts/stub.md", Layer: memory.LayerFact}, nil
}

func (s *stubEngine) Recall(ctx context.Context, opts engine.RecallOptions) ([]memory.Memory, error) {
	if s.recallFn != nil {
		return s.recallFn(ctx, opts)
	}
	return nil, nil
}

func (s *stubEngine) Forget(ctx context.Context, path, reason string) error {
	if s.forgetFn != nil {
		return s.forgetFn(ctx, path, reason)
	}
	return nil
}

// rpc builds a JSON-RPC 2.0 request line. Marshal error is ignored because
// inputs are always valid literals (map[string]any with primitives).
func rpc(id int, method string, params any) string {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		req["params"] = params
	}
	data, _ := json.Marshal(req) //nolint: inputs are always marshalable
	return string(data)
}

// notification builds a JSON-RPC 2.0 notification (no id).
func notification(method string) string {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	data, _ := json.Marshal(req) //nolint: inputs are always marshalable
	return string(data)
}

// runServer sends lines to a test server and returns all response lines.
func runServer(t *testing.T, eng Engine, lines ...string) []json.RawMessage {
	t.Helper()
	input := strings.Join(lines, "\n") + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer

	srv := NewServer(r, &w, eng)
	srv.now = func() time.Time { return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) }

	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var responses []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(w.String()), "\n") {
		if line == "" {
			continue
		}
		responses = append(responses, json.RawMessage(line))
	}
	return responses
}

// parseResponse unmarshals a JSON-RPC response and returns result and error parts.
func parseResponse(t *testing.T, raw json.RawMessage) (id json.RawMessage, result json.RawMessage, rpcErr *jsonrpcError) {
	t.Helper()
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *jsonrpcError   `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", resp.JSONRPC)
	}
	return resp.ID, resp.Result, resp.Error
}

func TestProtocol_InitializeHandshake(t *testing.T) {
	responses := runServer(t, &stubEngine{},
		rpc(1, "initialize", nil),
	)
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}

	id, result, rpcErr := parseResponse(t, responses[0])
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	if string(id) != "1" {
		t.Errorf("id = %s, want 1", id)
	}

	var init initializeResult
	if err := json.Unmarshal(result, &init); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if init.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", init.ProtocolVersion)
	}
	if init.ServerInfo.Name != "brain" {
		t.Errorf("serverInfo.name = %q, want brain", init.ServerInfo.Name)
	}
	if init.Capabilities.Tools == nil {
		t.Error("capabilities.tools should be present")
	}
}

func TestProtocol_ToolsListAfterInit(t *testing.T) {
	responses := runServer(t, &stubEngine{},
		rpc(1, "initialize", nil),
		notification("notifications/initialized"),
		rpc(2, "tools/list", nil),
	)
	// initialize + tools/list = 2 responses (notification has none)
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2", len(responses))
	}

	_, result, rpcErr := parseResponse(t, responses[1])
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}

	var toolsList struct {
		Tools []toolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(result, &toolsList); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(toolsList.Tools) != 4 {
		t.Fatalf("got %d tools, want 4", len(toolsList.Tools))
	}

	names := map[string]bool{}
	for _, tool := range toolsList.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"brain_remember", "brain_recall", "brain_list", "brain_forget"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestProtocol_RejectBeforeInit(t *testing.T) {
	// Both tools/list and tools/call should be rejected before initialize.
	responses := runServer(t, &stubEngine{},
		rpc(1, "tools/list", nil),
		rpc(2, "tools/call", map[string]any{
			"name":      "brain_recall",
			"arguments": map[string]any{},
		}),
	)
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2", len(responses))
	}

	for i, resp := range responses {
		_, _, rpcErr := parseResponse(t, resp)
		if rpcErr == nil {
			t.Errorf("response[%d]: expected error, got success", i)
			continue
		}
		if rpcErr.Code != errCodeInvalidRequest {
			t.Errorf("response[%d]: error code = %d, want %d", i, rpcErr.Code, errCodeInvalidRequest)
		}
	}
}

func TestProtocol_ParseError(t *testing.T) {
	input := "this is not json\n"
	r := strings.NewReader(input)
	var w bytes.Buffer
	srv := NewServer(r, &w, &stubEngine{})

	_ = srv.Run(context.Background())

	responses := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal([]byte(responses[0]), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != errCodeParse {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errCodeParse)
	}
}

func TestProtocol_InvalidJSONRPCVersion(t *testing.T) {
	input := `{"jsonrpc":"1.0","method":"initialize","id":1}` + "\n"
	r := strings.NewReader(input)
	var w bytes.Buffer
	srv := NewServer(r, &w, &stubEngine{})

	_ = srv.Run(context.Background())

	var resp jsonrpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(w.String())), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != errCodeInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errCodeInvalidRequest)
	}
}

func TestProtocol_UnknownMethod(t *testing.T) {
	responses := runServer(t, &stubEngine{},
		rpc(1, "bogus/method", nil),
	)
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}

	_, _, rpcErr := parseResponse(t, responses[0])
	if rpcErr == nil {
		t.Fatal("expected error")
	}
	if rpcErr.Code != errCodeMethodNotFound {
		t.Errorf("error code = %d, want %d", rpcErr.Code, errCodeMethodNotFound)
	}
}

func TestProtocol_UnknownTool(t *testing.T) {
	responses := runServer(t, &stubEngine{},
		rpc(1, "initialize", nil),
		notification("notifications/initialized"),
		rpc(2, "tools/call", map[string]any{
			"name":      "brain_nonexistent",
			"arguments": map[string]any{},
		}),
	)
	// initialize + tools/call = 2
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2", len(responses))
	}

	_, _, rpcErr := parseResponse(t, responses[1])
	if rpcErr == nil {
		t.Fatal("expected error for unknown tool")
	}
	if rpcErr.Code != errCodeInvalidParams {
		t.Errorf("error code = %d, want %d", rpcErr.Code, errCodeInvalidParams)
	}
}

// failWriter fails after maxWrites successful writes.
type failWriter struct {
	buf       bytes.Buffer
	maxWrites int
	cur       int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.cur++
	if w.cur > w.maxWrites {
		return 0, errors.New("broken pipe")
	}
	return w.buf.Write(p)
}

func TestProtocol_WriteErrorStopsLoop(t *testing.T) {
	// Allow 1 write (the initialize response), then fail.
	fw := &failWriter{maxWrites: 1}
	// Send initialize, then notification, then tools/list.
	// The tools/list write should fail, and the server should exit.
	input := strings.Join([]string{
		rpc(1, "initialize", nil),
		notification("notifications/initialized"),
		rpc(2, "tools/list", nil),
		rpc(3, "tools/list", nil), // should never be processed
	}, "\n") + "\n"

	srv := NewServer(strings.NewReader(input), fw, &stubEngine{})
	err := srv.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from broken pipe")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("error = %q, want 'write failed'", err)
	}
}

func TestProtocol_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Even with input available, should exit due to cancelled context.
	input := rpc(1, "initialize", nil) + "\n"
	var w bytes.Buffer
	srv := NewServer(strings.NewReader(input), &w, &stubEngine{})

	err := srv.Run(ctx)
	if err != context.Canceled {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestProtocol_EmptyLinesIgnored(t *testing.T) {
	input := "\n\n" + rpc(1, "initialize", nil) + "\n\n"
	var w bytes.Buffer
	srv := NewServer(strings.NewReader(input), &w, &stubEngine{})

	_ = srv.Run(context.Background())

	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d responses, want 1", len(lines))
	}
}
