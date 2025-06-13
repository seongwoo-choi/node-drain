package karpenter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	prometheusModel "github.com/prometheus/common/model"
)

func newPrometheusTestServer(t *testing.T, responses map[string]string) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		var val string
		for k, v := range responses {
			if strings.Contains(q, k) {
				val = v
				break
			}
		}
		if val == "" {
			t.Fatalf("unexpected query: %s", q)
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"%s"]}]}}`, val)
	})
	return httptest.NewServer(handler)
}

func TestParseUsageResultMemory(t *testing.T) {
	sample := &prometheusModel.Sample{Value: 123000000000}
	vector := prometheusModel.Vector{sample}
	result, err := parseUsageResult(vector, "memory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 123 {
		t.Fatalf("expected 123, got %d", result)
	}
}

func TestParseUsageResultCPU(t *testing.T) {
	sample := &prometheusModel.Sample{Value: 5}
	vector := prometheusModel.Vector{sample}
	result, err := parseUsageResult(vector, "cpu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 5 {
		t.Fatalf("expected 5, got %d", result)
	}
}

func TestGetKarpenterNodepoolUsage(t *testing.T) {
	server := newPrometheusTestServer(t, map[string]string{"karpenter_nodepool_usage": "64000000000"})
	defer server.Close()

	t.Setenv("PROMETHEUS_ADDRESS", server.URL)
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "test")
	t.Setenv("NODEPOOL_NAME", "testpool")

	usage, err := GetKarpenterNodepoolUsage(nil, "memory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage != 64 {
		t.Fatalf("expected 64, got %d", usage)
	}
}

func TestGetKarpenterPodRequest(t *testing.T) {
	server := newPrometheusTestServer(t, map[string]string{"karpenter_nodes_total_pod_requests": "32000000000"})
	defer server.Close()

	t.Setenv("PROMETHEUS_ADDRESS", server.URL)
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "test")
	t.Setenv("NODEPOOL_NAME", "testpool")

	req, err := GetKarpenterPodRequest(nil, "memory")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req != 32 {
		t.Fatalf("expected 32, got %d", req)
	}
}

func TestGetAllocateRate(t *testing.T) {
	responses := map[string]string{
		"karpenter_nodepool_usage":           "200",
		"karpenter_nodes_total_pod_requests": "50",
	}
	server := newPrometheusTestServer(t, responses)
	defer server.Close()

	t.Setenv("PROMETHEUS_ADDRESS", server.URL)
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "test")
	t.Setenv("NODEPOOL_NAME", "testpool")

	rate, err := GetAllocateRate(nil, "cpu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != 25 {
		t.Fatalf("expected 25, got %d", rate)
	}
}
