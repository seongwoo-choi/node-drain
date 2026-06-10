package config

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/api"
)

type configRoundTripFunc func(*http.Request) (*http.Response, error)

func (f configRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHeaderRoundTripperRejectsNilRequest(t *testing.T) {
	rt := &headerRoundTripper{}

	_, err := rt.RoundTrip(nil)
	if err == nil {
		t.Fatal("expected nil request error")
	}
	if !strings.Contains(err.Error(), "prometheus request is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHeaderRoundTripperUsesDefaultTransportWhenReceiverOrTransportNil(t *testing.T) {
	tests := []struct {
		name string
		rt   *headerRoundTripper
	}{
		{
			name: "nil receiver",
			rt:   nil,
		},
		{
			name: "nil transport",
			rt:   &headerRoundTripper{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("NewRequest failed: %v", err)
			}

			resp, err := tt.rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want=%d", resp.StatusCode, http.StatusNoContent)
			}
		})
	}
}

func TestHeaderRoundTripperSetsHeadersDeterministically(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://prometheus.example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Add("X-Scope-OrgID", "old")

	rt := &headerRoundTripper{
		headers: map[string]string{"X-Scope-OrgID": "new"},
		rt: configRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			values := req.Header.Values("X-Scope-OrgID")
			if len(values) != 1 || values[0] != "new" {
				t.Fatalf("X-Scope-OrgID values = %v, want [new]", values)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		}),
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	defer resp.Body.Close()
}

func TestPrometheusTenantIDFromEnv(t *testing.T) {
	tests := []struct {
		name       string
		tenantEnv  string
		legacyEnv  string
		wantTenant string
	}{
		{
			name:       "empty when unset",
			wantTenant: "",
		},
		{
			name:       "uses tenant env",
			tenantEnv:  " tenant-a ",
			legacyEnv:  "legacy-a",
			wantTenant: "tenant-a",
		},
		{
			name:       "falls back to legacy scope org env",
			legacyEnv:  " legacy-a ",
			wantTenant: "legacy-a",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROMETHEUS_TENANT_ID", tt.tenantEnv)
			t.Setenv("PROMETHEUS_SCOPE_ORG_ID", tt.legacyEnv)

			if got := prometheusTenantIDFromEnv(); got != tt.wantTenant {
				t.Fatalf("prometheusTenantIDFromEnv() = %q, want %q", got, tt.wantTenant)
			}
		})
	}
}

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
			name:    "prefixed api root endpoint",
			address: "http://localhost:8080/prometheus/api/v1",
			wantErr: "base URL",
		},
		{
			name:    "prefixed api root endpoint with trailing slash",
			address: "http://localhost:8080/prometheus/api/v1/",
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

func TestQueryPrometheusWithContextRejectsNilClient(t *testing.T) {
	if _, err := QueryPrometheusWithContext(context.Background(), nil, "up"); err == nil {
		t.Fatal("expected nil prometheus client error")
	} else if !strings.Contains(err.Error(), "prometheus client is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryPrometheusRejectsNilClient(t *testing.T) {
	if _, err := QueryPrometheus(nil, "up"); err == nil {
		t.Fatal("expected nil prometheus client error")
	} else if !strings.Contains(err.Error(), "prometheus client is required") {
		t.Fatalf("unexpected error: %v", err)
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
