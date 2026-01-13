package cmd

import (
	"app/config"
	"app/pkg/node"
	"app/pkg/notification"
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
	Run: func(cmd *cobra.Command, args []string) {
		// drain 정책 플래그 → 환경변수 주입 (pkg/node가 env 기반으로 읽음)
		// 기본값을 "기존 동작"과 동일하게 둬서, 사용자가 플래그를 지정했을 때만 행동이 바뀌게 합니다.
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

		// Pod 제거 정책 플래그 → 환경변수 주입 (pkg/pod가 env 기반으로 읽음)
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

		kubeConfig := os.Getenv("KUBE_CONFIG")
		clientSet, err := config.GetKubeClientSet(kubeConfig)
		if err != nil {
			slog.Error("쿠버네티스 클라이언트를 가져오는 중 오류가 발생했습니다.", "error", err)
			return
		}

		handleNodeDrain(clientSet)
	},
}

func handleNodeDrain(clientSet kubernetes.Interface) {
	slog.Info("노드 드레인 커맨드를 실행합니다.")

	results, summary, err := node.NodeDrainWithSummary(clientSet)
	if err != nil {
		slog.Error("노드를 드레인 하는 중 오류가 발생했습니다.", "error", err)
		err = notification.SendNodeDrainErrorWithSummary(err, summary)
		if err != nil {
			slog.Error("슬랙 알람을 보내는 중 오류가 발생했습니다.", "error", err)
		}
		return
	}

	err = notification.SendNodeDrainCompleteWithSummary(results, summary)
	if err != nil {
		slog.Error("슬랙 알람을 보내는 중 오류가 발생했습니다.", "error", err)
	}
}

func init() {
	rootCmd.AddCommand(drainCmd)

	// drain 정책 관련 플래그 (drain 커맨드 전용)
	drainCmd.Flags().StringVar(&drainPolicy, "drain-policy", "formula", "드레인 정책 (formula|step)")
	drainCmd.Flags().StringVar(&drainRounding, "drain-rounding", "floor", "드레인 계산 라운딩 (floor|round|ceil)")
	drainCmd.Flags().IntVar(&drainMin, "drain-min", 0, "드레인 최소 노드 수 (0이면 비활성)")
	drainCmd.Flags().IntVar(&drainMaxAbsolute, "drain-max-absolute", 0, "드레인 최대 노드 수(절대값, 0이면 비활성)")
	drainCmd.Flags().Float64Var(&drainMaxFraction, "drain-max-fraction", 0, "드레인 최대 비율(예: 0.2=최대 20%, 0이면 비활성)")
	drainCmd.Flags().StringVar(&drainStepRules, "drain-step-rules", "", "계단식 정책 규칙 (예: \"80:1,60:2\" => max<=60이면 2대, max<=80이면 1대, 그 외 0대)")
	drainCmd.Flags().IntVar(&drainSafetyMaxAllocateRate, "drain-safety-max-allocate-rate", 0, "안전 조건: maxAllocateRate가 이 값 이상이면 0대로 강제 (0이면 비활성)")
	drainCmd.Flags().StringVar(&drainSafetyQueries, "drain-safety-queries", "", "안전 조건 PromQL(세미콜론/개행 구분). 하나라도 결과가 >0이면 0대로 강제")
	drainCmd.Flags().BoolVar(&drainSafetyFailClosed, "drain-safety-fail-closed", true, "안전 조건 쿼리 실패 시 0대로 강제할지 여부")
	drainCmd.Flags().BoolVar(&drainProgressive, "drain-progressive", true, "점진적 드레인: 노드 1대 처리 후 안전 조건 재평가로 다음 노드 진행 여부를 결정")

	// Pod 제거 정책 플래그 (drain 커맨드 전용)
	drainCmd.Flags().StringVar(&podEvictionMode, "pod-eviction-mode", "evict", "파드 제거 방식 (evict|delete). 기본은 eviction(subresource) 사용")
	drainCmd.Flags().BoolVar(&podForce, "force", false, "eviction이 반복 실패/타임아웃일 때 delete로 강제 전환할지 여부")
	drainCmd.Flags().BoolVar(&podForceProblemPods, "force-problem-pods", true, "문제 파드는 즉시 delete(grace=0)로 처리할지 여부")
	drainCmd.Flags().BoolVar(&podPDBToken, "pdb-token", true, "같은 PDB에 매칭되는 파드는 동시에 제한(토큰)할지 여부")
	drainCmd.Flags().IntVar(&podPDBTokenMaxInFlight, "pdb-token-max-in-flight", 1, "같은 PDB 토큰 동시 처리 개수(기본 1)")
	drainCmd.Flags().IntVar(&podMaxConcurrent, "pod-max-concurrent", 30, "동시에 제거할 Pod 최대 개수")
	drainCmd.Flags().IntVar(&podMaxRetries, "pod-max-retries", 3, "Pod 제거 최대 재시도 횟수")
	drainCmd.Flags().StringVar(&podRetryBackoff, "pod-retry-backoff", "10s", "Pod 제거 재시도 간격 (예: 10s)")
	drainCmd.Flags().StringVar(&podDeletionTimeout, "pod-deletion-timeout", "2m", "Pod 삭제 대기 타임아웃 (예: 2m)")
	drainCmd.Flags().StringVar(&podCheckInterval, "pod-check-interval", "20s", "Pod 삭제 상태 확인 주기 (예: 20s)")
}
