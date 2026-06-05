package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/api"
)

func TestValidatePrometheusAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr string
	}{
		{
			name:    "base url",
			address: "http://localhost:8080/prometheus",
		},
		{
			name:    "base url without path",
			address: "https://prometheus.example.com",
		},
		{
			name:    "query endpoint",
			address: "http://localhost:8080/prometheus/api/v1/query",
			wantErr: "base URL",
		},
		{
			name:    "api root endpoint",
			address: "http://localhost:8080/api/v1",
			wantErr: "base URL",
		},
		{
			name:    "missing host",
			address: "http:///prometheus",
			wantErr: "host",
		},
		{
			name:    "unsupported scheme",
			address: "ftp://prometheus.example.com",
			wantErr: "scheme",
		},
		{
			name:    "empty",
			address: "",
			wantErr: "required",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrometheusAddress(tt.address)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidatePrometheusAddress() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want to contain %q", err, tt.wantErr)
			}
		})
	}
}

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
