package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// decodeRPCBody extracts the JSON-RPC payload from either a raw JSON response
// or an SSE stream ("event: message\ndata: {...}").
func decodeRPCBody(t *testing.T, body string, v any) {
	t.Helper()
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		if err := json.Unmarshal([]byte(body), v); err != nil {
			t.Fatalf("decode rpc body: %v (%s)", err, body)
		}
		return
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if err := json.Unmarshal([]byte(payload), v); err != nil {
				t.Fatalf("decode sse data: %v (%s)", err, payload)
			}
			return
		}
	}
	t.Fatalf("no json-rpc payload found in response body: %s", body)
}

// TestMCPMountEndToEnd exercises the /mcp endpoint mounted on the same gin engine:
// initialize -> notifications/initialized -> tools/list -> tools/call.
func TestMCPMountEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatalf("unexpected upstream HTTP call: %s", req.URL)
		return nil, nil
	}))
	RegisterMCP(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		body := `{"flightList":[],"type":"cn","currentDate":1,"currentTime":12}`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	server := httptest.NewServer(router)
	defer server.Close()

	post := func(t *testing.T, payload string, sessionID string) (*nethttp.Response, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, server.URL+"/mcp", bytes.NewReader([]byte(payload)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, string(raw)
	}

	// 1. initialize
	resp, body := post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
		"protocolVersion":"2025-06-18",
		"capabilities":{},
		"clientInfo":{"name":"kapi-test","version":"0.0.1"}
	}}`, "")
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("initialize: expected 200, got %d: %s", resp.StatusCode, body)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("initialize: missing Mcp-Session-Id header, body=%s", body)
	}

	var initResult struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeRPCBody(t, body, &initResult)
	if initResult.Result.ServerInfo.Name != "kapi" {
		t.Fatalf("expected server name kapi, got %q", initResult.Result.ServerInfo.Name)
	}

	// 2. initialized notification
	resp, _ = post(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sessionID)
	if resp.StatusCode != nethttp.StatusAccepted {
		t.Fatalf("initialized notification: expected 202, got %d", resp.StatusCode)
	}

	// 3. tools/list
	_, body = post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, sessionID)
	var listResult struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	decodeRPCBody(t, body, &listResult)

	wantTools := map[string]bool{
		"list_airports": false, "search_flights": false, "get_flight_status": false,
		"get_today_flights": false, "get_delay_trend": false, "get_weather": false,
	}
	for _, tool := range listResult.Result.Tools {
		if _, ok := wantTools[tool.Name]; ok {
			wantTools[tool.Name] = true
			if tool.Description == "" {
				t.Fatalf("tool %s missing description", tool.Name)
			}
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Fatalf("tool %s not exposed via tools/list, got: %s", name, body)
		}
	}

	// 4. tools/call list_airports
	_, body = post(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{
		"name":"list_airports","arguments":{}
	}}`, sessionID)
	var callResult struct {
		Result struct {
			StructuredContent struct {
				Total    int `json:"total"`
				Airports []struct {
					Code string `json:"code"`
				} `json:"airports"`
			} `json:"structuredContent"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeRPCBody(t, body, &callResult)
	if callResult.Result.IsError {
		t.Fatalf("tools/call reported error: %s", body)
	}
	if callResult.Result.StructuredContent.Total < 2 {
		t.Fatalf("expected at least 2 airports, got: %s", body)
	}

	// 5. unsupported airport surfaces as a tool error (IsError), not a transport failure
	_, body = post(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{
		"name":"search_flights","arguments":{"airport":"pek","direction":"departure"}
	}}`, sessionID)
	var errResult struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeRPCBody(t, body, &errResult)
	if !errResult.Result.IsError {
		t.Fatalf("expected business error as IsError tool result, got: %s", body)
	}
	if len(errResult.Result.Content) == 0 || !strings.Contains(errResult.Result.Content[0].Text, "not supported") {
		t.Fatalf("expected human-readable error content, got: %s", body)
	}
}
