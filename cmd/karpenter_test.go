package cmd

import (
	"context"
	"strings"
	"testing"

	prometheusModel "github.com/prometheus/common/model"
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

func TestAllocateRateCommandRejectsUnexpectedArgs(t *testing.T) {
	if allocateRateCmd.Args == nil {
		t.Fatal("allocate-rate command Args is nil")
	}

	err := allocateRateCmd.Args(allocateRateCmd, []string{"false"})
	if err == nil {
		t.Fatal("expected unexpected arg error")
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

func TestNewKarpenterClientFromEnvScopesClusterWhenProvided(t *testing.T) {
	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	t.Setenv("NODEPOOL_NAME", "test-nodepool")
	t.Setenv("CLUSTER_NAME", "test-cluster")

	querier := &cmdRecordingMetricsQuerier{}
	client := newKarpenterClientFromEnv(querier)

	if _, err := client.GetKarpenterNodepoolUsage(context.Background(), "cpu"); err != nil {
		t.Fatalf("GetKarpenterNodepoolUsage failed: %v", err)
	}
	if len(querier.queries) != 1 {
		t.Fatalf("query count = %d, want=1", len(querier.queries))
	}
	query := querier.queries[0]
	for _, want := range []string{
		`nodepool="test-nodepool"`,
		`cluster="test-cluster"`,
		`resource_type="cpu"`,
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q: %s", want, query)
		}
	}
}

type cmdRecordingMetricsQuerier struct {
	queries []string
}

func (r *cmdRecordingMetricsQuerier) Query(ctx context.Context, query string) (prometheusModel.Vector, error) {
	r.queries = append(r.queries, query)
	return prometheusModel.Vector{
		&prometheusModel.Sample{
			Value: prometheusModel.SampleValue(100),
		},
	}, nil
}
