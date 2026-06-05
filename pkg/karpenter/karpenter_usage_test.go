package karpenter

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/api"
	prometheusModel "github.com/prometheus/common/model"
)

type fakeMetricsQuerier struct {
	usageByResource    map[string]float64
	requestByResource  map[string]float64
	returnEmptyOnUsage bool
	emptyUsageMetrics  map[string]bool
}

func (f fakeMetricsQuerier) Query(ctx context.Context, query string) (prometheusModel.Vector, error) {
	resourceType := "unknown"
	if strings.Contains(query, "resource_type='memory'") || strings.Contains(query, `resource_type="memory"`) {
		resourceType = "memory"
	}
	if strings.Contains(query, "resource_type='cpu'") || strings.Contains(query, `resource_type="cpu"`) {
		resourceType = "cpu"
	}

	for _, metricName := range nodepoolUsageMetricNames {
		if !strings.Contains(query, metricName) {
			continue
		}
		if f.returnEmptyOnUsage || f.emptyUsageMetrics[metricName] {
			return prometheusModel.Vector{}, nil
		}
		return vectorOf(f.usageByResource[resourceType]), nil
	}
	if strings.Contains(query, "karpenter_nodes_total_pod_requests") {
		return vectorOf(f.requestByResource[resourceType]), nil
	}
	return prometheusModel.Vector{}, nil
}

type recordingMetricsQuerier struct {
	queries []string
}

func (r *recordingMetricsQuerier) Query(ctx context.Context, query string) (prometheusModel.Vector, error) {
	r.queries = append(r.queries, query)
	if strings.Contains(query, "karpenter_nodes_total_pod_requests") {
		return vectorOf(50), nil
	}
	return vectorOf(100), nil
}

func vectorOf(value float64) prometheusModel.Vector {
	return prometheusModel.Vector{
		&prometheusModel.Sample{
			Value: prometheusModel.SampleValue(value),
		},
	}
}

func vectorOfMany(values ...float64) prometheusModel.Vector {
	vector := make(prometheusModel.Vector, 0, len(values))
	for _, value := range values {
		vector = append(vector, &prometheusModel.Sample{
			Value: prometheusModel.SampleValue(value),
		})
	}
	return vector
}

func TestPrometheusQuerierAllowsNilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1234.5,"1"]}]}}`))
	}))
	defer server.Close()

	client, err := api.NewClient(api.Config{Address: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	vector, err := NewPrometheusQuerier(client).Query(nil, "up")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(vector) != 1 {
		t.Fatalf("vector length = %d, want=1", len(vector))
	}
	if got := vector[0].Value.String(); got != "1" {
		t.Fatalf("sample value = %s, want=1", got)
	}
}

func TestGetKarpenterNodepoolUsageFallsBackToLegacyMetric(t *testing.T) {
	querier := fakeMetricsQuerier{
		usageByResource: map[string]float64{"memory": 200 * 1000 * 1000 * 1000},
		emptyUsageMetrics: map[string]bool{
			"karpenter_nodepools_usage": true,
		},
	}

	client := NewClient("nodepool-a", querier)
	got, err := client.GetKarpenterNodepoolUsage(context.Background(), "memory")
	if err != nil {
		t.Fatalf("GetKarpenterNodepoolUsage() error = %v", err)
	}
	if got != 200 {
		t.Fatalf("GetKarpenterNodepoolUsage() = %.0f, want=200", got)
	}
}

func TestGetKarpenterNodepoolUsageQueriesSingleAggregatedSeries(t *testing.T) {
	querier := &recordingMetricsQuerier{}
	client := NewClientForCluster("nodepool-a", "cluster-a", querier)

	_, err := client.GetKarpenterNodepoolUsage(context.Background(), "memory")
	if err != nil {
		t.Fatalf("GetKarpenterNodepoolUsage() error = %v", err)
	}
	if len(querier.queries) != 1 {
		t.Fatalf("query count = %d, want=1", len(querier.queries))
	}
	query := querier.queries[0]
	if !strings.Contains(query, `max(karpenter_nodepools_usage{`) {
		t.Fatalf("query should aggregate nodepool usage with max: %s", query)
	}
	if !strings.Contains(query, `cluster="cluster-a"`) {
		t.Fatalf("query missing cluster matcher: %s", query)
	}
}

func TestNewClientForClusterTrimsLabelMatchers(t *testing.T) {
	querier := &recordingMetricsQuerier{}
	client := NewClientForCluster(" nodepool-a ", " cluster-a ", querier)

	_, err := client.GetKarpenterNodepoolUsage(context.Background(), "memory")
	if err != nil {
		t.Fatalf("GetKarpenterNodepoolUsage() error = %v", err)
	}
	if len(querier.queries) != 1 {
		t.Fatalf("query count = %d, want=1", len(querier.queries))
	}
	query := querier.queries[0]
	for _, want := range []string{
		`nodepool="nodepool-a"`,
		`cluster="cluster-a"`,
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q: %s", want, query)
		}
	}
	for _, unexpected := range []string{
		`nodepool=" nodepool-a "`,
		`cluster=" cluster-a "`,
	} {
		if strings.Contains(query, unexpected) {
			t.Fatalf("query contains untrimmed matcher %q: %s", unexpected, query)
		}
	}
}

func TestClientRejectsNilMetricsQuerier(t *testing.T) {
	client := NewClientForCluster("nodepool-a", "cluster-a", nil)

	for _, call := range []struct {
		name string
		run  func() error
	}{
		{
			name: "nodepool usage",
			run: func() error {
				_, err := client.GetKarpenterNodepoolUsage(context.Background(), "memory")
				return err
			},
		},
		{
			name: "pod request",
			run: func() error {
				_, err := client.GetKarpenterPodRequest(context.Background(), "memory")
				return err
			},
		},
		{
			name: "allocate rate",
			run: func() error {
				_, err := client.GetAllocateRate(context.Background(), "memory")
				return err
			},
		},
	} {
		t.Run(call.name, func(t *testing.T) {
			err := call.run()
			if err == nil {
				t.Fatal("expected nil metrics querier error")
			}
			if !strings.Contains(err.Error(), "metrics querier is required") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientRejectsNilReceiver(t *testing.T) {
	var client *Client

	_, err := client.GetKarpenterNodepoolUsage(context.Background(), "memory")
	if err == nil {
		t.Fatal("expected nil karpenter client error")
	}
	if !strings.Contains(err.Error(), "karpenter client is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetKarpenterPodRequestDeduplicatesScrapeTargets(t *testing.T) {
	querier := &recordingMetricsQuerier{}
	client := NewClientForCluster("nodepool-a", "cluster-a", querier)

	_, err := client.GetKarpenterPodRequest(context.Background(), "cpu")
	if err != nil {
		t.Fatalf("GetKarpenterPodRequest() error = %v", err)
	}
	if len(querier.queries) != 1 {
		t.Fatalf("query count = %d, want=1", len(querier.queries))
	}
	query := querier.queries[0]
	for _, want := range []string{
		"sum(max without (container,endpoint,instance,job,namespace,pod,service) (karpenter_nodes_total_pod_requests{",
		"sum(max without (container,endpoint,instance,job,namespace,pod,service) (karpenter_nodes_total_daemon_requests{",
		`nodepool="nodepool-a"`,
		`cluster="cluster-a"`,
		`resource_type="cpu"`,
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q: %s", want, query)
		}
	}
}

func TestGetAllocateRateScopesQueriesByCluster(t *testing.T) {
	querier := &recordingMetricsQuerier{}
	client := NewClientForCluster("nodepool-a", "cluster-a", querier)

	_, err := client.GetAllocateRate(context.Background(), "cpu")
	if err != nil {
		t.Fatalf("GetAllocateRate() error = %v", err)
	}

	if len(querier.queries) != 2 {
		t.Fatalf("query count = %d, want=2", len(querier.queries))
	}
	for _, query := range querier.queries {
		if !strings.Contains(query, `nodepool="nodepool-a"`) {
			t.Fatalf("query missing nodepool matcher: %s", query)
		}
		if !strings.Contains(query, `cluster="cluster-a"`) {
			t.Fatalf("query missing cluster matcher: %s", query)
		}
		if !strings.Contains(query, `resource_type="cpu"`) {
			t.Fatalf("query missing resource_type matcher: %s", query)
		}
	}
}

func TestParseUsageResultRejectsAmbiguousResults(t *testing.T) {
	_, err := parseUsageResult(vectorOfMany(1, 2), "cpu")
	if err == nil {
		t.Fatal("expected ambiguous result error")
	}
}

func TestParseUsageResultRejectsInvalidSampleValue(t *testing.T) {
	_, err := parseUsageResult(vectorOf(math.NaN()), "cpu")
	if err == nil {
		t.Fatal("expected invalid sample value error")
	}
}

func TestGetAllocateRate(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		querier      fakeMetricsQuerier
		wantErr      bool
		wantRate     int
	}{
		{
			name:         "정상 비율 계산",
			resourceType: "memory",
			querier: fakeMetricsQuerier{
				usageByResource:   map[string]float64{"memory": 200 * 1000 * 1000 * 1000},
				requestByResource: map[string]float64{"memory": 100 * 1000 * 1000 * 1000},
			},
			wantErr:  false,
			wantRate: 50,
		},
		{
			name:         "분모 0이면 오류 반환",
			resourceType: "cpu",
			querier: fakeMetricsQuerier{
				usageByResource:   map[string]float64{"cpu": 0},
				requestByResource: map[string]float64{"cpu": 10},
			},
			wantErr: true,
		},
		{
			name:         "Prometheus 빈 결과면 오류 반환",
			resourceType: "memory",
			querier: fakeMetricsQuerier{
				returnEmptyOnUsage: true,
			},
			wantErr: true,
		},
		{
			name:         "지원하지 않는 리소스 타입",
			resourceType: "disk",
			querier: fakeMetricsQuerier{
				usageByResource:   map[string]float64{"disk": 100},
				requestByResource: map[string]float64{"disk": 50},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient("nodepool-a", tt.querier)
			got, err := client.GetAllocateRate(context.Background(), tt.resourceType)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetAllocateRate() error = %v, wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.wantRate {
				t.Fatalf("GetAllocateRate() = %d, want=%d", got, tt.wantRate)
			}
		})
	}
}
