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
		Run: func(command *cobra.Command, args []string) {
			ctx := command.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			handleKarpenterAllocateRate(ctx)
		},
	}
)

func handleKarpenterAllocateRate(ctx context.Context) {
	slog.Info("Karpenter Allocate Rate 사용량 조회 커맨드를 실행합니다.")

	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 실패", "error", err)
		return
	}

	nodepool := os.Getenv("NODEPOOL_NAME")
	metricsQuerier := karpenter.NewPrometheusQuerier(prometheusClient)
	karpenterClient := karpenter.NewClient(nodepool, metricsQuerier)

	memoryAllocateRate, err := karpenterClient.GetAllocateRate(ctx, "memory")
	if err != nil {
		slog.Error("Karpenter memory allocate rate 조회 실패", "error", err)
		return
	}

	cpuAllocateRate, err := karpenterClient.GetAllocateRate(ctx, "cpu")
	if err != nil {
		slog.Error("Karpenter cpu allocate rate 조회 실패", "error", err)
		return
	}

	slog.Info("Karpenter", "memoryAllocateRate", fmt.Sprintf("%d %%", memoryAllocateRate))
	slog.Info("Karpenter", "cpuAllocateRate", fmt.Sprintf("%d %%", cpuAllocateRate))
}

func init() {
	rootCmd.AddCommand(karpenterCmd)
	karpenterCmd.AddCommand(allocateRateCmd)
}
