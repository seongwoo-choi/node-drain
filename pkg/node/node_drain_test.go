package node

import (
	"context"
	"fmt"
	"testing"
	"time"

	"app/pkg/pod"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// MockKubernetesInterface는 테스트를 위한 mock 클라이언트입니다
type MockKubernetesInterface struct {
	mock.Mock
	kubernetes.Interface
}

func TestDrainSingleNode(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("단축 모드에서 테스트를 건너뜁니다.")
	}

	// 테스트용 설정
	testConfig := &pod.EvictionConfig{
		MaxConcurrentEvictions: 2,
		MaxRetries:             3,
		RetryBackoffDuration:   1 * time.Second,
		PodDeletionTimeout:     10 * time.Second,
		CheckInterval:          1 * time.Second,
		EvictionMode:           pod.EvictionModeDelete, // fake client에서 eviction(subresource) 삭제 반영이 어려워 delete 모드로 테스트
	}

	tests := []struct {
		name          string
		nodeName      string
		node          *coreV1.Node
		pods          []*coreV1.Pod
		expectedError bool
	}{
		{
			name:     "성공적인 노드 드레인",
			nodeName: "test-node",
			node: &coreV1.Node{
				ObjectMeta: metaV1.ObjectMeta{
					Name: "test-node",
				},
				Status: coreV1.NodeStatus{
					Conditions: []coreV1.NodeCondition{
						{
							Type:   coreV1.NodeReady,
							Status: coreV1.ConditionTrue,
						},
					},
				},
			},
			pods: []*coreV1.Pod{
				{
					ObjectMeta: metaV1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
					},
					Spec: coreV1.PodSpec{
						NodeName: "test-node",
					},
				},
			},
			expectedError: false,
		},
		{
			name:          "존재하지 않는 노드 드레인",
			nodeName:      "non-existent-node",
			node:          nil,
			pods:          []*coreV1.Pod{},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			clientSet := fake.NewSimpleClientset()

			// 테스트용 노드 생성
			if tt.node != nil {
				_, err := clientSet.CoreV1().Nodes().Create(ctx, tt.node, metaV1.CreateOptions{})
				assert.NoError(t, err)
			}

			// 테스트용 파드 생성
			for _, pod := range tt.pods {
				_, err := clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metaV1.CreateOptions{})
				assert.NoError(t, err)
			}

			// 테스트용 설정으로 함수 호출
			err := drainSingleNodeWithConfig(clientSet, tt.nodeName, testConfig)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// 테스트용 함수 추가
func drainSingleNodeWithConfig(clientSet kubernetes.Interface, nodeName string, config *pod.EvictionConfig) error {
	if err := CordonNode(clientSet, nodeName); err != nil {
		return fmt.Errorf("%s 노드를 cordon 하는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	if err := pod.EvictPods(clientSet, nodeName, config); err != nil {
		return fmt.Errorf("노드 %s 에서 파드를 제거하는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	if err := waitForPodsToTerminate(clientSet, nodeName); err != nil {
		return fmt.Errorf("노드 %s 에서 파드가 종료되는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	// 테스트에서는 짧은 시간으로 설정
	time.Sleep(1 * time.Second)

	return nil
}
