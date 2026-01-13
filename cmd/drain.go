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

		kubeConfig := os.Getenv("KUBE_CONFIG")
		clientSet, err := config.GetKubeClientSet(kubeConfig)
		if err != nil {
			slog.Error("쿠버네티스 클라이언트를 가져오는 중 오류가 발생했습니다.", err)
			return
		}

		handleNodeDrain(clientSet)
	},
}

func handleNodeDrain(clientSet kubernetes.Interface) {
	slog.Info("노드 드레인 커맨드를 실행합니다.")

	results, err := node.NodeDrain(clientSet)
	if err != nil {
		slog.Error("노드를 드레인 하는 중 오류가 발생했습니다.: ", err)
		err = notification.SendNodeDrainError(err)
		if err != nil {
			slog.Error("슬랙 알람을 보내는 중 오류가 발생했습니다.: ", err)
		}
		return
	}

	err = notification.SendNodeDrainComplete(results)
	if err != nil {
		slog.Error("슬랙 알람을 보내는 중 오류가 발생했습니다.: ", err)
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
}
