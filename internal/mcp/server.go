// Package mcp serves AgentLens analyses to an AI coding assistant over the Model Context
// Protocol (JSON-RPC 2.0 on stdio), so the assistant can ask which tools stopped working and
// why, instead of the user pasting a prompt. It implements the few methods a tools-only
// server needs with the standard library alone.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tsbkw/agentlens/internal/providers"
)

// ProtocolVersion is the MCP revision this server speaks; older clients are answered with
// their own version, which the methods used here are compatible with.
const ProtocolVersion = "2025-06-18"

// Server answers MCP requests with analyses of the sessions of the given providers.
type Server struct {
	Providers []*providers.LoadedProvider
	// Version is reported to the client in serverInfo.
	Version string
	// Log receives diagnostics; stdout is reserved for the protocol. Defaults to stderr.
	Log io.Writer
	// Now is the clock used for report windows; tests replace it.
	Now func() time.Time
}

// NewServer creates a Server over provs.
func NewServer(version string, provs ...*providers.LoadedProvider) *Server {
	return &Server{Providers: provs, Version: version, Log: os.Stderr, Now: time.Now}
}

// JSON-RPC 2.0 messages. IDs are kept raw because clients send numbers or strings.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// MaxLineBytes bounds one incoming message.
const MaxLineBytes = 16 * 1024 * 1024

// Serve reads newline-delimited JSON-RPC messages from in and writes responses to out until
// in is exhausted or ctx is cancelled. Notifications get no response.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	var mu sync.Mutex
	write := func(v interface{}) {
		raw, err := json.Marshal(v)
		if err != nil {
			s.logf("cannot encode response: %v", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		out.Write(raw)
		out.Write([]byte("\n"))
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), MaxLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.Method == "" {
			// A JSON array (a batch) or garbage: one error with a null id
			write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParseError, Message: "expected a JSON-RPC 2.0 request object"}})
			continue
		}
		isNotification := len(req.ID) == 0 || string(req.ID) == "null"
		result, rpcErr := s.handle(ctx, req)
		if isNotification {
			continue
		}
		if rpcErr != nil {
			write(response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
			continue
		}
		write(response{JSONRPC: "2.0", ID: req.ID, Result: result})
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading MCP input: %w", err)
	}
	return nil
}

func (s *Server) logf(format string, args ...interface{}) {
	if s.Log != nil {
		fmt.Fprintf(s.Log, "agentlens mcp: "+format+"\n", args...)
	}
}

// handle dispatches one request. A nil result with a nil error means a notification was
// consumed.
func (s *Server) handle(ctx context.Context, req request) (interface{}, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &params)
		version := ProtocolVersion
		if params.ProtocolVersion != "" && params.ProtocolVersion < ProtocolVersion {
			version = params.ProtocolVersion
		}
		return map[string]interface{}{
			"protocolVersion": version,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
			"serverInfo":      map[string]interface{}{"name": "agentlens", "version": s.Version},
			"instructions": "AgentLens analyzes this machine's AI agent session logs (Claude Code and others): which Skills and MCP servers " +
				"failed, were silently replaced by other tools, stopped being called, and why. Call `report` for a period overview with " +
				"the dormant tools, `why` for one Skill or MCP server, `list_sessions` and `session` to look at sessions, `inspect_call` " +
				"for one call. When `why` returns an investigation, follow its checks on this machine and never print secrets.",
		}, nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return map[string]interface{}{"tools": toolDefinitions()}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call needs a tool name"}
		}
		return s.callTool(ctx, params.Name, params.Arguments), nil
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		return nil, nil
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q is not supported", req.Method)}
}

// callTool runs a tool and wraps its result the way MCP expects: text content holding the
// JSON, the same value as structuredContent, and isError on failure.
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) map[string]interface{} {
	result, err := s.runTool(ctx, name, args)
	if err != nil {
		s.logf("%s: %v", name, err)
		return map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": err.Error()}},
			"isError": true,
		}
	}
	raw, merr := json.MarshalIndent(result, "", "  ")
	if merr != nil {
		return map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": "cannot encode result: " + merr.Error()}},
			"isError": true,
		}
	}
	return map[string]interface{}{
		"content":           []map[string]interface{}{{"type": "text", "text": string(raw)}},
		"structuredContent": result,
	}
}
