package karpenter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prometheusModel "github.com/prometheus/common/model"
)

var nodepoolUsageMetricNames = []string{
	"karpenter_nodepools_usage",
	"karpenter_nodepool_usage",
}

// MetricsQuerier is a minimal contract for querying metrics backends.
type MetricsQuerier interface {
	Query(ctx context.Context, query string) (prometheusModel.Vector, error)
}

// AllocateRateProvider exposes allocation-rate calculations used by drain logic.
type AllocateRateProvider interface {
	GetAllocateRate(ctx context.Context, resourceType string) (int, error)
}

// PrometheusQuerier is a MetricsQuerier backed by Prometheus API.
type PrometheusQuerier struct {
	api v1.API
}

// NewPrometheusQuerier creates a Prometheus-backed metrics querier.
func NewPrometheusQuerier(client api.Client) *PrometheusQuerier {
	return &PrometheusQuerier{
		api: v1.NewAPI(client),
	}
}

// Query executes a Prometheus instant query.
func (p *PrometheusQuerier) Query(ctx context.Context, query string) (prometheusModel.Vector, error) {
	queryCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		queryCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	result, warnings, err := p.api.Query(queryCtx, query, timeNow())
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		slog.Warn("프로메테우스 쿼리 warning", "warnings", warnings)
	}

	vector, ok := result.(prometheusModel.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from Prometheus")
	}
	return vector, nil
}

// Client provides Karpenter usage/allocation queries.
type Client struct {
	nodepoolName string
	querier      MetricsQuerier
}

// NewClient creates a Karpenter metrics client.
func NewClient(nodepoolName string, querier MetricsQuerier) *Client {
	return &Client{
		nodepoolName: nodepoolName,
		querier:      querier,
	}
}

// GetKarpenterPodRequest returns pod request usage for a resource type.
func (c *Client) GetKarpenterPodRequest(ctx context.Context, resourceType string) (float64, error) {
	query := fmt.Sprintf(
		"sum(karpenter_nodes_total_pod_requests{nodepool='%s',resource_type='%s'} + karpenter_nodes_total_daemon_requests{nodepool='%s',resource_type='%s'})",
		c.nodepoolName,
		resourceType,
		c.nodepoolName,
		resourceType,
	)
	slog.Info("query", "query", query)

	result, err := c.querier.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query pod request: %w", err)
	}
	return parseUsageResult(result, resourceType)
}

// GetKarpenterNodepoolUsage returns nodepool usage for a resource type.
func (c *Client) GetKarpenterNodepoolUsage(ctx context.Context, resourceType string) (float64, error) {
	var emptyErr error
	for _, metricName := range nodepoolUsageMetricNames {
		query := fmt.Sprintf(
			"%s{nodepool='%s', resource_type='%s'}",
			metricName,
			c.nodepoolName,
			resourceType,
		)
		slog.Info("query", "query", query)

		result, err := c.querier.Query(ctx, query)
		if err != nil {
			return 0, fmt.Errorf("query nodepool usage: %w", err)
		}
		if len(result) == 0 {
			emptyErr = fmt.Errorf("empty prometheus result for metric %s resource type %s", metricName, resourceType)
			continue
		}
		return parseUsageResult(result, resourceType)
	}

	if emptyErr == nil {
		emptyErr = errors.New("empty prometheus result for nodepool usage")
	}
	return 0, emptyErr
}

// GetAllocateRate returns pod-request to nodepool-usage ratio in percent.
func (c *Client) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	nodepoolUsage, err := c.GetKarpenterNodepoolUsage(ctx, resourceType)
	if err != nil {
		return 0, err
	}
	if nodepoolUsage <= 0 {
		return 0, fmt.Errorf("invalid %s nodepool usage: %.2f", resourceType, nodepoolUsage)
	}

	podRequest, err := c.GetKarpenterPodRequest(ctx, resourceType)
	if err != nil {
		return 0, err
	}

	switch resourceType {
	case "memory":
		slog.Info("Karpenter", "nodepoolUsage", fmt.Sprintf("%.0f GB", nodepoolUsage))
		slog.Info("Karpenter", "podRequest", fmt.Sprintf("%.0f GB", podRequest))
	case "cpu":
		slog.Info("Karpenter", "nodepoolUsage", fmt.Sprintf("%.0f vCPU", nodepoolUsage))
		slog.Info("Karpenter", "podRequest", fmt.Sprintf("%.0f vCPU", podRequest))
	default:
		return 0, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	allocateRate := math.Round((podRequest / nodepoolUsage) * 100)
	return int(allocateRate), nil
}

func parseUsageResult(result prometheusModel.Vector, resourceType string) (float64, error) {
	if len(result) == 0 {
		return 0, fmt.Errorf("empty prometheus result for resource type %s", resourceType)
	}

	sample := result[0]
	switch resourceType {
	case "memory":
		usage, err := strconv.ParseFloat(sample.Value.String(), 64)
		if err != nil {
			return 0, fmt.Errorf("parse memory usage: %w", err)
		}
		return usage / (1000 * 1000 * 1000), nil
	case "cpu":
		usage, err := strconv.ParseFloat(sample.Value.String(), 64)
		if err != nil {
			return 0, fmt.Errorf("parse cpu usage: %w", err)
		}
		return usage, nil
	default:
		return 0, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

var timeNow = func() time.Time {
	return time.Now()
}
