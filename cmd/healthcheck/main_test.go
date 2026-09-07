package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := check(context.Background(), server.Client(), server.URL); err != nil {
		t.Fatalf("check() error = %v", err)
	}
}

func TestCheckRejectsUnreadyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := check(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("check() succeeded for an unready response")
	}
}
