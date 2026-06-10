package cmd

import (
	"app/config"
	"app/pkg/karpenter"
	"app/pkg/node"
	"app/pkg/pod"
	"app/types"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

var analyzeOutputFormat string

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "노드 드레인 사전 진단 실행",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		applyRootFlagEnv(command)
		applyAnalyzeFlagEnv(command)
		if err := applyAnalyzeRuntimeEnv(); err != nil {
			return err
		}

		if err := validateRequiredEnvValues(
			requiredEnvValue{flagName: "prometheus-address", envKey: "PROMETHEUS_ADDRESS"},
			requiredEnvValue{flagName: "cluster-name", envKey: "CLUSTER_NAME"},
			requiredEnvValue{flagName: "nodepool-name", envKey: "NODEPOOL_NAME"},
		); err != nil {
			return err
		}
		if err := config.ValidatePrometheusAddress(os.Getenv("PROMETHEUS_ADDRESS")); err != nil {
			return err
		}
		if err := validateAnalyzeCommandConfig(); err != nil {
			return err
		}

		ctx := command.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		clientSet, err := config.GetKubeClientSet(os.Getenv("KUBE_CONFIG"), os.Getenv("KUBECONFIG"))
		if err != nil {
			slog.Error("쿠버네티스 클라이언트 생성 실패", "error", err)
			return fmt.Errorf("쿠버네티스 클라이언트 생성 실패: %w", err)
		}

		return handleNodeDrainAnalysis(ctx, clientSet, os.Stdout)
	},
}

func handleNodeDrainAnalysis(ctx context.Context, clientSet kubernetes.Interface, w io.Writer) error {
	slog.Info("노드 드레인 사전 진단 커맨드를 실행합니다.")

	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 실패", "error", err)
		return fmt.Errorf("Prometheus 클라이언트 생성 실패: %w", err)
	}

	metricsQuerier := karpenter.NewPrometheusQuerier(prometheusClient)
	karpenterClient := newKarpenterClientFromEnv(metricsQuerier)
	return runNodeDrainAnalysis(ctx, clientSet, node.DrainDependencies{
		AllocateRateProvider: karpenterClient,
		Notifier:             nil,
	}, buildNodeDrainAnalysisConfig(), w, analyzeOutputFormat)
}

func runNodeDrainAnalysis(ctx context.Context, clientSet kubernetes.Interface, deps node.DrainDependencies, cfg node.DrainConfig, w io.Writer, outputFormat string) error {
	report, err := node.NodeDrainWithReport(ctx, clientSet, deps, cfg)
	if outputErr := writeNodeDrainAnalysisReport(w, report, outputFormat); outputErr != nil {
		return fmt.Errorf("드레인 사전 진단 결과 출력 실패: %w", outputErr)
	}
	if err != nil {
		return fmt.Errorf("드레인 사전 진단 실패: %w", err)
	}
	return nil
}

func buildNodeDrainAnalysisConfig() node.DrainConfig {
	nodepool := strings.TrimSpace(os.Getenv("NODEPOOL_NAME"))
	drainConfig := node.DefaultDrainConfig(nodepool)
	drainConfig.Eviction = pod.GetEvictionConfigFromEnv()
	drainConfig.DryRun = true
	drainConfig.NodeSelectionStrategy = node.DrainNodeSelectionStrategy(drainNodeSelectionStrategy)
	drainConfig.SkipUnschedulable = drainSkipUnschedulable
	return drainConfig
}

func validateAnalyzeCommandConfig() error {
	if err := node.ValidateDrainPolicyEnv(); err != nil {
		return err
	}
	if _, err := parseDrainOutputFormat(analyzeOutputFormat); err != nil {
		return err
	}
	if err := validateDrainNodeSelectionStrategy(drainNodeSelectionStrategy); err != nil {
		return err
	}
	return nil
}

func applyAnalyzeFlagEnv(command *cobra.Command) {
	setEnvFromChangedFlag(command, "drain-policy", "DRAIN_POLICY", drainPolicy)
	setEnvFromChangedFlag(command, "drain-rounding", "DRAIN_ROUNDING", drainRounding)
	setEnvFromChangedFlag(command, "drain-min", "DRAIN_MIN", fmt.Sprintf("%d", drainMin))
	setEnvFromChangedFlag(command, "drain-max-absolute", "DRAIN_MAX_ABSOLUTE", fmt.Sprintf("%d", drainMaxAbsolute))
	setEnvFromChangedFlag(command, "drain-max-fraction", "DRAIN_MAX_FRACTION", fmt.Sprintf("%g", drainMaxFraction))
	setEnvFromChangedFlag(command, "drain-step-rules", "DRAIN_STEP_RULES", drainStepRules)
	setEnvFromChangedFlag(command, "drain-safety-max-allocate-rate", "DRAIN_SAFETY_MAX_ALLOCATE_RATE", fmt.Sprintf("%d", drainSafetyMaxAllocateRate))
	setEnvFromChangedFlag(command, "drain-safety-queries", "DRAIN_SAFETY_QUERIES", drainSafetyQueries)
	setEnvFromChangedFlag(command, "drain-safety-fail-closed", "DRAIN_SAFETY_FAIL_CLOSED", fmt.Sprintf("%t", drainSafetyFailClosed))
	setEnvFromChangedFlag(command, "drain-node-selection", "DRAIN_NODE_SELECTION", drainNodeSelectionStrategy)
	setEnvFromChangedFlag(command, "drain-skip-unschedulable", "DRAIN_SKIP_UNSCHEDULABLE", fmt.Sprintf("%t", drainSkipUnschedulable))
	setEnvFromChangedFlag(command, "output", "DRAIN_OUTPUT_FORMAT", analyzeOutputFormat)
}

func applyAnalyzeRuntimeEnv() error {
	if err := setBoolFromEnv("DRAIN_SKIP_UNSCHEDULABLE", &drainSkipUnschedulable); err != nil {
		return err
	}
	setStringFromEnv("DRAIN_NODE_SELECTION", &drainNodeSelectionStrategy)
	setStringFromEnv("DRAIN_OUTPUT_FORMAT", &analyzeOutputFormat)
	return nil
}

func writeNodeDrainAnalysisReport(w io.Writer, report types.NodeDrainReport, outputFormat string) error {
	format, err := parseDrainOutputFormat(outputFormat)
	if err != nil {
		return err
	}
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	_, err = fmt.Fprintf(
		w,
		"NodePool %s: total=%d planned=%d selected=%d plannedPods=%d dryRun=%t\n",
		report.Summary.TargetNodepool,
		report.Summary.TotalNodesInNodepool,
		report.Summary.PlannedDrainNodeCount,
		report.Summary.SelectedDrainNodeCount,
		report.Summary.PlannedPodCount,
		report.Summary.DryRun,
	)
	return err
}

func init() {
	rootCmd.AddCommand(analyzeCmd)

	analyzeCmd.Flags().StringVar(&drainPolicy, "drain-policy", "formula", "드레인 정책 (formula|step)")
	analyzeCmd.Flags().StringVar(&drainRounding, "drain-rounding", "floor", "드레인 계산 라운딩 (floor|round|ceil)")
	analyzeCmd.Flags().IntVar(&drainMin, "drain-min", 0, "드레인 최소 노드 수 (0이면 비활성)")
	analyzeCmd.Flags().IntVar(&drainMaxAbsolute, "drain-max-absolute", 0, "드레인 최대 노드 수(절대값, 0이면 비활성)")
	analyzeCmd.Flags().Float64Var(&drainMaxFraction, "drain-max-fraction", 0, "드레인 최대 비율(예: 0.2=최대 20%, 0이면 비활성)")
	analyzeCmd.Flags().StringVar(&drainStepRules, "drain-step-rules", "", "계단식 정책 규칙 (예: \"80:1,60:2\")")
	analyzeCmd.Flags().IntVar(&drainSafetyMaxAllocateRate, "drain-safety-max-allocate-rate", 0, "안전 조건: maxAllocateRate가 이 값 이상이면 0대로 강제 (0이면 비활성)")
	analyzeCmd.Flags().StringVar(&drainSafetyQueries, "drain-safety-queries", "", "안전 조건 PromQL(세미콜론/개행 구분). 하나라도 결과가 >0이면 0대로 강제")
	analyzeCmd.Flags().BoolVar(&drainSafetyFailClosed, "drain-safety-fail-closed", true, "안전 조건 쿼리 실패 시 0대로 강제할지 여부")
	analyzeCmd.Flags().StringVar(&drainNodeSelectionStrategy, "drain-node-selection", string(node.DrainNodeSelectionOldest), "드레인 대상 노드 선택 전략 (oldest|empty-first|least-pods|most-pods)")
	analyzeCmd.Flags().BoolVar(&drainSkipUnschedulable, "drain-skip-unschedulable", false, "이미 cordon된 노드를 새 드레인 대상으로 선택하지 않음")
	analyzeCmd.Flags().StringVar(&analyzeOutputFormat, "output", "json", "결과 출력 형식 (text|json)")
}
