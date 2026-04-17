package notification

import (
	"app/types"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Notifier defines notification operations used by drain flows.
type Notifier interface {
	SendNodeDrainComplete(ctx context.Context, results []types.NodeDrainResult) error
	SendNodeDrainError(ctx context.Context, err error) error
	SendNodeCount(ctx context.Context, nodeCount int) error
	SendKarpenterAllocateRate(ctx context.Context, memoryAllocateRate int, cpuAllocateRate int) error
}

// SlackConfig configures Slack notifier behavior.
type SlackConfig struct {
	WebhookURL   string
	ClusterName  string
	NodepoolName string
	HTTPTimeout  time.Duration
	MaxRetries   int
	RetryBackoff time.Duration
	HTTPClient   *http.Client
}

// SlackMessage is Slack webhook payload.
type SlackMessage struct {
	Text string `json:"text"`
}

// SlackNotifier sends notifications via Slack webhook.
type SlackNotifier struct {
	webhookURL   string
	clusterName  string
	nodepoolName string
	maxRetries   int
	retryBackoff time.Duration
	client       *http.Client
}

// NewSlackNotifier constructs a Slack notifier.
func NewSlackNotifier(cfg SlackConfig) *SlackNotifier {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	retryBackoff := cfg.RetryBackoff
	if retryBackoff <= 0 {
		retryBackoff = 300 * time.Millisecond
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &SlackNotifier{
		webhookURL:   cfg.WebhookURL,
		clusterName:  cfg.ClusterName,
		nodepoolName: cfg.NodepoolName,
		maxRetries:   maxRetries,
		retryBackoff: retryBackoff,
		client:       client,
	}
}

// NewEnvSlackNotifier creates a Slack notifier from environment variables.
func NewEnvSlackNotifier() *SlackNotifier {
	return NewSlackNotifier(SlackConfig{
		WebhookURL:   os.Getenv("SLACK_WEBHOOK_URL"),
		ClusterName:  os.Getenv("CLUSTER_NAME"),
		NodepoolName: os.Getenv("NODEPOOL_NAME"),
	})
}

// SendNodeDrainComplete sends completion summary.
func (s *SlackNotifier) SendNodeDrainComplete(ctx context.Context, results []types.NodeDrainResult) error {
	if s.webhookURL == "" {
		return nil
	}
	return s.sendSlackMessage(ctx, s.formatNodeDrainMessage(results))
}

// SendNodeDrainCompleteWithSummary sends completion summary with aggregate metrics.
func (s *SlackNotifier) SendNodeDrainCompleteWithSummary(ctx context.Context, results []types.NodeDrainResult, summary types.NodeDrainSummary) error {
	if s.webhookURL == "" {
		return nil
	}
	message := s.formatNodeDrainMessage(results)
	message += formatNodeDrainSummaryBlock(summary)
	return s.sendSlackMessage(ctx, message)
}

// SendNodeDrainError sends error summary.
func (s *SlackNotifier) SendNodeDrainError(ctx context.Context, err error) error {
	if s.webhookURL == "" {
		return nil
	}
	return s.sendSlackMessage(ctx, err.Error())
}

// SendNodeDrainErrorWithSummary sends error summary with aggregate metrics.
func (s *SlackNotifier) SendNodeDrainErrorWithSummary(ctx context.Context, err error, summary types.NodeDrainSummary) error {
	if s.webhookURL == "" {
		return nil
	}
	message := fmt.Sprintf("❌ 노드 드레인 작업 실패: %s\n\n", err.Error())
	message += formatNodeDrainSummaryBlock(summary)
	return s.sendSlackMessage(ctx, message)
}

// SendNodeCount sends node count message.
func (s *SlackNotifier) SendNodeCount(ctx context.Context, nodeCount int) error {
	if s.webhookURL == "" {
		return nil
	}

	message := fmt.Sprintf("ℹ️ %s 의 현재 Nodepool(%s) 노드 개수: %d개", s.clusterName, s.nodepoolName, nodeCount)
	return s.sendSlackMessage(ctx, message)
}

// SendKarpenterAllocateRate sends allocation-rate message.
func (s *SlackNotifier) SendKarpenterAllocateRate(ctx context.Context, memoryAllocateRate int, cpuAllocateRate int) error {
	if s.webhookURL == "" {
		return nil
	}

	message := fmt.Sprintf("🔄 %s Nodepool(%s) 의 현재 Karpenter Allocate Rate\n\n", s.clusterName, s.nodepoolName)
	message += fmt.Sprintf("• MemoryAllocateRate: %d%%\n", memoryAllocateRate)
	message += fmt.Sprintf("• CpuAllocateRate: %d%%\n", cpuAllocateRate)

	return s.sendSlackMessage(ctx, message)
}

func (s *SlackNotifier) formatNodeDrainMessage(results []types.NodeDrainResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("ℹ️ 드레인 대상 노드가 없습니다. (클러스터: %s, Nodepool: %s)", s.clusterName, s.nodepoolName)
	}

	message := fmt.Sprintf("🔄 노드 드레인 작업이 완료되었습니다 (클러스터: %s, Nodepool: %s)\n\n", s.clusterName, s.nodepoolName)
	for _, result := range results {
		status := "성공"
		if !result.Success {
			status = "실패"
		}
		message += fmt.Sprintf(
			"• 노드: %s\n  인스턴스 타입: %s\n  노드풀: %s\n  노드 생성일: %s\n  시작 시간: %s\n  소요 시간(초): %d\n  상태: %s\n",
			result.NodeName,
			result.InstanceType,
			result.NodepoolName,
			result.Age,
			result.StartedAt,
			result.DurationSeconds,
			status,
		)
		if result.FailureReason != "" {
			message += fmt.Sprintf("  실패 사유: %s\n", result.FailureReason)
		}
	}

	return message
}

func formatNodeDrainSummaryBlock(summary types.NodeDrainSummary) string {
	message := "\n\n📊 드레인 요약\n"
	message += fmt.Sprintf("• TargetNodepool: %s\n", summary.TargetNodepool)
	message += fmt.Sprintf("• TotalNodesInNodepool: %d\n", summary.TotalNodesInNodepool)
	message += fmt.Sprintf("• PlannedDrainNodeCount: %d\n", summary.PlannedDrainNodeCount)
	message += fmt.Sprintf("• DrainedNodeCount: %d\n", summary.DrainedNodeCount)
	message += fmt.Sprintf("• TotalPods: %d\n", summary.TotalPods)
	message += fmt.Sprintf("• EvictedPods: %d\n", summary.EvictedPods)
	message += fmt.Sprintf("• DeletedPods: %d\n", summary.DeletedPods)
	message += fmt.Sprintf("• ForceDeletedPods: %d\n", summary.ForceDeletedPods)
	message += fmt.Sprintf("• PDBBlockedPods: %d\n", summary.PDBBlockedPods)
	message += fmt.Sprintf("• ForcedByFallback: %d\n", summary.ForcedByFallback)
	message += fmt.Sprintf("• ProblemPodsForced: %d\n", summary.ProblemPodsForced)
	if summary.StoppedBySafety {
		message += fmt.Sprintf("• StoppedBySafety: true (%s)\n", summary.StopSafetyReason)
	}
	if len(summary.TopErrorReasons) > 0 {
		message += fmt.Sprintf("• TopErrorReasons: %s\n", strings.Join(summary.TopErrorReasons, ", "))
	}
	return message
}

func (s *SlackNotifier) sendSlackMessage(ctx context.Context, message string) error {
	payload := SlackMessage{Text: message}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewBuffer(jsonPayload))
		if reqErr != nil {
			return fmt.Errorf("build slack request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := s.client.Do(req)
		if doErr != nil {
			lastErr = doErr
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				slog.Info("Slack 알림 전송 완료")
				return nil
			}

			lastErr = fmt.Errorf("slack notification failed with status code: %d body: %s", resp.StatusCode, string(body))
			if resp.StatusCode < 500 {
				return lastErr
			}
		}

		if attempt < s.maxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.retryBackoff):
			}
		}
	}

	return lastErr
}

// SendNodeDrainComplete sends completion summary using environment based notifier.
func SendNodeDrainComplete(results []types.NodeDrainResult) error {
	return NewEnvSlackNotifier().SendNodeDrainComplete(context.Background(), results)
}

// SendNodeDrainCompleteWithSummary sends completion summary with aggregate metrics.
func SendNodeDrainCompleteWithSummary(results []types.NodeDrainResult, summary types.NodeDrainSummary) error {
	return NewEnvSlackNotifier().SendNodeDrainCompleteWithSummary(context.Background(), results, summary)
}

// SendNodeDrainError sends error summary using environment based notifier.
func SendNodeDrainError(err error) error {
	return NewEnvSlackNotifier().SendNodeDrainError(context.Background(), err)
}

// SendNodeDrainErrorWithSummary sends error summary with aggregate metrics.
func SendNodeDrainErrorWithSummary(err error, summary types.NodeDrainSummary) error {
	return NewEnvSlackNotifier().SendNodeDrainErrorWithSummary(context.Background(), err, summary)
}

// SendNodeCount sends node count using environment based notifier.
func SendNodeCount(nodeCount int) error {
	return NewEnvSlackNotifier().SendNodeCount(context.Background(), nodeCount)
}

// SendKarpenterAllocateRate sends allocation rates using environment based notifier.
func SendKarpenterAllocateRate(memoryAllocateRate int, cpuAllocateRate int) error {
	return NewEnvSlackNotifier().SendKarpenterAllocateRate(context.Background(), memoryAllocateRate, cpuAllocateRate)
}
