package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAllocateRateCommandRequiresTargetSettings(t *testing.T) {
	if allocateRateCmd.RunE == nil {
		t.Fatal("allocate-rate command RunE is nil")
	}

	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = ""
	nodepoolName = ""
	t.Setenv("PROMETHEUS_ADDRESS", "")
	t.Setenv("NODEPOOL_NAME", "")

	err := allocateRateCmd.RunE(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("expected required setting error, got nil")
	}
	if !strings.Contains(err.Error(), "prometheus-address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

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
