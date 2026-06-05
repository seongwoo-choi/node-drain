package node

import (
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

type allocateRateProvider interface {
	GetAllocateRate(ctx context.Context, resourceType string) (int, error)
}

type DrainNodeSelectionStrategy string

const (
	DrainNodeSelectionOldest     DrainNodeSelectionStrategy = "oldest"
	DrainNodeSelectionEmptyFirst DrainNodeSelectionStrategy = "empty-first"
	DrainNodeSelectionLeastPods  DrainNodeSelectionStrategy = "least-pods"
	DrainNodeSelectionMostPods   DrainNodeSelectionStrategy = "most-pods"
)

// DrainDependencies defines external dependencies for node drain.
type DrainDependencies struct {
	AllocateRateProvider allocateRateProvider
	Notifier             notification.Notifier
}

// DrainConfig defines node drain behavior.
type DrainConfig struct {
	NodepoolName          string
	Eviction              *pod.EvictionConfig
	DryRun                bool
	NodeSelectionStrategy DrainNodeSelectionStrategy
	SkipUnschedulable     bool
}

// DefaultDrainConfig returns default drain settings.
func DefaultDrainConfig(nodepoolName string) DrainConfig {
	return DrainConfig{
		NodepoolName:          nodepoolName,
		Eviction:              pod.DefaultEvictionConfig(),
		NodeSelectionStrategy: DrainNodeSelectionOldest,
	}
}

// NodeDrain cordons and drains selected nodes from a nodepool.
func NodeDrain(ctx context.Context, clientSet kubernetes.Interface, deps DrainDependencies, cfg DrainConfig) ([]types.NodeDrainResult, error) {
	report, err := NodeDrainWithReport(ctx, clientSet, deps, cfg)
	return report.Results, err
}

// NodeDrainWithReport cordons and drains selected nodes from a nodepool and returns aggregate execution metadata.
func NodeDrainWithReport(ctx context.Context, clientSet kubernetes.Interface, deps DrainDependencies, cfg DrainConfig) (types.NodeDrainReport, error) {
	report := types.NodeDrainReport{}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.Eviction = normalizeDrainEvictionConfig(cfg.Eviction)
	cfg.NodepoolName = strings.TrimSpace(cfg.NodepoolName)
	if cfg.NodepoolName == "" {
		return report, fmt.Errorf("nodepool name is required")
	}
	if clientSet == nil {
		return report, fmt.Errorf("kubernetes client is required")
	}
	report.Summary.TargetNodepool = cfg.NodepoolName
	report.Summary.DryRun = cfg.DryRun
	if _, err := parseNodeSelectionStrategy(cfg.NodeSelectionStrategy); err != nil {
		return report, err
	}

	nodepoolNodes, err := getNodepoolNodes(ctx, clientSet, cfg.NodepoolName)
	if err != nil {
		return report, err
	}
	report.Summary.TotalNodesInNodepool = len(nodepoolNodes)

	if len(nodepoolNodes) > 0 && deps.AllocateRateProvider == nil {
		return report, fmt.Errorf("allocate rate provider is required")
	}

	drainNodeCount, err := getDrainNodeCount(ctx, deps, len(nodepoolNodes))
	if err != nil {
		return report, err
	}
	report.Summary.PlannedDrainNodeCount = drainNodeCount
	slog.Info("드레인 할 노드 개수", "drainNodeCount", drainNodeCount)

	nodesToDrain, err := selectNodesToDrain(ctx, clientSet, nodepoolNodes, drainNodeCount, cfg)
	if err != nil {
		return report, err
	}
	report.Summary.SelectedDrainNodeCount = len(nodesToDrain)
	slog.Info("선택된 드레인 대상 노드", "strategy", normalizeNodeSelectionStrategy(cfg.NodeSelectionStrategy), "requested", drainNodeCount, "selected", len(nodesToDrain))

	results, err := handleDrain(ctx, clientSet, nodesToDrain, deps, cfg, &report.Summary)
	report.Results = results
	finalizeNodeDrainSummary(&report.Summary, results)
	return report, err
}

func getNodepoolNodes(ctx context.Context, clientSet kubernetes.Interface, nodepoolName string) ([]coreV1.Node, error) {
	nodes, err := clientSet.CoreV1().Nodes().List(ctx, metaV1.ListOptions{
		LabelSelector: fmt.Sprintf("karpenter.sh/nodepool=%s", nodepoolName),
	})
	if err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

func getDrainNodeCount(ctx context.Context, deps DrainDependencies, lenNodes int) (int, error) {
	slog.Info("노드 사용률 조회 중")
	slog.Info("현재 노드 개수", "lenNodes", lenNodes)

	if deps.Notifier != nil {
		if err := deps.Notifier.SendNodeCount(ctx, lenNodes); err != nil {
			slog.Error("초기 노드 수 알림 전송 실패", "error", err)
		}
	}

	if lenNodes == 0 {
		slog.Info("노드풀이 비어 있어 드레인을 수행하지 않습니다.")
		return 0, nil
	}

	memoryAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "memory")
	if err != nil {
		return 0, err
	}
	cpuAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "cpu")
	if err != nil {
		return 0, err
	}

	if deps.Notifier != nil {
		if err := deps.Notifier.SendKarpenterAllocateRate(ctx, memoryAllocateRate, cpuAllocateRate); err != nil {
			slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		}
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	maxAllocateRate := int(math.Max(float64(memoryAllocateRate), float64(cpuAllocateRate)))
	slog.Info("최대 사용률", "maxAllocateRate", maxAllocateRate)

	opts := GetDrainPolicyOptionsFromEnv()
	blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditionsWithContext(ctx, maxAllocateRate, opts)
	if safetyErr != nil {
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

func selectNodesToDrain(ctx context.Context, clientSet kubernetes.Interface, nodes []coreV1.Node, drainNodeCount int, cfg DrainConfig) ([]coreV1.Node, error) {
	if drainNodeCount <= 0 || len(nodes) == 0 {
		return []coreV1.Node{}, nil
	}

	strategy, err := parseNodeSelectionStrategy(cfg.NodeSelectionStrategy)
	if err != nil {
		return nil, err
	}

	candidates := make([]coreV1.Node, 0, len(nodes))
	for _, n := range nodes {
		if strings.TrimSpace(n.Labels["karpenter.sh/nodepool"]) != cfg.NodepoolName {
			continue
		}
		if cfg.SkipUnschedulable && n.Spec.Unschedulable {
			slog.Info("이미 cordon된 노드를 드레인 후보에서 제외", "nodeName", n.Name)
			continue
		}
		candidates = append(candidates, n)
	}

	switch strategy {
	case DrainNodeSelectionOldest:
		sortNodesByAge(candidates)
	case DrainNodeSelectionEmptyFirst, DrainNodeSelectionLeastPods, DrainNodeSelectionMostPods:
		sortedNodes, sortErr := sortNodesByPodCount(ctx, clientSet, candidates, strategy)
		if sortErr != nil {
			return nil, sortErr
		}
		candidates = sortedNodes
	}

	if drainNodeCount > len(candidates) {
		drainNodeCount = len(candidates)
	}
	return candidates[:drainNodeCount], nil
}

func parseNodeSelectionStrategy(strategy DrainNodeSelectionStrategy) (DrainNodeSelectionStrategy, error) {
	switch DrainNodeSelectionStrategy(strings.ToLower(strings.TrimSpace(string(strategy)))) {
	case "", DrainNodeSelectionOldest:
		return DrainNodeSelectionOldest, nil
	case DrainNodeSelectionEmptyFirst:
		return DrainNodeSelectionEmptyFirst, nil
	case DrainNodeSelectionLeastPods:
		return DrainNodeSelectionLeastPods, nil
	case DrainNodeSelectionMostPods:
		return DrainNodeSelectionMostPods, nil
	default:
		return "", fmt.Errorf("지원하지 않는 드레인 노드 선택 전략: %s", strategy)
	}
}

func normalizeNodeSelectionStrategy(strategy DrainNodeSelectionStrategy) DrainNodeSelectionStrategy {
	parsed, err := parseNodeSelectionStrategy(strategy)
	if err != nil {
		return strategy
	}
	return parsed
}

func sortNodesByAge(nodes []coreV1.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].CreationTimestamp.Equal(&nodes[j].CreationTimestamp) {
			return nodes[i].Name < nodes[j].Name
		}
		return nodes[i].CreationTimestamp.Before(&nodes[j].CreationTimestamp)
	})
}

type nodeDrainCandidate struct {
	node     coreV1.Node
	podCount int
}

func sortNodesByPodCount(ctx context.Context, clientSet kubernetes.Interface, nodes []coreV1.Node, strategy DrainNodeSelectionStrategy) ([]coreV1.Node, error) {
	candidates := make([]nodeDrainCandidate, 0, len(nodes))
	for _, n := range nodes {
		pods, err := pod.GetNonCriticalPods(ctx, clientSet, n.Name)
		if err != nil {
			return nil, fmt.Errorf("노드 %s 파드 수 조회 실패: %w", n.Name, err)
		}
		candidates = append(candidates, nodeDrainCandidate{
			node:     n,
			podCount: len(pods),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].podCount != candidates[j].podCount {
			if strategy == DrainNodeSelectionMostPods {
				return candidates[i].podCount > candidates[j].podCount
			}
			return candidates[i].podCount < candidates[j].podCount
		}
		if candidates[i].node.CreationTimestamp.Equal(&candidates[j].node.CreationTimestamp) {
			return candidates[i].node.Name < candidates[j].node.Name
		}
		return candidates[i].node.CreationTimestamp.Before(&candidates[j].node.CreationTimestamp)
	})

	sortedNodes := make([]coreV1.Node, 0, len(candidates))
	for _, c := range candidates {
		slog.Info("드레인 후보 노드 평가", "nodeName", c.node.Name, "podCount", c.podCount, "strategy", strategy)
		sortedNodes = append(sortedNodes, c.node)
	}
	return sortedNodes, nil
}

func handleDrain(ctx context.Context, clientSet kubernetes.Interface, nodes []coreV1.Node, deps DrainDependencies, cfg DrainConfig, summary *types.NodeDrainSummary) ([]types.NodeDrainResult, error) {
	results := make([]types.NodeDrainResult, 0, len(nodes))
	if len(nodes) == 0 {
		return results, nil
	}

	opts := GetDrainPolicyOptionsFromEnv()
	progressive := parseEnvBool("DRAIN_PROGRESSIVE", true)
	shouldSafetyRecheck := progressive && (opts.SafetyMaxAllocateRate > 0 || len(opts.SafetyQueries) > 0)

	for i, n := range nodes {
		if strings.TrimSpace(n.Labels["karpenter.sh/nodepool"]) != cfg.NodepoolName {
			continue
		}

		start := time.Now()
		result := types.NodeDrainResult{
			NodeName:     n.Name,
			InstanceType: n.Labels["beta.kubernetes.io/instance-type"],
			NodepoolName: cfg.NodepoolName,
			Age:          n.CreationTimestamp.Format(time.RFC3339),
			StartedAt:    start.Format(time.RFC3339),
			DryRun:       cfg.DryRun,
		}

		if cfg.DryRun {
			plannedPods, planErr := getNodeDrainPodPlan(ctx, clientSet, n.Name)
			result.PlannedPods = plannedPods
			result.DurationSeconds = int64(time.Since(start).Seconds())
			if planErr != nil {
				result.Success = false
				result.FailureReason = planErr.Error()
				results = append(results, result)
				return results, fmt.Errorf("노드 %s dry-run 계획 생성 실패: %w", n.Name, planErr)
			}
			result.Success = true
			results = append(results, result)
			slog.Info("dry-run 노드 드레인 계획", "nodeName", n.Name, "plannedPods", len(plannedPods))
			continue
		}

		if err := CordonNode(ctx, clientSet, n.Name); err != nil {
			result.Success = false
			result.FailureReason = err.Error()
			result.DurationSeconds = int64(time.Since(start).Seconds())
			results = append(results, result)
			return results, fmt.Errorf("노드 %s cordon 실패: %w", n.Name, err)
		}
		incrementCordonedNodeCount(summary)

		podReport, err := drainSingleNode(ctx, clientSet, n.Name, cfg.Eviction)
		addPodEvictionReportToSummary(summary, podReport)
		if err != nil {
			result.Success = false
			result.FailureReason = err.Error()
			result.DurationSeconds = int64(time.Since(start).Seconds())
			results = append(results, result)
			return results, fmt.Errorf("노드 %s 드레인 실패: %w", n.Name, err)
		}

		result.Success = true
		result.DurationSeconds = int64(time.Since(start).Seconds())
		results = append(results, result)

		if shouldSafetyRecheck && i < len(nodes)-1 {
			memoryAllocateRate, memErr := deps.AllocateRateProvider.GetAllocateRate(ctx, "memory")
			cpuAllocateRate, cpuErr := deps.AllocateRateProvider.GetAllocateRate(ctx, "cpu")
			if memErr != nil || cpuErr != nil {
				if opts.SafetyFailClosed {
					reason := fmt.Sprintf("안전 재평가 실패: memoryError=%v cpuError=%v", memErr, cpuErr)
					markDrainStoppedBySafety(summary, reason)
					slog.Warn("안전 재평가 실패로 추가 드레인을 중단합니다.", "memoryError", memErr, "cpuError", cpuErr)
					break
				}
				slog.Warn("안전 재평가 실패를 fail-open으로 처리합니다.", "memoryError", memErr, "cpuError", cpuErr)
				continue
			}

			maxRate := int(math.Max(float64(memoryAllocateRate), float64(cpuAllocateRate)))
			blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditionsWithContext(ctx, maxRate, opts)
			if safetyErr != nil {
				slog.Warn("안전 재평가 중 오류", "error", safetyErr, "blocked", blocked, "reason", reason)
			}
			if blocked {
				markDrainStoppedBySafety(summary, reason)
				slog.Warn("안전 조건에 의해 추가 드레인을 중단합니다.", "reason", reason)
				break
			}
		}
	}

	memoryAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "memory")
	memErr := err
	cpuAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "cpu")
	cpuErr := err
	if memErr != nil || cpuErr != nil {
		appendDrainSummaryWarning(summary, fmt.Sprintf("최종 Karpenter 사용률 조회 실패: memoryError=%v cpuError=%v", memErr, cpuErr))
		slog.Warn("최종 Karpenter 사용률 조회 실패", "memoryError", memErr, "cpuError", cpuErr)
		return results, nil
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	if deps.Notifier != nil {
		if err := deps.Notifier.SendKarpenterAllocateRate(ctx, memoryAllocateRate, cpuAllocateRate); err != nil {
			slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		}
	}

	return results, nil
}

func finalizeNodeDrainSummary(summary *types.NodeDrainSummary, results []types.NodeDrainResult) {
	if summary == nil {
		return
	}

	errorReasons := make([]string, 0)
	for _, result := range results {
		if result.Success {
			summary.SuccessfulNodeCount++
			if !result.DryRun {
				summary.DrainedNodeCount++
			}
		} else {
			summary.FailedNodeCount++
			if result.FailureReason != "" {
				errorReasons = append(errorReasons, result.FailureReason)
			}
		}
		if result.DryRun {
			summary.PlannedPodCount += len(result.PlannedPods)
			summary.TotalPods += len(result.PlannedPods)
		}
	}
	summary.TopErrorReasons = topUniqueStrings(errorReasons, 3)
}

func addPodEvictionReportToSummary(summary *types.NodeDrainSummary, report pod.EvictionReport) {
	if summary == nil {
		return
	}
	summary.TotalPods += report.TotalPods
	summary.EvictedPods += report.EvictedPods
	summary.DeletedPods += report.DeletedPods
	summary.ForceDeletedPods += report.ForceDeletedPods
	summary.PDBBlockedPods += report.PDBBlockedPods
	summary.ForcedByFallback += report.ForcedByFallback
	summary.ProblemPodsForced += report.ProblemPodsForced
}

func incrementCordonedNodeCount(summary *types.NodeDrainSummary) {
	if summary == nil {
		return
	}
	summary.CordonedNodeCount++
}

func markDrainStoppedBySafety(summary *types.NodeDrainSummary, reason string) {
	if summary == nil {
		return
	}
	summary.StoppedBySafety = true
	summary.StopSafetyReason = reason
}

func appendDrainSummaryWarning(summary *types.NodeDrainSummary, warning string) {
	if summary == nil || strings.TrimSpace(warning) == "" {
		return
	}
	summary.Warnings = append(summary.Warnings, warning)
}

func topUniqueStrings(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, limit)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func getNodeDrainPodPlan(ctx context.Context, clientSet kubernetes.Interface, nodeName string) ([]types.NodeDrainPodPlan, error) {
	pods, err := pod.GetNonCriticalPods(ctx, clientSet, nodeName)
	if err != nil {
		return nil, err
	}

	plans := make([]types.NodeDrainPodPlan, 0, len(pods))
	for _, p := range pods {
		plan := types.NodeDrainPodPlan{
			Namespace: p.Namespace,
			Name:      p.Name,
			Phase:     string(p.Status.Phase),
		}
		if len(p.OwnerReferences) > 0 {
			plan.OwnerKind = p.OwnerReferences[0].Kind
			plan.OwnerName = p.OwnerReferences[0].Name
		}
		plans = append(plans, plan)
		slog.Info("dry-run 제거 대상 pod", "nodeName", nodeName, "namespace", plan.Namespace, "pod", plan.Name, "phase", plan.Phase, "ownerKind", plan.OwnerKind, "ownerName", plan.OwnerName)
	}
	return plans, nil
}

func drainSingleNode(ctx context.Context, clientSet kubernetes.Interface, nodeName string, cfg *pod.EvictionConfig) (pod.EvictionReport, error) {
	cfg = normalizeDrainEvictionConfig(cfg)

	report, err := pod.EvictPodsWithReport(ctx, clientSet, nodeName, cfg)
	if err != nil {
		return report, fmt.Errorf("노드 %s 파드 제거 실패: %w", nodeName, err)
	}

	if err := waitForPodsToTerminate(ctx, clientSet, nodeName, cfg); err != nil {
		return report, fmt.Errorf("노드 %s 파드 종료 대기 실패: %w", nodeName, err)
	}

	if cfg.PostEvictionNodeDelay > 0 {
		timer := time.NewTimer(cfg.PostEvictionNodeDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		case <-timer.C:
		}
	}

	return report, nil
}

func waitForPodsToTerminate(ctx context.Context, clientSet kubernetes.Interface, nodeName string, cfg *pod.EvictionConfig) error {
	slog.Info("노드에서 데몬셋 제외 파드 종료 대기 시작", "nodeName", nodeName)
	cfg = normalizeDrainEvictionConfig(cfg)

	waitCtx := ctx
	if cfg.NodeTerminationTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, cfg.NodeTerminationTimeout)
		defer cancel()
	}

	ticker := time.NewTicker(cfg.NodeTerminationCheckTick)
	defer ticker.Stop()

	if done, err := nonCriticalPodsTerminated(waitCtx, clientSet, nodeName); done || err != nil {
		return err
	}

	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("노드 %s 파드 종료 대기 타임아웃: %w", nodeName, waitCtx.Err())
		case <-ticker.C:
			if done, err := nonCriticalPodsTerminated(waitCtx, clientSet, nodeName); done || err != nil {
				return err
			}
		}
	}
}

func nonCriticalPodsTerminated(ctx context.Context, clientSet kubernetes.Interface, nodeName string) (bool, error) {
	pods, err := pod.GetNonCriticalPods(ctx, clientSet, nodeName)
	if err != nil {
		return false, fmt.Errorf("노드 %s 파드 조회 실패: %w", nodeName, err)
	}
	if len(pods) == 0 {
		slog.Info("데몬셋 제외 모든 Pod 종료 완료", "nodeName", nodeName)
		return true, nil
	}
	slog.Info("Pod 종료 대기 중", "nodeName", nodeName, "remainingPods", len(pods))
	return false, nil
}

func normalizeDrainEvictionConfig(cfg *pod.EvictionConfig) *pod.EvictionConfig {
	defaults := pod.DefaultEvictionConfig()
	if cfg == nil {
		return defaults
	}

	normalized := *cfg
	if normalized.NodeTerminationTimeout <= 0 {
		normalized.NodeTerminationTimeout = defaults.NodeTerminationTimeout
	}
	if normalized.NodeTerminationCheckTick <= 0 {
		normalized.NodeTerminationCheckTick = defaults.NodeTerminationCheckTick
	}
	if normalized.PostEvictionNodeDelay < 0 {
		normalized.PostEvictionNodeDelay = 0
	}
	return &normalized
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
