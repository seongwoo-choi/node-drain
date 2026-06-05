package cmd

import (
	"app/config"
	"app/pkg/karpenter"
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	karpenterCmd = &cobra.Command{
		Use:   "karpenter",
		Short: "Karpenter 노드 사용량 조회",
	}

	allocateRateCmd = &cobra.Command{
		Use:   "allocate-rate",
		Short: "Allocate Rate 사용량 조회",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			applyRootFlagEnv(command)

			if err := validateRequiredEnvValues(
				requiredEnvValue{flagName: "prometheus-address", envKey: "PROMETHEUS_ADDRESS"},
				requiredEnvValue{flagName: "nodepool-name", envKey: "NODEPOOL_NAME"},
			); err != nil {
				return err
			}
			if err := config.ValidatePrometheusAddress(os.Getenv("PROMETHEUS_ADDRESS")); err != nil {
				return err
			}

			ctx := command.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			return handleKarpenterAllocateRate(ctx)
		},
	}
)

func handleKarpenterAllocateRate(ctx context.Context) error {
	slog.Info("Karpenter Allocate Rate 사용량 조회 커맨드를 실행합니다.")

	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 실패", "error", err)
		return fmt.Errorf("Prometheus 클라이언트 생성 실패: %w", err)
	}

	metricsQuerier := karpenter.NewPrometheusQuerier(prometheusClient)
	karpenterClient := newKarpenterClientFromEnv(metricsQuerier)

	memoryAllocateRate, err := karpenterClient.GetAllocateRate(ctx, "memory")
	if err != nil {
		slog.Error("Karpenter memory allocate rate 조회 실패", "error", err)
		return fmt.Errorf("Karpenter memory allocate rate 조회 실패: %w", err)
	}

	cpuAllocateRate, err := karpenterClient.GetAllocateRate(ctx, "cpu")
	if err != nil {
		slog.Error("Karpenter cpu allocate rate 조회 실패", "error", err)
		return fmt.Errorf("Karpenter cpu allocate rate 조회 실패: %w", err)
	}

	slog.Info("Karpenter", "memoryAllocateRate", fmt.Sprintf("%d %%", memoryAllocateRate))
	slog.Info("Karpenter", "cpuAllocateRate", fmt.Sprintf("%d %%", cpuAllocateRate))
	return nil
}

func newKarpenterClientFromEnv(querier karpenter.MetricsQuerier) *karpenter.Client {
	return karpenter.NewClientForCluster(
		os.Getenv("NODEPOOL_NAME"),
		os.Getenv("CLUSTER_NAME"),
		querier,
	)
}

func init() {
	rootCmd.AddCommand(karpenterCmd)
	karpenterCmd.AddCommand(allocateRateCmd)
}
