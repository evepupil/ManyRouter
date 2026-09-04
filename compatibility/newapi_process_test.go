//go:build contract

package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startNewAPI(t *testing.T, parent context.Context, binary string) string {
	t.Helper()
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absoluteBinary); err != nil {
		t.Fatalf("New API binary is unavailable: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	processContext, stopProcess := context.WithCancel(parent)
	workingDirectory := t.TempDir()
	logFile, err := os.Create(filepath.Join(workingDirectory, "new-api.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(processContext, absoluteBinary)
	command.Dir = workingDirectory
	command.Env = contractEnvironment(port)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		stopProcess()
		_ = logFile.Close()
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		stopProcess()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			if command.Process != nil {
				_ = command.Process.Kill()
			}
		}
		_ = logFile.Close()
	})
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForNewAPI(t, parent, baseURL, exited)
	return baseURL
}

func contractEnvironment(port int) []string {
	blocked := map[string]struct{}{
		"SQL_DSN": {}, "LOG_SQL_DSN": {}, "REDIS_CONN_STRING": {}, "PORT": {}, "SESSION_SECRET": {},
	}
	environment := make([]string, 0, len(os.Environ())+5)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, exists := blocked[strings.ToUpper(key)]; !exists {
			environment = append(environment, item)
		}
	}
	return append(environment,
		fmt.Sprintf("PORT=%d", port),
		"SESSION_SECRET=manyrouter-contract-session-secret-2026",
		"GIN_MODE=release",
		"NODE_TYPE=master",
		"TZ=UTC",
	)
}

func waitForNewAPI(t *testing.T, ctx context.Context, baseURL string, exited <-chan error) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			t.Fatalf("New API exited before becoming ready: %v", err)
		case <-ctx.Done():
			t.Fatalf("New API did not become ready: %v", context.Cause(ctx))
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/status", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				continue
			}
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
	}
}

func postJSON(t *testing.T, ctx context.Context, url, token string, requestBody, responseBody any) {
	t.Helper()
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	doJSON(t, request, token, responseBody)
}

func putJSON(t *testing.T, ctx context.Context, url, token string, requestBody, responseBody any) {
	t.Helper()
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	doJSON(t, request, token, responseBody)
}

func getJSON(t *testing.T, ctx context.Context, url, token string, responseBody any) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	doJSON(t, request, token, responseBody)
}

func doJSON(t *testing.T, request *http.Request, token string, responseBody any) {
	t.Helper()
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		summary := string(payload)
		if len(summary) > 1024 {
			summary = summary[:1024]
		}
		t.Fatalf("HTTP request failed with status %d: %s", response.StatusCode, summary)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(responseBody); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
