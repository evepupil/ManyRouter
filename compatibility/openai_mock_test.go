//go:build contract

package compatibility_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newOpenAIMock(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Header.Get("Authorization") != "Bearer "+contractUpstreamKey {
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"message": "invalid upstream key"}})
			return
		}
		switch request.URL.Path {
		case "/v1/chat/completions", "/chat/completions":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "chatcmpl-manyrouter-contract", "object": "chat.completion",
				"created": time.Now().Unix(), "model": contractModel,
				"choices": []map[string]any{{
					"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
		case "/v1/models", "/models":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"object": "list", "data": []map[string]any{{"id": contractModel, "object": "model"}},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"message": "unsupported mock path"}})
		}
	}))
	t.Cleanup(server.Close)
	return server
}
