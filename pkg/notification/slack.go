package notification

import (
	"app/types"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
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

func SendNodeDrainCompleteWithSummary(results []types.NodeDrainResult, summary types.NodeDrainSummary) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}

	message := formatNodeDrainMessageWithSummary(results, summary)
	return sendSlackMessage(webhookURL, message)
}

func SendNodeDrainError(err error) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}
	return sendSlackMessage(webhookURL, err.Error())
}

func SendNodeDrainErrorWithSummary(err error, summary types.NodeDrainSummary) error {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		return fmt.Errorf("SLACK_WEBHOOK_URL is not set")
	}
	message := fmt.Sprintf("❌ 노드 드레인 작업 중 오류가 발생했습니다 (클러스터: %s, Nodepool: %s)\n\n",
		os.Getenv("CLUSTER_NAME"),
		os.Getenv("NODEPOOL_NAME"),
	)
	message += fmt.Sprintf("에러: %s\n\n", err.Error())
	message += formatNodeDrainSummaryBlock(summary)
	return sendSlackMessage(webhookURL, message)
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

func formatNodeDrainMessageWithSummary(results []types.NodeDrainResult, summary types.NodeDrainSummary) string {
	if len(results) == 0 {
		message := fmt.Sprintf("ℹ️ 드레인 대상 노드가 없습니다. (클러스터: %s, Nodepool: %s)\n\n",
			os.Getenv("CLUSTER_NAME"),
			os.Getenv("NODEPOOL_NAME"),
		)
		message += formatNodeDrainSummaryBlock(summary)
		return message
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

	message += "\n"
	message += formatNodeDrainSummaryBlock(summary)
	return message
}

func formatNodeDrainSummaryBlock(summary types.NodeDrainSummary) string {
	msg := "📊 드레인 요약\n\n"
	msg += fmt.Sprintf("• NodePool: %s\n", summary.TargetNodepool)
	msg += fmt.Sprintf("• NodePool 총 노드 수: %d\n", summary.TotalNodesInNodepool)
	msg += fmt.Sprintf("• 계획된 드레인 노드 수: %d\n", summary.PlannedDrainNodeCount)
	msg += fmt.Sprintf("• 실제 드레인 완료 노드 수: %d\n", summary.DrainedNodeCount)

	msg += "\n"
	msg += fmt.Sprintf("• 대상 파드 수: %d\n", summary.TotalPods)
	msg += fmt.Sprintf("• Eviction 성공: %d\n", summary.EvictedPods)
	msg += fmt.Sprintf("• Delete 수행: %d (강제 삭제: %d)\n", summary.DeletedPods, summary.ForceDeletedPods)
	msg += fmt.Sprintf("• PDB 차단 감지: %d\n", summary.PDBBlockedPods)
	msg += fmt.Sprintf("• Eviction 실패 후 강제 전환: %d\n", summary.ForcedByFallback)
	msg += fmt.Sprintf("• 문제 파드 즉시 강제 삭제: %d\n", summary.ProblemPodsForced)

	if summary.StoppedBySafety {
		msg += "\n"
		msg += fmt.Sprintf("• 안전 조건으로 추가 드레인 중단: true (%s)\n", summary.StopSafetyReason)
	}

	if len(summary.TopErrorReasons) > 0 {
		msg += "\n"
		msg += fmt.Sprintf("• 주요 실패 이유(Top): %s\n", strings.Join(summary.TopErrorReasons, ", "))
	}

	return msg
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
