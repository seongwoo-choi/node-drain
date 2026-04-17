package node

import (
	"app/pkg/pod"
	"app/types"
	"context"
	"fmt"
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
