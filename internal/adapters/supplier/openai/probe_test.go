package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/supplier/openai"
)

func TestProbeClientReadsNonStreamChatCompletion(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer supplier-key" || request.Header.Get("Accept") != "application/json" {
			t.Error("missing supplier probe headers")
		}
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Temperature float64 `json:"temperature"`
			TopP        float64 `json:"top_p"`
			MaxTokens   int     `json:"max_tokens"`
			Stream      bool    `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "model-a" || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != "reply briefly" || payload.Temperature != 0.7 || payload.TopP != 0.9 || payload.MaxTokens != 16 || payload.Stream {
			t.Errorf("unexpected probe payload: %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"model":"model-a-2026","choices":[{"message":{"role":"assistant","content":"blue"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`))
	}))
	defer server.Close()
	client, err := openai.NewProbeClient(server.Client())
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Probe(context.Background(), server.URL+"/v1", []byte("supplier-key"), openai.ProbeRequest{
		Model: "model-a", Prompt: "reply briefly", Temperature: 0.7, TopP: 0.9, MaxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.HTTPStatus != http.StatusOK || result.Text != "blue" || result.ResponseModel != "model-a-2026" || result.FinishReason != "stop" {
		t.Fatalf("unexpected response identity: %#v", result)
	}
	if result.InputTokens != 11 || result.OutputTokens != 2 || result.FirstTokenMillis == nil || result.TotalMillis < *result.FirstTokenMillis || result.StreamCompleted {
		t.Fatalf("unexpected response measurements: %#v", result)
	}
}

func TestProbeClientReadsStreamAndMeasuresFirstVisibleText(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Error("stream probe did not request SSE")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatal("test server cannot flush")
		}
		_, _ = writer.Write([]byte("data: {\"model\":\"model-stream\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n"))
		flusher.Flush()
		time.Sleep(15 * time.Millisecond)
		_, _ = writer.Write([]byte("data: {\"model\":\"model-stream\",\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n"))
		flusher.Flush()
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = writer.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	client, err := openai.NewProbeClient(server.Client())
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Probe(context.Background(), server.URL, []byte("supplier-key"), openai.ProbeRequest{
		Model: "model-a", Prompt: "hello", Temperature: 1, TopP: 1, MaxTokens: 16, Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" || result.ResponseModel != "model-stream" || result.FinishReason != "stop" || !result.StreamCompleted {
		t.Fatalf("unexpected stream result: %#v", result)
	}
	if result.InputTokens != 9 || result.OutputTokens != 2 || result.FirstTokenMillis == nil || *result.FirstTokenMillis < 5 || result.TotalMillis < *result.FirstTokenMillis {
		t.Fatalf("unexpected stream measurements: %#v", result)
	}
}

func TestProbeClientReportsStreamWithoutDoneAsIncomplete(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"model\":\"model-a\",\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}]}\n\n"))
	}))
	defer server.Close()
	client, err := openai.NewProbeClient(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Probe(context.Background(), server.URL, []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "partial" || result.FinishReason != "stop" || result.StreamCompleted {
		t.Fatalf("truncated stream was not reported as incomplete: %#v", result)
	}
}

func TestProbeClientRejectsNonSSEStreamResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("supplier-key private response body"))
	}))
	defer server.Close()
	client, err := openai.NewProbeClient(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Probe(context.Background(), server.URL, []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16, Stream: true})
	if err == nil || strings.Contains(err.Error(), "supplier-key") || strings.Contains(err.Error(), "private response") {
		t.Fatalf("unexpected stream protocol error: %v", err)
	}
}

func TestProbeClientReturnsHTTPFailuresAsResults(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte("supplier-key private response body"))
			}))
			defer server.Close()
			client, err := openai.NewProbeClient(server.Client())
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Probe(context.Background(), server.URL, []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16})
			if err != nil || result.HTTPStatus != status {
				t.Fatalf("HTTP %d did not remain a business result: result=%#v error=%v", status, result, err)
			}
		})
	}
}

func TestProbeClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalled.Store(true)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := openai.NewProbeClient(redirect.Client())
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Probe(context.Background(), redirect.URL, []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16})
	if err != nil || result.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("unexpected redirect result: result=%#v error=%v", result, err)
	}
	if targetCalled.Load() {
		t.Fatal("redirect target received a supplier credential")
	}
}

func TestProbeClientRejectsOversizedAndMalformedResponsesWithoutLeakingContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: strings.Repeat("private-body-", 6000)},
		{name: "malformed", body: `{"supplier-key":"private-body"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := openai.NewProbeClient(server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Probe(context.Background(), server.URL, []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16})
			if err == nil {
				t.Fatal("invalid supplier response was accepted")
			}
			if strings.Contains(err.Error(), "supplier-key") || strings.Contains(err.Error(), "private-body") {
				t.Fatalf("probe error leaked credentials or response content: %v", err)
			}
		})
	}
}

func TestNewProbeClientRequiresHTTPClient(t *testing.T) {
	t.Parallel()
	if _, err := openai.NewProbeClient(nil); err == nil {
		t.Fatal("nil HTTP client was accepted")
	}
}

func TestProbeClientReturnsNetworkErrors(t *testing.T) {
	t.Parallel()
	client, err := openai.NewProbeClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Probe(context.Background(), "https://supplier.example", []byte("supplier-key"), openai.ProbeRequest{Model: "model-a", Prompt: "hello", MaxTokens: 16})
	if err == nil || strings.Contains(err.Error(), "supplier-key") {
		t.Fatalf("unexpected network error: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
