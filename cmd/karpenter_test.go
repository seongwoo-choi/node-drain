package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestHandleKarpenterAllocateRateReturnsPrometheusClientError(t *testing.T) {
	t.Setenv("PROMETHEUS_ADDRESS", "://bad")
	t.Setenv("PROMETHEUS_SCOPE_ORG_ID", "organization-dev")
	t.Setenv("NODEPOOL_NAME", "test-nodepool")

	err := handleKarpenterAllocateRate(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Prometheus 클라이언트 생성 실패") {
		t.Fatalf("unexpected error: %v", err)
	}
}
