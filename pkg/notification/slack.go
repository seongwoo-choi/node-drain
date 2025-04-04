package notification

import (
	"app/types"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

type SlackMessage struct {
	Text string `json:"text"`
}

func SendNodeDrainComplete(results []types.NodeDrainResult) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}

	message := formatNodeDrainMessage(results)
	return sendSlackMessage(webhookURL, message)
}

func SendNodeDrainError(err error) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}
	return sendSlackMessage(webhookURL, err.Error())
}

func SendNodeCount(nodeCount int) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}

	message := fmt.Sprintf("ℹ️ %s 의 현재 Nodepool(%s) 노드 개수: %d개",
		os.Getenv("CLUSTER_NAME"),
		os.Getenv("NODEPOOL_NAME"),
		nodeCount)

	return sendSlackMessage(webhookURL, message)
}

func SendKarpenterAllocateRate(memoryAllocateRate int, cpuAllocateRate int) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}

	message := fmt.Sprintf("🔄 %s Nodepool(%s) 의 현재 Karpenter Allocate Rate\n\n", os.Getenv("CLUSTER_NAME"), os.Getenv("NODEPOOL_NAME"))
	message += fmt.Sprintf("• MemoryAllocateRate: %d%%\n", memoryAllocateRate)
	message += fmt.Sprintf("• CpuAllocateRate: %d%%\n", cpuAllocateRate)

	return sendSlackMessage(webhookURL, message)
}

func formatNodeDrainMessage(results []types.NodeDrainResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("ℹ️ 드레인 대상 노드가 없습니다. (클러스터: %s, Nodepool: %s)",
			os.Getenv("CLUSTER_NAME"),
			os.Getenv("NODEPOOL_NAME"),
		)
	}

	var message string
	message = fmt.Sprintf("🔄 노드 드레인 작업이 완료되었습니다 (클러스터: %s, Nodepool: %s)\n\n",
		os.Getenv("CLUSTER_NAME"),
		os.Getenv("NODEPOOL_NAME"),
	)

	for _, result := range results {
		message += fmt.Sprintf("• 노드: %s\n  인스턴스 타입: %s\n  노드풀: %s\n  노드가 생성된 날짜: %s\n",
			result.NodeName,
			result.InstanceType,
			result.NodepoolName,
			result.Age,
		)
	}

	return message
}

func sendSlackMessage(webhookURL string, message string) error {
	payload := SlackMessage{Text: message}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack notification failed with status code: %d", resp.StatusCode)
	}

	slog.Info("Slack 알림 전송 완료")
	return nil
}
