//go:build contract

package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
)

func TestNewAPIM3RetriesAnotherAutoChannelBeforeFirstToken(t *testing.T) {
	binary := os.Getenv("MANYROUTER_NEW_API_BINARY")
	if binary == "" {
		t.Skip("MANYROUTER_NEW_API_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var failedCalls atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		failedCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"message": "controlled failure"}})
	}))
	defer failing.Close()
	var healthyCalls atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		healthyCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "chatcmpl-m3-failover", "object": "chat.completion",
			"created": time.Now().Unix(), "model": contractModel,
			"choices": []map[string]any{{
				"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer healthy.Close()
	baseURL := startNewAPI(t, ctx, binary)
	admin := initializeAndLogin(t, ctx, baseURL)
	putNewAPIOption(t, ctx, baseURL, admin, "RetryTimes", "1")
	putNewAPIOption(t, ctx, baseURL, admin, "AutomaticRetryStatusCodes", "500-503")
	key := provisionM3AutoGroup(t, ctx, baseURL, admin, "m3fa", []m3ContractUpstream{
		{baseURL: failing.URL, priority: 10},
		{baseURL: healthy.URL, priority: 0},
	})
	status, code := m1Completion(ctx, baseURL, key)
	if status != http.StatusOK || code != "" {
		t.Fatalf("failover request returned HTTP %d code %q", status, code)
	}
	if failedCalls.Load() != 1 || healthyCalls.Load() != 1 {
		t.Fatalf("request did not traverse failing then healthy channel: failed=%d healthy=%d", failedCalls.Load(), healthyCalls.Load())
	}
	client, err := newapi.NewClient(baseURL, []byte(admin), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := client.ReadRetryPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policy.RetryTimes != 1 || !policy.AllowsStatus(http.StatusInternalServerError) {
		t.Fatalf("retry policy was not readable after failover: %#v", policy)
	}
}

func TestNewAPIM3DoesNotReplayAfterStreamStarts(t *testing.T) {
	binary := os.Getenv("MANYROUTER_NEW_API_BINARY")
	if binary == "" {
		t.Skip("MANYROUTER_NEW_API_BINARY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var partialCalls atomic.Int32
	partial := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		partialCalls.Add(1)
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		payload := fmt.Sprintf(
			"data: {\"id\":\"chatcmpl-m3-partial\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial-once\"},\"finish_reason\":null}]}\n\n",
			time.Now().Unix(), contractModel,
		)
		_, _ = fmt.Fprintf(buffered, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%x\r\n%s\r\n", len(payload), payload)
		_ = buffered.Flush()
		_ = connection.Close()
	}))
	defer partial.Close()
	var healthyCalls atomic.Int32
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		healthyCalls.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: {\"id\":\"chatcmpl-m3-replayed\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"must-not-appear\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", time.Now().Unix(), contractModel)
	}))
	defer healthy.Close()
	baseURL := startNewAPI(t, ctx, binary)
	admin := initializeAndLogin(t, ctx, baseURL)
	putNewAPIOption(t, ctx, baseURL, admin, "RetryTimes", "1")
	putNewAPIOption(t, ctx, baseURL, admin, "AutomaticRetryStatusCodes", "500-503")
	key := provisionM3AutoGroup(t, ctx, baseURL, admin, "m3fs", []m3ContractUpstream{
		{baseURL: partial.URL, priority: 10},
		{baseURL: healthy.URL, priority: 0},
	})
	status, responseBody, err := m3StreamCompletion(ctx, baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("partial stream returned HTTP %d: %s", status, responseBody)
	}
	if strings.Count(responseBody, "partial-once") != 1 {
		t.Fatalf("partial stream was missing or replayed: %s", responseBody)
	}
	if strings.Contains(responseBody, "must-not-appear") {
		t.Fatalf("backup channel content was appended after streaming started: %s", responseBody)
	}
	if partialCalls.Load() != 1 || healthyCalls.Load() != 0 {
		t.Fatalf("stream was retried after output started: partial=%d healthy=%d", partialCalls.Load(), healthyCalls.Load())
	}
}

type m3ContractUpstream struct {
	baseURL  string
	priority int64
}

func provisionM3AutoGroup(t *testing.T, ctx context.Context, baseURL, admin, group string, upstreams []m3ContractUpstream) string {
	t.Helper()
	client, err := newapi.NewClient(baseURL, []byte(admin), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := client.ReadActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	auto := routing.DesiredGroup{Key: group, DisplayName: "M3 Failover", SaleRatio: "1", Visible: true}
	ratios, _, err := reconciliation.MergeGroupRatios(baseline.GroupRatios, auto.Key, auto.SaleRatio)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetGroupRatios(ctx, ratios); err != nil {
		t.Fatal(err)
	}
	visible, _ := reconciliation.MergeUserUsableGroups(baseline.UserUsableGroups, auto)
	if err := client.SetUserUsableGroups(ctx, visible); err != nil {
		t.Fatal(err)
	}
	for _, upstream := range upstreams {
		channel := m3ContractChannel(upstream.baseURL, auto.Key, upstream.priority)
		if err := client.CreateChannel(ctx, channel, []byte(contractUpstreamKey)); err != nil {
			t.Fatal(err)
		}
		actual, err := client.ReadActualState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		created, err := reconciliation.LocateManagedChannel(channel, nil, actual.Channels)
		if err != nil || created == nil {
			t.Fatalf("created M3 channel is missing: %v", err)
		}
		if err := client.UpdateChannel(ctx, created.ID, channel, []byte(contractUpstreamKey)); err != nil {
			t.Fatal(err)
		}
		if err := client.SetChannelEnabled(ctx, created.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	return createUserAPIKey(t, ctx, baseURL, admin, auto.Key)
}

func m3StreamCompletion(ctx context.Context, baseURL, key string) (int, string, error) {
	body, err := json.Marshal(map[string]any{
		"model": contractModel, "stream": true,
		"messages":   []map[string]string{{"role": "user", "content": "M3 stream contract check"}},
		"max_tokens": 2,
	})
	if err != nil {
		return 0, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	return response.StatusCode, string(payload), err
}

func m3ContractChannel(baseURL, autoGroup string, priority int64) routing.DesiredChannel {
	relationID := uuid.New()
	return routing.DesiredChannel{
		ID: uuid.New(), ManagedTag: routing.ManagedTag(relationID), Name: "M3 Failover [ManyRouter]",
		Protocol: "openai_compatible", BaseURL: baseURL, CredentialID: uuid.New(), CredentialVersion: 1,
		Models:   []routing.ModelRoute{{Model: contractModel, UpstreamModel: contractModel}},
		GroupKey: routing.GroupKey(relationID), ExtraGroupKeys: []string{autoGroup},
		Priority: priority, Weight: 100, DesiredStatus: routing.DesiredEnabled,
	}
}

func putNewAPIOption(t *testing.T, ctx context.Context, baseURL, admin, key, value string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"key": key, "value": value})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+"/api/option/", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+admin)
	request.Header.Set("New-Api-User", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("update New API option %s returned HTTP %d", key, response.StatusCode)
	}
}
