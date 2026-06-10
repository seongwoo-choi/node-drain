package notification

import (
	"app/types"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSendNodeDrainComplete(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatalf("unexpected method: %s", req.Method)
				}
				if got := req.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("unexpected content type: %s", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	err := notifier.SendNodeDrainComplete(context.Background(), []types.NodeDrainResult{
		{
			NodeName:        "node-1",
			InstanceType:    "t3.medium",
			NodepoolName:    "test-pool",
			Age:             "2024-01-01T00:00:00Z",
			StartedAt:       "2024-01-01T00:01:00Z",
			DurationSeconds: 12,
			Success:         true,
		},
	})
	if err != nil {
		t.Fatalf("SendNodeDrainComplete failed: %v", err)
	}
}

func TestSlackNoWebhookIsNoop(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
	})

	if err := notifier.SendNodeCount(context.Background(), 3); err != nil {
		t.Fatalf("SendNodeCount should no-op without webhook: %v", err)
	}
	if err := notifier.SendNodeDrainError(context.Background(), context.Canceled); err != nil {
		t.Fatalf("SendNodeDrainError should no-op without webhook: %v", err)
	}
}

func TestSlackNilNotifierIsNoop(t *testing.T) {
	var notifier *SlackNotifier

	if err := notifier.SendNodeCount(context.Background(), 3); err != nil {
		t.Fatalf("SendNodeCount should no-op for nil notifier: %v", err)
	}
	if err := notifier.SendNodeDrainError(context.Background(), context.Canceled); err != nil {
		t.Fatalf("SendNodeDrainError should no-op for nil notifier: %v", err)
	}
}

func TestSlackWhitespaceWebhookIsNoop(t *testing.T) {
	attempts := 0
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "   ",
		ClusterName:  " test-cluster ",
		NodepoolName: " test-pool ",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	if err := notifier.SendNodeCount(context.Background(), 3); err != nil {
		t.Fatalf("SendNodeCount should no-op with whitespace webhook: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("whitespace webhook should not send request, attempts=%d", attempts)
	}
	if notifier.clusterName != "test-cluster" {
		t.Fatalf("clusterName not trimmed: %q", notifier.clusterName)
	}
	if notifier.nodepoolName != "test-pool" {
		t.Fatalf("nodepoolName not trimmed: %q", notifier.nodepoolName)
	}
}

func TestSlackNilErrorUsesFallbackMessage(t *testing.T) {
	var body string
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				payload, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read request body failed: %v", err)
				}
				body = string(payload)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	if err := notifier.SendNodeDrainError(context.Background(), nil); err != nil {
		t.Fatalf("SendNodeDrainError with nil error failed: %v", err)
	}
	if !strings.Contains(body, "unknown error") {
		t.Fatalf("fallback error message missing from payload: %s", body)
	}
}

func TestSlackNilContextUsesBackground(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Context() == nil {
					t.Fatal("request context is nil")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("ok")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	if err := notifier.SendNodeCount(nil, 3); err != nil {
		t.Fatalf("SendNodeCount with nil context failed: %v", err)
	}
}

func TestFormatNodeDrainDryRunMessage(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
	})

	message := notifier.formatNodeDrainMessage([]types.NodeDrainResult{
		{
			NodeName:     "node-1",
			InstanceType: "t3.medium",
			NodepoolName: "test-pool",
			DryRun:       true,
			Success:      true,
			PlannedPods: []types.NodeDrainPodPlan{
				{Namespace: "default", Name: "pod-1"},
			},
		},
	})

	if !strings.Contains(message, "dry-run 계획 생성 완료") {
		t.Fatalf("dry-run message header missing: %s", message)
	}
	if !strings.Contains(message, "상태: 계획") {
		t.Fatalf("dry-run status missing: %s", message)
	}
	if !strings.Contains(message, "제거 예정 Pod: 1개") {
		t.Fatalf("planned pod count missing: %s", message)
	}
}

func TestFormatNodeDrainDryRunFailureMessage(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
	})

	message := notifier.formatNodeDrainMessage([]types.NodeDrainResult{
		{
			NodeName:      "node-1",
			InstanceType:  "t3.medium",
			NodepoolName:  "test-pool",
			DryRun:        true,
			Success:       false,
			FailureReason: "pod list failed",
		},
	})

	if strings.Contains(message, "dry-run 계획 생성 완료") {
		t.Fatalf("dry-run failure should not use success header: %s", message)
	}
	if !strings.Contains(message, "dry-run 계획 생성 실패/일부 실패") {
		t.Fatalf("dry-run failure header missing: %s", message)
	}
	if !strings.Contains(message, "상태: 실패") {
		t.Fatalf("dry-run failure status missing: %s", message)
	}
	if !strings.Contains(message, "실패 사유: pod list failed") {
		t.Fatalf("dry-run failure reason missing: %s", message)
	}
}

func TestFormatNodeDrainSummaryBlockIncludesOutcomeSignals(t *testing.T) {
	message := formatNodeDrainSummaryBlock(types.NodeDrainSummary{
		TargetNodepool:         "test-pool",
		TotalNodesInNodepool:   3,
		PlannedDrainNodeCount:  2,
		SelectedDrainNodeCount: 2,
		CordonedNodeCount:      1,
		DrainedNodeCount:       1,
		SuccessfulNodeCount:    1,
		FailedNodeCount:        0,
		PDBBlockedPods:         1,
		ProblemPodCount:        2,
		UnmanagedPodCount:      3,
		PodsWithFinalizers:     4,
		StoppedBySafety:        true,
		StopSafetyReason:       "maxAllocateRate(95) >= safetyMaxAllocateRate(90)",
		Warnings:               []string{"최종 Karpenter 사용률 조회 실패"},
	})

	for _, want := range []string{
		"SelectedDrainNodeCount: 2",
		"CordonedNodeCount: 1",
		"DrainedNodeCount: 1",
		"SuccessfulNodeCount: 1",
		"PDBBlockedPods: 1",
		"ProblemPodCount: 2",
		"UnmanagedPodCount: 3",
		"PodsWithFinalizers: 4",
		"StoppedBySafety: true",
		"Warnings: 최종 Karpenter 사용률 조회 실패",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("summary message missing %q: %s", want, message)
		}
	}
}

func TestSlackRetryOn5xx(t *testing.T) {
	attempts := 0
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		MaxRetries:   2,
		RetryBackoff: 1 * time.Millisecond,
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				status := http.StatusInternalServerError
				if attempts == 3 {
					status = http.StatusOK
				}
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader("retry")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	if err := notifier.SendNodeCount(context.Background(), 3); err != nil {
		t.Fatalf("SendNodeCount failed: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("unexpected attempts: got=%d want=3", attempts)
	}
}

func TestSlackNoRetryOn4xx(t *testing.T) {
	attempts := 0
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		MaxRetries:   3,
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader("bad request")),
					Header:     make(http.Header),
				}, nil
			}),
		},
	})

	if err := notifier.SendNodeCount(context.Background(), 3); err == nil {
		t.Fatal("expected error for 4xx response")
	}
	if attempts != 1 {
		t.Fatalf("4xx should not retry: got=%d want=1", attempts)
	}
}

func TestSlackContextTimeout(t *testing.T) {
	notifier := NewSlackNotifier(SlackConfig{
		WebhookURL:   "https://example.com/webhook",
		ClusterName:  "test-cluster",
		NodepoolName: "test-pool",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := notifier.SendNodeCount(ctx, 3); err == nil {
		t.Fatal("expected timeout error")
	}
}
