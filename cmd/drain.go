package cmd

import (
	"app/config"
	"app/pkg/karpenter"
	"app/pkg/node"
	"app/pkg/notification"
	"app/pkg/pod"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

var (
	drainPolicy                string
	drainRounding              string
	drainMin                   int
	drainMaxAbsolute           int
	drainMaxFraction           float64
	drainStepRules             string
	drainSafetyMaxAllocateRate int
	drainSafetyQueries         string
	drainSafetyFailClosed      bool
	drainProgressive           bool

	podEvictionMode        string
	podForce               bool
	podForceProblemPods    bool
	podPDBToken            bool
	podPDBTokenMaxInFlight int
	podMaxConcurrent       int
	podMaxRetries          int
	podRetryBackoff        string
	podDeletionTimeout     string
	podCheckInterval       string
)

var drainCmd = &cobra.Command{
	Use:   "drain",
	Short: "노드 드레인 실행",
	RunE: func(command *cobra.Command, args []string) error {
		// drain 정책 플래그 -> env 주입
		_ = os.Setenv("DRAIN_POLICY", drainPolicy)
		_ = os.Setenv("DRAIN_ROUNDING", drainRounding)
		_ = os.Setenv("DRAIN_MIN", strconv.Itoa(drainMin))
		_ = os.Setenv("DRAIN_MAX_ABSOLUTE", strconv.Itoa(drainMaxAbsolute))
		_ = os.Setenv("DRAIN_MAX_FRACTION", strconv.FormatFloat(drainMaxFraction, 'f', -1, 64))
		_ = os.Setenv("DRAIN_STEP_RULES", drainStepRules)
		_ = os.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", strconv.Itoa(drainSafetyMaxAllocateRate))
		_ = os.Setenv("DRAIN_SAFETY_QUERIES", drainSafetyQueries)
		_ = os.Setenv("DRAIN_SAFETY_FAIL_CLOSED", strconv.FormatBool(drainSafetyFailClosed))
		_ = os.Setenv("DRAIN_PROGRESSIVE", strconv.FormatBool(drainProgressive))

		// pod 제거 정책 플래그 -> env 주입
		_ = os.Setenv("POD_EVICTION_MODE", podEvictionMode)
		_ = os.Setenv("POD_FORCE", strconv.FormatBool(podForce))
		_ = os.Setenv("POD_FORCE_PROBLEM_PODS", strconv.FormatBool(podForceProblemPods))
		_ = os.Setenv("POD_PDB_TOKEN", strconv.FormatBool(podPDBToken))
		_ = os.Setenv("POD_PDB_TOKEN_MAX_IN_FLIGHT", strconv.Itoa(podPDBTokenMaxInFlight))
		_ = os.Setenv("POD_MAX_CONCURRENT", strconv.Itoa(podMaxConcurrent))
		_ = os.Setenv("POD_MAX_RETRIES", strconv.Itoa(podMaxRetries))
		_ = os.Setenv("POD_RETRY_BACKOFF", podRetryBackoff)
		_ = os.Setenv("POD_DELETION_TIMEOUT", podDeletionTimeout)
		_ = os.Setenv("POD_CHECK_INTERVAL", podCheckInterval)

		ctx := command.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		kubeConfigMode := os.Getenv("KUBE_CONFIG")
		kubeConfigPath := os.Getenv("KUBECONFIG")

		clientSet, err := config.GetKubeClientSet(kubeConfigMode, kubeConfigPath)
		if err != nil {
			slog.Error("쿠버네티스 클라이언트 생성 실패", "error", err)
			return fmt.Errorf("쿠버네티스 클라이언트 생성 실패: %w", err)
		}

		return handleNodeDrain(ctx, clientSet)
	},
}

func handleNodeDrain(ctx context.Context, clientSet kubernetes.Interface) error {
	slog.Info("노드 드레인 커맨드를 실행합니다.")

	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 실패", "error", err)
		return fmt.Errorf("Prometheus 클라이언트 생성 실패: %w", err)
	}

	nodepool := os.Getenv("NODEPOOL_NAME")
	metricsQuerier := karpenter.NewPrometheusQuerier(prometheusClient)
	karpenterClient := karpenter.NewClient(nodepool, metricsQuerier)
	notifier := notification.NewSlackNotifier(notification.SlackConfig{
		WebhookURL:   os.Getenv("SLACK_WEBHOOK_URL"),
		ClusterName:  os.Getenv("CLUSTER_NAME"),
		NodepoolName: nodepool,
	})

	drainConfig := node.DefaultDrainConfig(nodepool)
	drainConfig.Eviction = pod.GetEvictionConfigFromEnv()

	results, err := node.NodeDrain(ctx, clientSet, node.DrainDependencies{
		AllocateRateProvider: karpenterClient,
		Notifier:             notifier,
	}, drainConfig)
	if err != nil {
		slog.Error("노드 드레인 실패", "error", err)
		if notifyErr := notifier.SendNodeDrainError(ctx, err); notifyErr != nil {
			slog.Error("슬랙 알림 전송 실패", "error", notifyErr)
		}
		return err
	}

	if err = notifier.SendNodeDrainComplete(ctx, results); err != nil {
		slog.Error("슬랙 알림 전송 실패", "error", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(drainCmd)

	drainCmd.Flags().StringVar(&drainPolicy, "drain-policy", "formula", "드레인 정책 (formula|step)")
	drainCmd.Flags().StringVar(&drainRounding, "drain-rounding", "floor", "드레인 계산 라운딩 (floor|round|ceil)")
	drainCmd.Flags().IntVar(&drainMin, "drain-min", 0, "드레인 최소 노드 수 (0이면 비활성)")
	drainCmd.Flags().IntVar(&drainMaxAbsolute, "drain-max-absolute", 0, "드레인 최대 노드 수(절대값, 0이면 비활성)")
	drainCmd.Flags().Float64Var(&drainMaxFraction, "drain-max-fraction", 0, "드레인 최대 비율(예: 0.2=최대 20%, 0이면 비활성)")
	drainCmd.Flags().StringVar(&drainStepRules, "drain-step-rules", "", "계단식 정책 규칙 (예: \"80:1,60:2\")")
	drainCmd.Flags().IntVar(&drainSafetyMaxAllocateRate, "drain-safety-max-allocate-rate", 0, "안전 조건: maxAllocateRate가 이 값 이상이면 0대로 강제 (0이면 비활성)")
	drainCmd.Flags().StringVar(&drainSafetyQueries, "drain-safety-queries", "", "안전 조건 PromQL(세미콜론/개행 구분). 하나라도 결과가 >0이면 0대로 강제")
	drainCmd.Flags().BoolVar(&drainSafetyFailClosed, "drain-safety-fail-closed", true, "안전 조건 쿼리 실패 시 0대로 강제할지 여부")
	drainCmd.Flags().BoolVar(&drainProgressive, "drain-progressive", true, "점진적 드레인: 노드 1대 처리 후 안전 조건 재평가")

	drainCmd.Flags().StringVar(&podEvictionMode, "pod-eviction-mode", "evict", "파드 제거 방식 (evict|delete)")
	drainCmd.Flags().BoolVar(&podForce, "force", false, "eviction 반복 실패/타임아웃 시 delete 강제 전환 여부")
	drainCmd.Flags().BoolVar(&podForceProblemPods, "force-problem-pods", true, "문제 파드를 즉시 delete(grace=0)로 처리할지 여부")
	drainCmd.Flags().BoolVar(&podPDBToken, "pdb-token", true, "같은 PDB에 매칭되는 파드 동시 처리 제한 여부")
	drainCmd.Flags().IntVar(&podPDBTokenMaxInFlight, "pdb-token-max-in-flight", 1, "같은 PDB 토큰 동시 처리 개수")
	drainCmd.Flags().IntVar(&podMaxConcurrent, "pod-max-concurrent", 30, "동시 제거 Pod 최대 개수")
	drainCmd.Flags().IntVar(&podMaxRetries, "pod-max-retries", 3, "Pod 제거 최대 재시도 횟수")
	drainCmd.Flags().StringVar(&podRetryBackoff, "pod-retry-backoff", "10s", "Pod 제거 재시도 간격")
	drainCmd.Flags().StringVar(&podDeletionTimeout, "pod-deletion-timeout", "2m", "Pod 삭제 대기 타임아웃")
	drainCmd.Flags().StringVar(&podCheckInterval, "pod-check-interval", "20s", "Pod 삭제 상태 확인 주기")
}
