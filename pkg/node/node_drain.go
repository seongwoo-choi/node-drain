package node

import (
	"app/pkg/karpenter"
	"app/pkg/notification"
	"app/pkg/pod"
	"app/types"
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func NodeDrain(clientSet kubernetes.Interface) ([]types.NodeDrainResult, error) {
	results, _, err := NodeDrainWithSummary(clientSet)
	return results, err
}

func NodeDrainWithSummary(clientSet kubernetes.Interface) ([]types.NodeDrainResult, types.NodeDrainSummary, error) {
	nodepoolName := os.Getenv("NODEPOOL_NAME")
	summary := types.NodeDrainSummary{
		TargetNodepool: nodepoolName,
	}

	// 노드 목록을 가져옴
	nodepoolNode, err := getNodepoolNode(clientSet)
	if err != nil {
		return nil, summary, err
	}
	summary.TotalNodesInNodepool = len(nodepoolNode)

	// 생성 시간 기준으로 오름차순 정렬 (오래된 순)
	sort.Slice(nodepoolNode, func(i, j int) bool {
		return nodepoolNode[i].CreationTimestamp.Before(&nodepoolNode[j].CreationTimestamp)
	})

	// 드레인 할 노드 대수 가져오기
	drainNodeCount, err := getDrainNodeCount(clientSet, len(nodepoolNode))
	if err != nil {
		return nil, summary, err
	}
	slog.Info("드레인 할 노드 개수", "drainNodeCount", drainNodeCount)
	summary.PlannedDrainNodeCount = drainNodeCount

	// 노드 드레인 수행
	nodesToDrain := nodepoolNode[:drainNodeCount]
	for _, node := range nodesToDrain {
		if err := CordonNode(clientSet, node.Name); err != nil {
			return nil, summary, fmt.Errorf("노드 %s를 cordon하는 데 실패했습니다: %w", node.Name, err)
		}
	}

	return handleDrainWithSummary(clientSet, &coreV1.NodeList{Items: nodesToDrain}, drainNodeCount, nodepoolName, summary)
}

func getNodepoolNode(clientSet kubernetes.Interface) ([]coreV1.Node, error) {
	nodepoolName := os.Getenv("NODEPOOL_NAME")
	nodes, err := clientSet.CoreV1().Nodes().List(context.Background(), metaV1.ListOptions{
		LabelSelector: fmt.Sprintf("karpenter.sh/nodepool=%s", nodepoolName),
	})
	if err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

func getDrainNodeCount(clientSet kubernetes.Interface, lenNodes int) (int, error) {
	slog.Info("노드 사용률 조회 중")
	slog.Info("현재 노드 개수", "lenNodes", lenNodes)

	// 초기 노드 수를 Slack으로 전송
	if err := notification.SendNodeCount(lenNodes); err != nil {
		slog.Error("초기 노드 수 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	memoryAllocateRate, err := karpenter.GetAllocateRate(clientSet, "memory")
	if err != nil {
		return 0, err
	}

	cpuAllocateRate, err := karpenter.GetAllocateRate(clientSet, "cpu")
	if err != nil {
		return 0, err
	}

	if err := notification.SendKarpenterAllocateRate(memoryAllocateRate, cpuAllocateRate); err != nil {
		slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	maxAllocateRate := int(math.Max(float64(memoryAllocateRate), float64(cpuAllocateRate)))
	slog.Info("최대 사용률", "maxAllocateRate", maxAllocateRate)

	opts := GetDrainPolicyOptionsFromEnv()

	blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditions(maxAllocateRate, opts)
	if safetyErr != nil {
		// fail-open/fail-closed는 ShouldBlockDrainBySafetyConditions에서 결정
		slog.Warn("드레인 안전 조건 평가 중 오류", "error", safetyErr, "blocked", blocked, "reason", reason)
	}
	if blocked {
		slog.Warn("안전 조건에 의해 드레인을 수행하지 않습니다.", "reason", reason)
		return 0, nil
	}

	drainNodeCount := CalculateDrainNodeCount(lenNodes, maxAllocateRate, opts)
	slog.Info("드레인 정책", "policy", opts.Policy, "rounding", opts.Rounding, "minDrain", opts.MinDrain, "maxAbs", opts.MaxDrainAbsolute, "maxFraction", opts.MaxDrainFraction)
	slog.Info("드레인 할 노드 개수(정책 적용)", "drainNodeCount", drainNodeCount)

	return drainNodeCount, nil
}

func handleDrain(clientSet kubernetes.Interface, nodes *coreV1.NodeList, drainNodeCount int, nodepoolName string) ([]types.NodeDrainResult, error) {
	results, _, err := handleDrainWithSummary(clientSet, nodes, drainNodeCount, nodepoolName, types.NodeDrainSummary{TargetNodepool: nodepoolName})
	return results, err
}

func handleDrainWithSummary(clientSet kubernetes.Interface, nodes *coreV1.NodeList, drainNodeCount int, nodepoolName string, summary types.NodeDrainSummary) ([]types.NodeDrainResult, types.NodeDrainSummary, error) {
	var results []types.NodeDrainResult
	errorCounts := map[string]int{}

	// 점진적 드레인: 안전 조건이 설정된 경우, 노드 1대 처리 후 재평가 가능
	opts := GetDrainPolicyOptionsFromEnv()
	progressive := parseEnvBool("DRAIN_PROGRESSIVE", true)
	shouldSafetyRecheck := progressive && (opts.SafetyMaxAllocateRate > 0 || len(opts.SafetyQueries) > 0)

	// drainNodeCount 만큼만 가장 오래된 노드부터 처리
	for i := 0; i < drainNodeCount && i < len(nodes.Items); i++ {
		node := nodes.Items[i] // 이미 정렬된 상태이므로 순서대로 처리

		// nodepool 확인
		if strings.TrimSpace(node.Labels["karpenter.sh/nodepool"]) == nodepoolName {
			report, err := drainSingleNodeWithReport(clientSet, node.Name)
			summary.DrainedNodeCount++
			summary.TotalPods += report.TotalPods
			summary.EvictedPods += report.EvictedPods
			summary.DeletedPods += report.DeletedPods
			summary.ForceDeletedPods += report.ForceDeletedPods
			summary.PDBBlockedPods += report.PDBBlockedPods
			summary.ForcedByFallback += report.ForcedByFallback
			summary.ProblemPodsForced += report.ProblemPodsForced
			for k, v := range report.ErrorsByReason {
				errorCounts[k] += v
			}

			if err != nil {
				summary.TopErrorReasons = topKeysByCount(errorCounts, 3)
				return nil, summary, err
			}

			results = append(results, types.NodeDrainResult{
				NodeName:     node.Name,
				InstanceType: node.Labels["beta.kubernetes.io/instance-type"],
				NodepoolName: nodepoolName,
				Age:          node.CreationTimestamp.Format(time.RFC3339),
			})

			// 점진적 드레인: 노드 1대 처리 후 안전 조건 재평가
			if shouldSafetyRecheck && i < drainNodeCount-1 {
				memRate, mErr := karpenter.GetAllocateRate(clientSet, "memory")
				if mErr != nil {
					slog.Warn("안전 재평가 중 Memory allocate rate 조회 실패(계속 진행)", "error", mErr)
				}
				cpuRate, cErr := karpenter.GetAllocateRate(clientSet, "cpu")
				if cErr != nil {
					slog.Warn("안전 재평가 중 CPU allocate rate 조회 실패(계속 진행)", "error", cErr)
				}
				maxRate := int(math.Max(float64(memRate), float64(cpuRate)))
				blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditions(maxRate, opts)
				if safetyErr != nil {
					slog.Warn("안전 재평가 중 오류", "error", safetyErr, "blocked", blocked, "reason", reason)
				}
				if blocked {
					slog.Warn("안전 조건에 의해 추가 드레인을 중단합니다.", "reason", reason)
					summary.StoppedBySafety = true
					summary.StopSafetyReason = reason
					break
				}
			}
		}
	}

	memoryAllocateRate, err := karpenter.GetAllocateRate(clientSet, "memory")
	if err != nil {
		summary.TopErrorReasons = topKeysByCount(errorCounts, 3)
		return nil, summary, err
	}

	cpuAllocateRate, err := karpenter.GetAllocateRate(clientSet, "cpu")
	if err != nil {
		summary.TopErrorReasons = topKeysByCount(errorCounts, 3)
		return nil, summary, err
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	if err := notification.SendKarpenterAllocateRate(memoryAllocateRate, cpuAllocateRate); err != nil {
		slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	summary.TopErrorReasons = topKeysByCount(errorCounts, 3)
	return results, summary, nil
}

func drainSingleNode(clientSet kubernetes.Interface, nodeName string) error {
	_, err := drainSingleNodeWithReport(clientSet, nodeName)
	if err != nil {
		return fmt.Errorf("노드 %s 에서 파드를 제거하는 중 오류가 발생했습니다.: %w", nodeName, err)
	}
	return nil
}

func drainSingleNodeWithReport(clientSet kubernetes.Interface, nodeName string) (pod.EvictionReport, error) {
	config := pod.GetEvictionConfigFromEnv()

	report, err := pod.EvictPodsWithReport(clientSet, nodeName, config)
	if err != nil {
		return report, fmt.Errorf("노드 %s 에서 파드를 제거하는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	if err := waitForPodsToTerminate(clientSet, nodeName); err != nil {
		return report, fmt.Errorf("노드 %s 에서 파드가 종료되는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	time.Sleep(50 * time.Second)

	return report, nil
}

func parseEnvBool(key string, defaultValue bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultValue
	}
	return b
}

func topKeysByCount(m map[string]int, n int) []string {
	if n <= 0 || len(m) == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k: k, v: v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v == items[j].v {
			return items[i].k < items[j].k
		}
		return items[i].v > items[j].v
	})
	if len(items) > n {
		items = items[:n]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.k)
	}
	return out
}

func waitForPodsToTerminate(clientSet kubernetes.Interface, nodeName string) error {
	slog.Info("노드에서 데몬셋을 제외한 모든 파드가 종료될 때까지 기다리는 중", "nodeName", nodeName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			slog.Warn("파드 종료 대기 타임아웃", "nodeName", nodeName)
			return fmt.Errorf("노드 %s 에서 파드 종료 대기 중 타임아웃 발생", nodeName)
		default:
			pods, err := pod.GetNonCriticalPods(clientSet, nodeName)
			if err != nil {
				return fmt.Errorf("노드 %s 에서 데몬셋을 제외한 파드를 가져오는 중 오류가 발생했습니다.: %v", nodeName, err)
			}

			if len(pods) == 0 {
				slog.Info("데몬셋을 제외한 모든 Pod가 종료됨", "nodeName", nodeName)
				return nil
			}

			slog.Info("Pod 종료 대기 중", "nodeName", nodeName, "remainingPods", len(pods))
			time.Sleep(15 * time.Second)
		}
	}
}
