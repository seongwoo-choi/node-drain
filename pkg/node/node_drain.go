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

// DrainDependencies defines external dependencies for node drain.
type DrainDependencies struct {
	AllocateRateProvider allocateRateProvider
	Notifier             notification.Notifier
}

// DrainConfig defines node drain behavior.
type DrainConfig struct {
	NodepoolName string
	Eviction     *pod.EvictionConfig
}

// DefaultDrainConfig returns default drain settings.
func DefaultDrainConfig(nodepoolName string) DrainConfig {
	return DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     pod.DefaultEvictionConfig(),
	}
}

// NodeDrain cordons and drains selected nodes from a nodepool.
func NodeDrain(ctx context.Context, clientSet kubernetes.Interface, deps DrainDependencies, cfg DrainConfig) ([]types.NodeDrainResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.Eviction = normalizeDrainEvictionConfig(cfg.Eviction)
	if cfg.NodepoolName == "" {
		return nil, fmt.Errorf("nodepool name is required")
	}
	if deps.AllocateRateProvider == nil {
		return nil, fmt.Errorf("allocate rate provider is required")
	}

	nodepoolNodes, err := getNodepoolNodes(ctx, clientSet, cfg.NodepoolName)
	if err != nil {
		return nil, err
	}

	sort.Slice(nodepoolNodes, func(i, j int) bool {
		return nodepoolNodes[i].CreationTimestamp.Before(&nodepoolNodes[j].CreationTimestamp)
	})

	drainNodeCount, err := getDrainNodeCount(ctx, deps, len(nodepoolNodes))
	if err != nil {
		return nil, err
	}
	slog.Info("드레인 할 노드 개수", "drainNodeCount", drainNodeCount)

	nodesToDrain := nodepoolNodes[:drainNodeCount]
	return handleDrain(ctx, clientSet, nodesToDrain, deps, cfg)
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
	blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditions(maxAllocateRate, opts)
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

func handleDrain(ctx context.Context, clientSet kubernetes.Interface, nodes []coreV1.Node, deps DrainDependencies, cfg DrainConfig) ([]types.NodeDrainResult, error) {
	results := make([]types.NodeDrainResult, 0, len(nodes))
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
		}

		if err := CordonNode(ctx, clientSet, n.Name); err != nil {
			result.Success = false
			result.FailureReason = err.Error()
			result.DurationSeconds = int64(time.Since(start).Seconds())
			results = append(results, result)
			return results, fmt.Errorf("노드 %s cordon 실패: %w", n.Name, err)
		}

		if err := drainSingleNode(ctx, clientSet, n.Name, cfg.Eviction); err != nil {
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
			if memErr != nil {
				slog.Warn("안전 재평가 중 Memory allocate rate 조회 실패(계속 진행)", "error", memErr)
			}
			cpuAllocateRate, cpuErr := deps.AllocateRateProvider.GetAllocateRate(ctx, "cpu")
			if cpuErr != nil {
				slog.Warn("안전 재평가 중 CPU allocate rate 조회 실패(계속 진행)", "error", cpuErr)
			}

			maxRate := int(math.Max(float64(memoryAllocateRate), float64(cpuAllocateRate)))
			blocked, reason, safetyErr := ShouldBlockDrainBySafetyConditions(maxRate, opts)
			if safetyErr != nil {
				slog.Warn("안전 재평가 중 오류", "error", safetyErr, "blocked", blocked, "reason", reason)
			}
			if blocked {
				slog.Warn("안전 조건에 의해 추가 드레인을 중단합니다.", "reason", reason)
				break
			}
		}
	}

	memoryAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "memory")
	if err != nil {
		return results, err
	}
	cpuAllocateRate, err := deps.AllocateRateProvider.GetAllocateRate(ctx, "cpu")
	if err != nil {
		return results, err
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

func drainSingleNode(ctx context.Context, clientSet kubernetes.Interface, nodeName string, cfg *pod.EvictionConfig) error {
	cfg = normalizeDrainEvictionConfig(cfg)

	if err := pod.EvictPods(ctx, clientSet, nodeName, cfg); err != nil {
		return fmt.Errorf("노드 %s 파드 제거 실패: %w", nodeName, err)
	}

	if err := waitForPodsToTerminate(ctx, clientSet, nodeName, cfg); err != nil {
		return fmt.Errorf("노드 %s 파드 종료 대기 실패: %w", nodeName, err)
	}

	if cfg.PostEvictionNodeDelay > 0 {
		timer := time.NewTimer(cfg.PostEvictionNodeDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}

	return nil
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

	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("노드 %s 파드 종료 대기 타임아웃: %w", nodeName, waitCtx.Err())
		case <-ticker.C:
			pods, err := pod.GetNonCriticalPods(waitCtx, clientSet, nodeName)
			if err != nil {
				return fmt.Errorf("노드 %s 파드 조회 실패: %w", nodeName, err)
			}
			if len(pods) == 0 {
				slog.Info("데몬셋 제외 모든 Pod 종료 완료", "nodeName", nodeName)
				return nil
			}
			slog.Info("Pod 종료 대기 중", "nodeName", nodeName, "remainingPods", len(pods))
		}
	}
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
