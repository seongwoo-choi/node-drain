package karpenter

import (
	"context"
	"strings"
	"testing"

	prometheusModel "github.com/prometheus/common/model"
)

type fakeMetricsQuerier struct {
	usageByResource    map[string]float64
	requestByResource  map[string]float64
	returnEmptyOnUsage bool
}

func (f fakeMetricsQuerier) Query(ctx context.Context, query string) (prometheusModel.Vector, error) {
	resourceType := "unknown"
	if strings.Contains(query, "resource_type='memory'") {
		resourceType = "memory"
	}
	if strings.Contains(query, "resource_type='cpu'") {
		resourceType = "cpu"
	}

	if strings.Contains(query, "karpenter_nodepool_usage") {
		if f.returnEmptyOnUsage {
			return prometheusModel.Vector{}, nil
		}
		return vectorOf(f.usageByResource[resourceType]), nil
	}
	if strings.Contains(query, "karpenter_nodes_total_pod_requests") {
		return vectorOf(f.requestByResource[resourceType]), nil
	}
	return prometheusModel.Vector{}, nil
}

func vectorOf(value float64) prometheusModel.Vector {
	return prometheusModel.Vector{
		&prometheusModel.Sample{
			Value: prometheusModel.SampleValue(value),
		},
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
