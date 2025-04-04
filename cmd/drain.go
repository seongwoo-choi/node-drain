package cmd

import (
	"app/config"
	"app/pkg/node"
	"app/pkg/notification"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

var drainCmd = &cobra.Command{
	Use:   "drain",
	Short: "노드 드레인 실행",
	Run: func(cmd *cobra.Command, args []string) {
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
}
