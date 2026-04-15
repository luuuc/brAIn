// Package mcp implements an MCP (Model Context Protocol) server over stdio
// using JSON-RPC 2.0. It wraps the brAIn memory engine, exposing it as MCP
// tools for AI coding assistants.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/luuuc/brain/internal/version"
)

// JSON-RPC 2.0 error codes.
const (
	errCodeParse          = -32700
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeInternal       = -32603
)

// JSON-RPC 2.0 request/response types.

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCP protocol types.

type initializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	ServerInfo      mcpServerInfo    `json:"serverInfo"`
	Capabilities    serverCapability `json:"capabilities"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapability struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

// toolsCapability is intentionally empty — per MCP spec, its presence in
// capabilities signals that this server supports the tools/* methods.
type toolsCapability struct{}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Server is the MCP server. It reads JSON-RPC requests from reader and writes
// responses to writer, dispatching tool calls to the brAIn engine.
type Server struct {
	reader      io.Reader
	writer      io.Writer
	eng         Engine
	now         func() time.Time // clock function; defaults to time.Now
	initialized bool
	writeErr    error // sticky error from writeResponse; checked each loop iteration
}

// NewServer creates an MCP server backed by the given engine.
func NewServer(r io.Reader, w io.Writer, eng Engine) *Server {
	return &Server{
		reader: r,
		writer: w,
		eng:    eng,
		now:    time.Now,
	}
}

// Run reads JSON-RPC requests from stdin and dispatches them until the input
// stream closes, the context is cancelled, or a write to stdout fails.
//
// Note: scanner.Scan() blocks on stdin reads and cannot be interrupted by
// context cancellation alone. If the context is cancelled (e.g. SIGINT),
// the server exits after the current or next line is read. This is a known
// Go limitation with blocking I/O — the signal handler in cmd/mcp.go will
// cause the process to exit promptly in practice.
func (s *Server) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.reader)
	// 4 KB initial buffer, 1 MB max. Memory tool messages are small;
	// this cap prevents a malformed message from consuming unbounded RAM.
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	for scanner.Scan() {
		// Check for context cancellation between messages.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// If a previous write failed, the client is gone.
		if s.writeErr != nil {
			return fmt.Errorf("write failed: %w", s.writeErr)
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, errCodeParse, "parse error", err.Error())
			continue
		}
		if req.JSONRPC != "2.0" {
			s.sendError(req.ID, errCodeInvalidRequest, "invalid request", "jsonrpc must be \"2.0\"")
			continue
		}

		s.dispatch(ctx, &req)
	}
	return scanner.Err()
}

func (s *Server) dispatch(ctx context.Context, req *jsonrpcRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "notifications/initialized":
		s.initialized = true
		// Notification — no response.
	case "tools/list":
		if !s.requireInitialized(req) {
			return
		}
		s.handleToolsList(req)
	case "tools/call":
		if !s.requireInitialized(req) {
			return
		}
		s.handleToolsCall(ctx, req)
	default:
		s.sendError(req.ID, errCodeMethodNotFound, "method not found", req.Method)
	}
}

func (s *Server) handleInitialize(req *jsonrpcRequest) {
	v := version.Version
	s.sendResult(req.ID, initializeResult{
		ProtocolVersion: "2024-11-05",
		ServerInfo: mcpServerInfo{
			Name:    "brain",
			Version: v,
		},
		Capabilities: serverCapability{
			Tools: &toolsCapability{},
		},
	})
}

// requireInitialized checks that the server has completed the MCP handshake.
// Returns false (and sends an error) if not yet initialized.
func (s *Server) requireInitialized(req *jsonrpcRequest) bool {
	if s.initialized {
		return true
	}
	s.sendError(req.ID, errCodeInvalidRequest, "server not initialized", "call initialize first")
	return false
}

func (s *Server) sendResult(id json.RawMessage, result any) {
	s.writeResponse(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (s *Server) sendError(id json.RawMessage, code int, message string, data any) {
	s.writeResponse(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonrpcError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

func (s *Server) writeResponse(resp jsonrpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal response", "err", err)
		s.writeErr = err
		return
	}
	data = append(data, '\n')
	if _, err := s.writer.Write(data); err != nil {
		slog.Error("failed to write response", "err", err)
		s.writeErr = err
	}
}
