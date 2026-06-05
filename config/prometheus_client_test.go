package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/api"
)

func TestQueryPrometheusWithContextUsesCallerCancellation(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := api.NewClient(api.Config{Address: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err = QueryPrometheusWithContext(ctx, client, "up"); err == nil {
		t.Fatal("expected canceled query error")
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("request count = %d, want=0", got)
	}
}

func TestQueryPrometheusWithContextParsesVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1234.5,"1"]}]}}`))
	}))
	defer server.Close()

	client, err := api.NewClient(api.Config{Address: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	vector, err := QueryPrometheusWithContext(context.Background(), client, "up")
	if err != nil {
		t.Fatalf("QueryPrometheusWithContext failed: %v", err)
	}
	if len(vector) != 1 {
		t.Fatalf("vector length = %d, want=1", len(vector))
	}
	if got := vector[0].Value.String(); got != "1" {
		t.Fatalf("sample value = %s, want=1", got)
	}
}
