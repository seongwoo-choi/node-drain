package karpenter

import (
	"app/config"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"

	prometheusModel "github.com/prometheus/common/model"
	"k8s.io/client-go/kubernetes"
)

func GetKarpenterNodesAllocatable(clientSet kubernetes.Interface, resourceType string) (int64, error) {
	nodepool := os.Getenv("NODEPOOL_NAME")
	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 중 오류 발생", "error", err)
		return 0, err
	}

	query := fmt.Sprintf("sum(karpenter_nodes_allocatable{nodepool='%s', resource_type='%s'})", nodepool, resourceType)
	slog.Info("query", "query", query)

	result, err := config.QueryPrometheus(prometheusClient, query)
	if err != nil {
		slog.Error("Prometheus 쿼리 중 오류 발생", "error", err)
		return 0, err
	}

	return parseUsageResult(result, resourceType)
}

func GetKarpenterPodRequest(clientSet kubernetes.Interface, resourceType string) (int64, error) {
	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 중 오류 발생", "error", err)
		return 0, err
	}

	nodepool := os.Getenv("NODEPOOL_NAME")

	query := fmt.Sprintf("sum(karpenter_nodes_total_pod_requests{nodepool='%s',resource_type='%s'} + karpenter_nodes_total_daemon_requests{nodepool='%s',resource_type='%s'})", nodepool, resourceType, nodepool, resourceType)
	slog.Info("query", "query", query)

	result, err := config.QueryPrometheus(prometheusClient, query)
	if err != nil {
		slog.Error("Prometheus 쿼리 중 오류 발생", "error", err)
		return 0, err
	}

	return parseUsageResult(result, resourceType)
}

func GetKarpenterNodepoolUsage(clientSet kubernetes.Interface, resourceType string) (int64, error) {
	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 중 오류 발생", "error", err)
		return 0, err
	}

	nodepool := os.Getenv("NODEPOOL_NAME")

	query := fmt.Sprintf("karpenter_nodepool_usage{nodepool='%s', resource_type='%s'}", nodepool, resourceType)
	slog.Info("query", "query", query)

	result, err := config.QueryPrometheus(prometheusClient, query)
	if err != nil {
		slog.Error("Prometheus 쿼리 중 오류 발생", "error", err)
		return 0, err
	}

	return parseUsageResult(result, resourceType)
}

func GetAllocateRate(clientSet kubernetes.Interface, resourceType string) (int, error) {
	nodepoolUsage, err := GetKarpenterNodepoolUsage(clientSet, resourceType)
	if err != nil {
		slog.Error("Karpenter Nodepool Usage 사용량 조회 중 오류 발생", "error", err)
		return 0, err
	}

	podRequest, err := GetKarpenterPodRequest(clientSet, resourceType)
	if err != nil {
		slog.Error("Karpenter Allocatable 사용량 조회 중 오류 발생", "error", err)
		return 0, err
	}

	switch resourceType {
	case "memory":
		slog.Info("Karpenter", "nodepoolUsage", fmt.Sprintf("%d GB", nodepoolUsage))
		slog.Info("Karpenter", "podRequest", fmt.Sprintf("%d GB", podRequest))
	case "cpu":
		slog.Info("Karpenter", "nodepoolUsage", fmt.Sprintf("%d vCPU", nodepoolUsage))
		slog.Info("Karpenter", "podRequest", fmt.Sprintf("%d vCPU", podRequest))
	}

	allocateRate := math.Round(float64(podRequest) / float64(nodepoolUsage) * 100)
	return int(allocateRate), nil
}

func parseUsageResult(result prometheusModel.Vector, resourceType string) (int64, error) {
	for _, sample := range result {
		if resourceType == "memory" {
			usage, _ := strconv.ParseInt(sample.Value.String(), 10, 64)
			usageGB := usage / (1000 * 1000 * 1000)
			return usageGB, nil
		} else if resourceType == "cpu" {
			usage, _ := strconv.ParseFloat(sample.Value.String(), 64)
			return int64(usage), nil
		}
	}
	return 0, nil
}
