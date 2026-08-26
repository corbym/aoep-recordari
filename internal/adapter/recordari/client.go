// Package recordari provides a Recordari MCP adapter for the AOEP harness.
package recordari

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

// Client is a thin JSON-RPC 2.0 client for the Recordari MCP endpoint (POST /mcp).
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	idSeq   atomic.Int64
}

// NewClient creates a new Recordari MCP client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolResult is the standard MCP tool call result envelope.
type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallTool calls a named Recordari MCP tool with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var result ToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tool result: %w", err)
	}
	return &result, nil
}

// ParseResult extracts the JSON payload from the first content item in a tool result.
func ParseResult(tr *ToolResult) (map[string]any, error) {
	if tr.IsError || len(tr.Content) == 0 {
		msg := "tool error (no content)"
		if len(tr.Content) > 0 {
			msg = tr.Content[0].Text
		}
		return nil, fmt.Errorf("tool returned error: %s", msg)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
		// Result might be plain text — wrap it.
		return map[string]any{"text": tr.Content[0].Text}, nil
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.idSeq.Add(1)
	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	var rpc rpcResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return nil, fmt.Errorf("unmarshal rpc response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result, nil
}
