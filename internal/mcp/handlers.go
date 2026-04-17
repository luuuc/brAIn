package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

func (s *Server) handleToolsCall(ctx context.Context, req *jsonrpcRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, errCodeInvalidParams, "invalid params", err.Error())
		return
	}

	var result toolCallResult
	switch params.Name {
	case "brain_remember":
		result = s.handleRemember(ctx, params.Arguments)
	case "brain_recall":
		result = s.handleRecall(ctx, params.Arguments)
	case "brain_list":
		result = s.handleList(ctx, params.Arguments)
	case "brain_forget":
		result = s.handleForget(ctx, params.Arguments)
	case "brain_trust":
		result = s.handleTrust(ctx, params.Arguments)
	case "brain_trust_record":
		result = s.handleTrustRecord(ctx, params.Arguments)
	case "brain_trust_override":
		result = s.handleTrustOverride(ctx, params.Arguments)
	case "brain_track":
		result = s.handleTrack(ctx, params.Arguments)
	default:
		s.sendError(req.ID, errCodeInvalidParams, "unknown tool", params.Name)
		return
	}

	s.sendResult(req.ID, result)
}

func (s *Server) handleRemember(ctx context.Context, args map[string]any) toolCallResult {
	content, _ := args["content"].(string)
	if content == "" {
		return errorResult("content is required")
	}
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errorResult("domain is required")
	}

	m := memory.Memory{
		Domain:  domain,
		Body:    content,
		Created: s.now(),
		Source:  memory.SourceTool,
	}

	if layer, ok := args["layer"].(string); ok && layer != "" {
		l := memory.Layer(layer)
		if !l.Valid() {
			return errorResult(fmt.Sprintf("invalid layer %q", layer))
		}
		m.Layer = l
	}

	if tags, ok := args["tags"].(string); ok && tags != "" {
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				m.Tags = append(m.Tags, t)
			}
		}
	}

	result, err := s.eng.Remember(ctx, m)
	if err != nil {
		return engineError(err)
	}

	return jsonResult(map[string]any{
		"path":   result.Path,
		"layer":  string(result.Layer),
		"domain": domain,
	})
}

func (s *Server) handleRecall(ctx context.Context, args map[string]any) toolCallResult {
	opts := engine.RecallOptions{
		Limit: 5,
	}

	if domain, ok := args["domain"].(string); ok {
		opts.Domain = domain
	}
	if query, ok := args["query"].(string); ok {
		opts.Query = query
	}
	if layer, ok := args["layer"].(string); ok && layer != "" {
		l := memory.Layer(layer)
		if !l.Valid() {
			return errorResult(fmt.Sprintf("invalid layer %q", layer))
		}
		opts.Layer = &l
	}
	if limit, ok := args["limit"].(float64); ok {
		opts.Limit = int(limit)
	}

	memories, err := s.eng.Recall(ctx, opts)
	if err != nil {
		return engineError(err)
	}

	items := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		item := map[string]any{
			"path":   m.Path,
			"layer":  string(m.Layer),
			"domain": m.Domain,
			"title":  firstLine(m.Body),
			"body":   m.Body,
		}
		if len(m.Tags) > 0 {
			item["tags"] = m.Tags
		}
		items = append(items, item)
	}

	return jsonResult(map[string]any{
		"memories": items,
	})
}

func (s *Server) handleList(ctx context.Context, args map[string]any) toolCallResult {
	opts := engine.RecallOptions{
		Limit: 0, // no limit for list
	}

	if domain, ok := args["domain"].(string); ok {
		opts.Domain = domain
	}
	if layer, ok := args["layer"].(string); ok && layer != "" {
		l := memory.Layer(layer)
		if !l.Valid() {
			return errorResult(fmt.Sprintf("invalid layer %q", layer))
		}
		opts.Layer = &l
	}
	if retired, ok := args["include_retired"].(bool); ok {
		opts.IncludeRetired = retired
	}

	memories, err := s.eng.Recall(ctx, opts)
	if err != nil {
		return engineError(err)
	}

	items := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		item := map[string]any{
			"path":    m.Path,
			"layer":   string(m.Layer),
			"domain":  m.Domain,
			"created": m.Created.Format("2006-01-02"),
		}
		if m.Retired {
			item["retired"] = true
		}
		items = append(items, item)
	}

	return jsonResult(map[string]any{
		"memories": items,
	})
}

func (s *Server) handleForget(ctx context.Context, args map[string]any) toolCallResult {
	path, _ := args["path"].(string)
	if path == "" {
		return errorResult("path is required")
	}
	reason, _ := args["reason"].(string)

	err := s.eng.Forget(ctx, path, reason)
	if err != nil {
		return engineError(err)
	}

	return jsonResult(map[string]any{
		"path":   path,
		"status": "retired",
	})
}

// engineError maps an engine/store error to an appropriate tool error result.
// It checks for known sentinel errors to produce specific messages.
func engineError(err error) toolCallResult {
	if errors.Is(err, store.ErrNotFound) {
		return errorResult(fmt.Sprintf("not found: %s", err))
	}
	if errors.Is(err, store.ErrConflict) {
		return errorResult(fmt.Sprintf("conflict: %s", err))
	}
	return errorResult(err.Error())
}

// errorResult returns a toolCallResult indicating an error.
func errorResult(msg string) toolCallResult {
	return toolCallResult{
		Content: []toolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// jsonResult marshals v to JSON and wraps it in a toolCallResult.
func jsonResult(v any) toolCallResult {
	data, err := json.Marshal(v)
	if err != nil {
		return errorResult(err.Error())
	}
	return toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(data)}},
	}
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

