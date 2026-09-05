package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/evepupil/ManyRouter/internal/adapters/supplier/openai"
)

func TestCredentialCheckerAcceptsAUsableModelList(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer supplier-key" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()
	checker, err := openai.NewCredentialChecker(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(context.Background(), server.URL, []byte("supplier-key")); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialCheckerDoesNotFollowRedirects(t *testing.T) {
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
	checker, err := openai.NewCredentialChecker(redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(context.Background(), redirect.URL, []byte("supplier-key")); err == nil {
		t.Fatal("redirected credential check was accepted")
	}
	if targetCalled.Load() {
		t.Fatal("redirect target received a supplier credential")
	}
}
