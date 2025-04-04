package pod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEvictPods(t *testing.T) {
	// 테스트 클라이언트 생성
	clientset := fake.NewSimpleClientset()

	// 테스트 파드 생성
	pod1 := &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
		},
		Spec: coreV1.PodSpec{
			NodeName: "test-node",
		},
	}

	pod2 := &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "default",
			OwnerReferences: []metaV1.OwnerReference{
				{
					Kind: "DaemonSet",
					Name: "test-ds",
				},
			},
		},
		Spec: coreV1.PodSpec{
			NodeName: "test-node",
		},
	}

	// 파드 생성
	_, err := clientset.CoreV1().Pods("default").Create(context.Background(), pod1, metaV1.CreateOptions{})
	assert.NoError(t, err)
	_, err = clientset.CoreV1().Pods("default").Create(context.Background(), pod2, metaV1.CreateOptions{})
	assert.NoError(t, err)

	// EvictPods 테스트
	err = EvictPods(clientset, "test-node")
	assert.NoError(t, err)

	// 파드가 정상적으로 삭제되었는지 확인
	pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metaV1.ListOptions{})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pods.Items)) // DaemonSet 파드만 남아있어야 함
	assert.Equal(t, "test-pod-2", pods.Items[0].Name)
}

func TestGetNonCriticalPods(t *testing.T) {
	// 테스트 클라이언트 생성
	clientset := fake.NewSimpleClientset()

	// 일반 파드와 DaemonSet 파드 생성
	normalPod := &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      "normal-pod",
			Namespace: "default",
		},
		Spec: coreV1.PodSpec{
			NodeName: "test-node",
		},
	}

	daemonSetPod := &coreV1.Pod{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      "daemonset-pod",
			Namespace: "default",
			OwnerReferences: []metaV1.OwnerReference{
				{
					Kind: "DaemonSet",
					Name: "test-ds",
				},
			},
		},
		Spec: coreV1.PodSpec{
			NodeName: "test-node",
		},
	}

	// 파드 생성
	_, err := clientset.CoreV1().Pods("default").Create(context.Background(), normalPod, metaV1.CreateOptions{})
	assert.NoError(t, err)
	_, err = clientset.CoreV1().Pods("default").Create(context.Background(), daemonSetPod, metaV1.CreateOptions{})
	assert.NoError(t, err)

	// GetNonCriticalPods 테스트
	pods, err := GetNonCriticalPods(clientset, "test-node")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(pods))
	assert.Equal(t, "normal-pod", pods[0].Name)
}

func TestIsManagedByDaemonSet(t *testing.T) {
	tests := []struct {
		name     string
		pod      coreV1.Pod
		expected bool
	}{
		{
			name: "DaemonSet Pod",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					OwnerReferences: []metaV1.OwnerReference{
						{
							Kind: "DaemonSet",
							Name: "test-ds",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Normal Pod",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					OwnerReferences: []metaV1.OwnerReference{
						{
							Kind: "ReplicaSet",
							Name: "test-rs",
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isManagedByDaemonSet(tt.pod)
			assert.Equal(t, tt.expected, result)
		})
	}
}
