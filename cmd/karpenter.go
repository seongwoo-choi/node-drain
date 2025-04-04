package cmd

import (
	"app/config"
	"app/pkg/karpenter"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

var (
	karpenterCmd = &cobra.Command{
		Use:   "karpenter",
		Short: "Karpenter 노드 사용량 조회",
	}

	allocateRateCmd = &cobra.Command{
		Use:   "allocate-rate",
		Short: "Allocate Rate 사용량 조회",
		Run: func(cmd *cobra.Command, args []string) {
			kubeConfig := os.Getenv("KUBE_CONFIG")
			clientSet, err := config.GetKubeClientSet(kubeConfig)
			if err != nil {
				slog.Error("쿠버네티스 클라이언트를 가져오는 중 오류가 발생했습니다.", err)
				return
			}
			handleKarpenterAllocateRate(clientSet)
		},
	}
)

func handleKarpenterAllocateRate(clientSet kubernetes.Interface) {
	slog.Info("Karpenter Allocate Rate 사용량 조회 커맨드를 실행합니다.")
	memoryAllocateRate, err := karpenter.GetAllocateRate(clientSet, "memory")
	if err != nil {
		slog.Error("Karpenter Memory Allocate Rate 사용량 조회 중 오류가 발생했습니다.: ", err)
		return
	}

	cpuAllocateRate, err := karpenter.GetAllocateRate(clientSet, "cpu")
	if err != nil {
		slog.Error("Karpenter Cpu Allocate Rate 사용량 조회 중 오류가 발생했습니다.: ", err)
		return
	}
	slog.Info("Karpenter", "memoryAllocateRate", fmt.Sprintf("%d %%", memoryAllocateRate))
	slog.Info("Karpenter", "cpuAllocateRate", fmt.Sprintf("%d %%", cpuAllocateRate))
}

func init() {
	rootCmd.AddCommand(karpenterCmd)
	karpenterCmd.AddCommand(allocateRateCmd)
}
