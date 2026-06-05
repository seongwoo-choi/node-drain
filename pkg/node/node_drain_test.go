package node

import (
	"app/pkg/pod"
	"app/types"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type fakeAllocateRateProvider struct {
	rates map[string]int
}

func (f fakeAllocateRateProvider) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	return f.rates[resourceType], nil
}

type sequenceAllocateRateProvider struct {
	rates map[string][]int
	calls map[string]int
}

func (f *sequenceAllocateRateProvider) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	values := f.rates[resourceType]
	if len(values) == 0 {
		return 0, nil
	}

	idx := f.calls[resourceType]
	f.calls[resourceType] = idx + 1
	if idx >= len(values) {
		return values[len(values)-1], nil
	}
	return values[idx], nil
}

type failOnSafetyRecheckAllocateRateProvider struct {
	calls map[string]int
}

func (f *failOnSafetyRecheckAllocateRateProvider) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[resourceType]++
	if f.calls[resourceType] > 1 {
		return 0, errors.New("metrics backend unavailable")
	}
	return 30, nil
}

type failingAllocateRateProvider struct {
	calls int
}

func (f *failingAllocateRateProvider) GetAllocateRate(ctx context.Context, resourceType string) (int, error) {
	f.calls++
	return 0, fmt.Errorf("unexpected allocate rate lookup for %s", resourceType)
}

type fakeNotifier struct{}

func (f fakeNotifier) SendNodeDrainComplete(ctx context.Context, results []types.NodeDrainResult) error {
	return nil
}
func (f fakeNotifier) SendNodeDrainError(ctx context.Context, err error) error {
	return nil
}
func (f fakeNotifier) SendNodeCount(ctx context.Context, nodeCount int) error {
	return nil
}
func (f fakeNotifier) SendKarpenterAllocateRate(ctx context.Context, memoryAllocateRate int, cpuAllocateRate int) error {
	return nil
}

func TestNodeDrainSelectsExpectedNodeCount(t *testing.T) {
	tests := []struct {
		name              string
		nodeCount         int
		memoryRate        int
		cpuRate           int
		expectedDrain     int
		expectedNodeNames []string
	}{
		{
			name:              "drain 대상 0개",
			nodeCount:         3,
			memoryRate:        99,
			cpuRate:           99,
			expectedDrain:     0,
			expectedNodeNames: []string{},
		},
		{
			name:              "drain 대상 1개",
			nodeCount:         3,
			memoryRate:        40,
			cpuRate:           20,
			expectedDrain:     1,
			expectedNodeNames: []string{"node-1"},
		},
		{
			name:              "drain 대상 N개",
			nodeCount:         4,
			memoryRate:        30,
			cpuRate:           25,
			expectedDrain:     2,
			expectedNodeNames: []string{"node-1", "node-2"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DRAIN_POLICY", "formula")
			t.Setenv("DRAIN_ROUNDING", "floor")
			t.Setenv("DRAIN_MIN", "0")
			t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
			t.Setenv("DRAIN_MAX_FRACTION", "0")
			t.Setenv("DRAIN_STEP_RULES", "")
			t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
			t.Setenv("DRAIN_SAFETY_QUERIES", "")
			t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
			t.Setenv("DRAIN_PROGRESSIVE", "false")

			clientSet := fake.NewSimpleClientset()
			nodepoolName := "test-nodepool"

			for i := 1; i <= tt.nodeCount; i++ {
				node := newNode(nodepoolName, i)
				if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
					t.Fatalf("노드 생성 실패: %v", err)
				}
			}

			results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
				AllocateRateProvider: fakeAllocateRateProvider{
					rates: map[string]int{
						"memory": tt.memoryRate,
						"cpu":    tt.cpuRate,
					},
				},
				Notifier: fakeNotifier{},
			}, DrainConfig{
				NodepoolName: nodepoolName,
				Eviction: &pod.EvictionConfig{
					MaxConcurrentEvictions:   2,
					MaxRetries:               1,
					RetryBackoffDuration:     1 * time.Millisecond,
					PodDeletionTimeout:       1 * time.Second,
					CheckInterval:            10 * time.Millisecond,
					EvictionTimeout:          2 * time.Second,
					NodeTerminationTimeout:   1 * time.Second,
					NodeTerminationCheckTick: 10 * time.Millisecond,
					PostEvictionNodeDelay:    0,
				},
			})
			if err != nil {
				t.Fatalf("NodeDrain 실패: %v", err)
			}

			if len(results) != tt.expectedDrain {
				t.Fatalf("drain 결과 개수 불일치: got=%d want=%d", len(results), tt.expectedDrain)
			}

			for idx, result := range results {
				if result.NodeName != tt.expectedNodeNames[idx] {
					t.Fatalf("drain 노드 순서 불일치: got=%s want=%s", result.NodeName, tt.expectedNodeNames[idx])
				}
				if !result.Success {
					t.Fatalf("노드 드레인 실패 결과: %+v", result)
				}
			}
		})
	}
}

func TestNodeDrainWithReportSkipsMetricsForEmptyNodepool(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "90")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	provider := &failingAllocateRateProvider{}

	report, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: provider,
		Notifier:             fakeNotifier{},
	}, DrainConfig{
		NodepoolName: "empty-nodepool",
		Eviction:     testEvictionConfig(),
	})
	if err != nil {
		t.Fatalf("NodeDrainWithReport 실패: %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("빈 노드풀은 metric provider를 호출하면 안 됨: calls=%d", provider.calls)
	}
	if len(report.Results) != 0 {
		t.Fatalf("빈 노드풀 결과 개수 불일치: got=%d want=0", len(report.Results))
	}
	if report.Summary.TotalNodesInNodepool != 0 {
		t.Fatalf("total node count 불일치: got=%d want=0", report.Summary.TotalNodesInNodepool)
	}
	if report.Summary.PlannedDrainNodeCount != 0 {
		t.Fatalf("planned drain count 불일치: got=%d want=0", report.Summary.PlannedDrainNodeCount)
	}
	if report.Summary.SelectedDrainNodeCount != 0 {
		t.Fatalf("selected drain count 불일치: got=%d want=0", report.Summary.SelectedDrainNodeCount)
	}
	if report.Summary.TargetNodepool != "empty-nodepool" {
		t.Fatalf("target nodepool 불일치: got=%s want=empty-nodepool", report.Summary.TargetNodepool)
	}
}

func TestNodeDrainWithReportAllowsEmptyNodepoolWithoutAllocateProvider(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()

	report, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName: "empty-nodepool",
		Eviction:     testEvictionConfig(),
	})
	if err != nil {
		t.Fatalf("NodeDrainWithReport 실패: %v", err)
	}
	if len(report.Results) != 0 {
		t.Fatalf("빈 노드풀 결과 개수 불일치: got=%d want=0", len(report.Results))
	}
	if report.Summary.TotalNodesInNodepool != 0 {
		t.Fatalf("total node count 불일치: got=%d want=0", report.Summary.TotalNodesInNodepool)
	}
	if report.Summary.PlannedDrainNodeCount != 0 {
		t.Fatalf("planned drain count 불일치: got=%d want=0", report.Summary.PlannedDrainNodeCount)
	}
}

func TestNodeDrainWithReportRequiresAllocateProviderWhenNodepoolHasNodes(t *testing.T) {
	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	node := newNode(nodepoolName, 1)
	if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("노드 생성 실패: %v", err)
	}

	_, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
	})
	if err == nil {
		t.Fatal("expected allocate provider error, got nil")
	}
	if !strings.Contains(err.Error(), "allocate rate provider is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNodeDrainProgressiveDoesNotPreCordonRemainingNodes(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "90")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	provider := &sequenceAllocateRateProvider{
		rates: map[string][]int{
			"memory": {30, 95, 95},
			"cpu":    {30, 95, 95},
		},
		calls: map[string]int{},
	}

	results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: provider,
		Notifier:             fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
	})
	if err != nil {
		t.Fatalf("NodeDrain 실패: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("drain 결과 개수 불일치: got=%d want=1", len(results))
	}
	if results[0].NodeName != "node-1" {
		t.Fatalf("drain 노드 순서 불일치: got=%s want=node-1", results[0].NodeName)
	}

	assertNodeUnschedulable(t, clientSet, "node-1", true)
	assertNodeUnschedulable(t, clientSet, "node-2", false)
	assertNodeUnschedulable(t, clientSet, "node-3", false)
}

func TestNodeDrainProgressiveFailClosedStopsOnSafetyRecheckError(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "90")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: &failOnSafetyRecheckAllocateRateProvider{},
		Notifier:             fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
	})
	if err != nil {
		t.Fatalf("NodeDrain 실패: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("drain 결과 개수 불일치: got=%d want=1", len(results))
	}

	assertNodeUnschedulable(t, clientSet, "node-1", true)
	assertNodeUnschedulable(t, clientSet, "node-2", false)
	assertNodeUnschedulable(t, clientSet, "node-3", false)
}

func TestNodeDrainWithReportSummarizesSafetyStop(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "0")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "90")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	provider := &sequenceAllocateRateProvider{
		rates: map[string][]int{
			"memory": {30, 95, 95},
			"cpu":    {30, 95, 95},
		},
		calls: map[string]int{},
	}

	report, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: provider,
		Notifier:             fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
	})
	if err != nil {
		t.Fatalf("NodeDrainWithReport 실패: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("drain 결과 개수 불일치: got=%d want=1", len(report.Results))
	}
	if report.Summary.TotalNodesInNodepool != 3 {
		t.Fatalf("total node count 불일치: got=%d want=3", report.Summary.TotalNodesInNodepool)
	}
	if report.Summary.PlannedDrainNodeCount != 2 {
		t.Fatalf("planned drain count 불일치: got=%d want=2", report.Summary.PlannedDrainNodeCount)
	}
	if report.Summary.SelectedDrainNodeCount != 2 {
		t.Fatalf("selected drain count 불일치: got=%d want=2", report.Summary.SelectedDrainNodeCount)
	}
	if report.Summary.DrainedNodeCount != 1 {
		t.Fatalf("drained count 불일치: got=%d want=1", report.Summary.DrainedNodeCount)
	}
	if !report.Summary.StoppedBySafety {
		t.Fatalf("expected StoppedBySafety=true: %+v", report.Summary)
	}
	if !strings.Contains(report.Summary.StopSafetyReason, "maxAllocateRate") {
		t.Fatalf("unexpected safety stop reason: %s", report.Summary.StopSafetyReason)
	}
}

func TestNodeDrainDryRunPlansPodsWithoutCordonOrEvict(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	pods := []*coreV1.Pod{
		newPodOnNode("default", "workload-pod", "node-1", coreV1.PodRunning, "ReplicaSet", "workload-rs"),
		newPodOnNode("default", "daemon-pod", "node-1", coreV1.PodRunning, "DaemonSet", "daemon"),
		newPodOnNode("default", "done-pod", "node-1", coreV1.PodSucceeded, "Job", "done-job"),
	}
	for _, p := range pods {
		if _, err := clientSet.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("파드 생성 실패: %v", err)
		}
	}

	results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("NodeDrain dry-run 실패: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("dry-run 결과 개수 불일치: got=%d want=1", len(results))
	}
	result := results[0]
	if !result.DryRun {
		t.Fatal("expected dry-run result")
	}
	if !result.Success {
		t.Fatalf("dry-run 결과 실패: %+v", result)
	}
	if len(result.PlannedPods) != 1 {
		t.Fatalf("planned pod 개수 불일치: got=%d want=1 %+v", len(result.PlannedPods), result.PlannedPods)
	}
	if result.PlannedPods[0].Name != "workload-pod" {
		t.Fatalf("planned pod 불일치: got=%s want=workload-pod", result.PlannedPods[0].Name)
	}

	assertNodeUnschedulable(t, clientSet, "node-1", false)
	assertNodeUnschedulable(t, clientSet, "node-2", false)
	assertNodeUnschedulable(t, clientSet, "node-3", false)
	if _, err := clientSet.CoreV1().Pods("default").Get(context.Background(), "workload-pod", metaV1.GetOptions{}); err != nil {
		t.Fatalf("dry-run은 pod를 삭제하면 안 됨: %v", err)
	}
}

func TestNodeDrainWithReportSummarizesDryRunPlan(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "true")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	p := newPodOnNode("default", "workload-pod", "node-1", coreV1.PodRunning, "ReplicaSet", "workload-rs")
	if _, err := clientSet.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("파드 생성 실패: %v", err)
	}

	report, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     testEvictionConfig(),
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("NodeDrainWithReport dry-run 실패: %v", err)
	}
	if !report.Summary.DryRun {
		t.Fatalf("expected dry-run summary: %+v", report.Summary)
	}
	if report.Summary.PlannedDrainNodeCount != 1 {
		t.Fatalf("planned drain count 불일치: got=%d want=1", report.Summary.PlannedDrainNodeCount)
	}
	if report.Summary.PlannedPodCount != 1 {
		t.Fatalf("planned pod count 불일치: got=%d want=1", report.Summary.PlannedPodCount)
	}
	if report.Summary.DrainedNodeCount != 0 {
		t.Fatalf("dry-run drained count 불일치: got=%d want=0", report.Summary.DrainedNodeCount)
	}
	if report.Summary.SuccessfulNodeCount != 1 {
		t.Fatalf("successful node count 불일치: got=%d want=1", report.Summary.SuccessfulNodeCount)
	}
}

func TestNodeDrainWithReportSummarizesActualPodEviction(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "false")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 2; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	p := newPodOnNode("default", "workload-pod", "node-1", coreV1.PodRunning, "ReplicaSet", "workload-rs")
	if _, err := clientSet.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("파드 생성 실패: %v", err)
	}

	evictionCfg := testEvictionConfig()
	evictionCfg.DeleteAfterEviction = true

	report, err := NodeDrainWithReport(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName: nodepoolName,
		Eviction:     evictionCfg,
	})
	if err != nil {
		t.Fatalf("NodeDrainWithReport 실패: %v", err)
	}
	if report.Summary.TotalPods != 1 {
		t.Fatalf("total pod count 불일치: got=%d want=1", report.Summary.TotalPods)
	}
	if report.Summary.EvictedPods != 1 {
		t.Fatalf("evicted pod count 불일치: got=%d want=1", report.Summary.EvictedPods)
	}
	if report.Summary.DeletedPods != 1 {
		t.Fatalf("deleted pod count 불일치: got=%d want=1", report.Summary.DeletedPods)
	}
	if report.Summary.DrainedNodeCount != 1 {
		t.Fatalf("drained node count 불일치: got=%d want=1", report.Summary.DrainedNodeCount)
	}
}

func TestWaitForPodsToTerminateReturnsImmediatelyWhenNoPodsRemain(t *testing.T) {
	clientSet := fake.NewSimpleClientset()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cfg := testEvictionConfig()
	cfg.NodeTerminationTimeout = time.Second
	cfg.NodeTerminationCheckTick = time.Hour

	start := time.Now()
	if err := waitForPodsToTerminate(ctx, clientSet, "node-empty", cfg); err != nil {
		t.Fatalf("waitForPodsToTerminate failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Fatalf("waitForPodsToTerminate took %s, expected immediate return before context timeout", elapsed)
	}
}

func TestNodeDrainCanSkipUnschedulableNodes(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "false")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if i == 1 {
			node.Spec.Unschedulable = true
		}
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName:          nodepoolName,
		Eviction:              testEvictionConfig(),
		NodeSelectionStrategy: DrainNodeSelectionOldest,
		SkipUnschedulable:     true,
	})
	if err != nil {
		t.Fatalf("NodeDrain 실패: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("drain 결과 개수 불일치: got=%d want=1", len(results))
	}
	if results[0].NodeName != "node-2" {
		t.Fatalf("drain 노드 선택 불일치: got=%s want=node-2", results[0].NodeName)
	}

	assertNodeUnschedulable(t, clientSet, "node-1", true)
	assertNodeUnschedulable(t, clientSet, "node-2", true)
	assertNodeUnschedulable(t, clientSet, "node-3", false)
}

func TestNodeDrainSelectsEmptyFirstNode(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "false")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 3; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	p := newPodOnNode("default", "workload-pod", "node-1", coreV1.PodRunning, "ReplicaSet", "workload-rs")
	if _, err := clientSet.CoreV1().Pods(p.Namespace).Create(context.Background(), p, metaV1.CreateOptions{}); err != nil {
		t.Fatalf("파드 생성 실패: %v", err)
	}

	results, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName:          nodepoolName,
		Eviction:              testEvictionConfig(),
		NodeSelectionStrategy: DrainNodeSelectionEmptyFirst,
	})
	if err != nil {
		t.Fatalf("NodeDrain 실패: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("drain 결과 개수 불일치: got=%d want=1", len(results))
	}
	if results[0].NodeName != "node-2" {
		t.Fatalf("drain 노드 선택 불일치: got=%s want=node-2", results[0].NodeName)
	}

	assertNodeUnschedulable(t, clientSet, "node-1", false)
	assertNodeUnschedulable(t, clientSet, "node-2", true)
	assertNodeUnschedulable(t, clientSet, "node-3", false)
}

func TestNodeDrainRejectsInvalidNodeSelectionStrategy(t *testing.T) {
	t.Setenv("DRAIN_POLICY", "formula")
	t.Setenv("DRAIN_ROUNDING", "floor")
	t.Setenv("DRAIN_MIN", "0")
	t.Setenv("DRAIN_MAX_ABSOLUTE", "1")
	t.Setenv("DRAIN_MAX_FRACTION", "0")
	t.Setenv("DRAIN_STEP_RULES", "")
	t.Setenv("DRAIN_SAFETY_MAX_ALLOCATE_RATE", "0")
	t.Setenv("DRAIN_SAFETY_QUERIES", "")
	t.Setenv("DRAIN_SAFETY_FAIL_CLOSED", "true")
	t.Setenv("DRAIN_PROGRESSIVE", "false")

	clientSet := fake.NewSimpleClientset()
	nodepoolName := "test-nodepool"
	for i := 1; i <= 2; i++ {
		node := newNode(nodepoolName, i)
		if _, err := clientSet.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{}); err != nil {
			t.Fatalf("노드 생성 실패: %v", err)
		}
	}

	_, err := NodeDrain(context.Background(), clientSet, DrainDependencies{
		AllocateRateProvider: fakeAllocateRateProvider{
			rates: map[string]int{
				"memory": 30,
				"cpu":    30,
			},
		},
		Notifier: fakeNotifier{},
	}, DrainConfig{
		NodepoolName:          nodepoolName,
		Eviction:              testEvictionConfig(),
		NodeSelectionStrategy: "unknown",
	})
	if err == nil {
		t.Fatal("expected invalid node selection strategy error, got nil")
	}
	if !strings.Contains(err.Error(), "지원하지 않는 드레인 노드 선택 전략") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testEvictionConfig() *pod.EvictionConfig {
	return &pod.EvictionConfig{
		MaxConcurrentEvictions:   2,
		MaxRetries:               1,
		RetryBackoffDuration:     1 * time.Millisecond,
		PodDeletionTimeout:       1 * time.Second,
		CheckInterval:            10 * time.Millisecond,
		EvictionTimeout:          2 * time.Second,
		NodeTerminationTimeout:   1 * time.Second,
		NodeTerminationCheckTick: 10 * time.Millisecond,
		PostEvictionNodeDelay:    0,
	}
}

func newPodOnNode(namespace string, name string, nodeName string, phase coreV1.PodPhase, ownerKind string, ownerName string) *coreV1.Pod {
	return &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			OwnerReferences: []metaV1.OwnerReference{
				{
					Kind: ownerKind,
					Name: ownerName,
				},
			},
		},
		Spec: coreV1.PodSpec{
			NodeName: nodeName,
			Containers: []coreV1.Container{
				{Name: "container"},
			},
		},
		Status: coreV1.PodStatus{
			Phase: phase,
		},
	}
}

func assertNodeUnschedulable(t *testing.T, clientSet *fake.Clientset, nodeName string, want bool) {
	t.Helper()

	node, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metaV1.GetOptions{})
	if err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	if node.Spec.Unschedulable != want {
		t.Fatalf("node %s unschedulable 불일치: got=%t want=%t", nodeName, node.Spec.Unschedulable, want)
	}
}

func newNode(nodepool string, order int) *coreV1.Node {
	ts := time.Date(2024, 1, order, 0, 0, 0, 0, time.UTC)
	return &coreV1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name: fmt.Sprintf("node-%d", order),
			Labels: map[string]string{
				"karpenter.sh/nodepool":            nodepool,
				"beta.kubernetes.io/instance-type": "t3.large",
			},
			CreationTimestamp: metaV1.NewTime(ts),
		},
	}
}
