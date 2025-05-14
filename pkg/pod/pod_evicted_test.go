package pod

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	coreV1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEvictPods(t *testing.T) {
	tests := []struct {
		name          string
		nodeName      string
		pods          []coreV1.Pod
		config        *EvictionConfig
		expectedError bool
	}{
		{
			name:     "정상적인 파드 제거",
			nodeName: "node-1",
			pods: []coreV1.Pod{
				{
					ObjectMeta: metaV1.ObjectMeta{
						Name:      "pod-1",
						Namespace: "default",
					},
					Spec: coreV1.PodSpec{
						NodeName: "node-1",
					},
				},
			},
			config:        DefaultEvictionConfig(),
			expectedError: false,
		},
		{
			name:     "PDB로 인한 제거 실패",
			nodeName: "node-1",
			pods: []coreV1.Pod{
				{
					ObjectMeta: metaV1.ObjectMeta{
						Name:      "pod-1",
						Namespace: "default",
						Labels: map[string]string{
							"app": "test",
						},
					},
					Spec: coreV1.PodSpec{
						NodeName: "node-1",
					},
				},
			},
			config:        DefaultEvictionConfig(),
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// 테스트용 파드 추가
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(context.Background(), &pod, metaV1.CreateOptions{})
				if err != nil {
					t.Fatalf("파드 생성 실패: %v", err)
				}
			}

			// PDB 추가 (필요한 경우)
			if tt.name == "PDB로 인한 제거 실패" {
				pdb := &policyv1.PodDisruptionBudget{
					ObjectMeta: metaV1.ObjectMeta{
						Name:      "test-pdb",
						Namespace: "default",
					},
					Spec: policyv1.PodDisruptionBudgetSpec{
						MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
						Selector: &metaV1.LabelSelector{
							MatchLabels: map[string]string{"app": "test"},
						},
					},
				}
				_, err := client.PolicyV1().PodDisruptionBudgets("default").Create(context.Background(), pdb, metaV1.CreateOptions{})
				if err != nil {
					t.Fatalf("PDB 생성 실패: %v", err)
				}
			}

			err := EvictPods(client, tt.nodeName, tt.config)

			if (err != nil) != tt.expectedError {
				t.Errorf("EvictPods() error = %v, expectedError %v", err, tt.expectedError)
			}
		})
	}
}

func TestEvictPod(t *testing.T) {
	tests := []struct {
		name          string
		pod           coreV1.Pod
		podExists     bool
		expectedError bool
	}{
		{
			name: "존재하는 파드 제거",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "exist-pod",
					Namespace: "default",
				},
			},
			podExists:     true,
			expectedError: false,
		},
		{
			name: "존재하지 않는 파드 제거",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "non-exist-pod",
					Namespace: "default",
				},
			},
			podExists:     false,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// 파드가 존재하는 경우에만 생성
			if tt.podExists {
				_, err := client.CoreV1().Pods(tt.pod.Namespace).Create(context.Background(), &tt.pod, metaV1.CreateOptions{})
				if err != nil {
					t.Fatalf("파드 생성 실패: %v", err)
				}
			}

			// evictPod 테스트
			err := evictPod(context.Background(), client, tt.pod)

			if (err != nil) != tt.expectedError {
				t.Errorf("evictPod() error = %v, expectedError %v", err, tt.expectedError)
			}
		})
	}
}

func TestWaitForPodDeletion(t *testing.T) {
	tests := []struct {
		name          string
		pod           coreV1.Pod
		config        *EvictionConfig
		deletePod     bool
		expectedError bool
	}{
		{
			name: "파드가 정상적으로 삭제됨",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			config:        DefaultEvictionConfig(),
			deletePod:     true,
			expectedError: false,
		},
		{
			name: "파드 삭제 타임아웃",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "pod-2",
					Namespace: "default",
				},
			},
			config: &EvictionConfig{
				PodDeletionTimeout: 1 * time.Second,
				CheckInterval:      100 * time.Millisecond,
			},
			deletePod:     false,
			expectedError: true,
		},
		{
			name: "파드가 이미 존재하지 않음",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "pod-3",
					Namespace: "default",
				},
			},
			config:        DefaultEvictionConfig(),
			deletePod:     false, // 파드를 생성하지 않음
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			if tt.name != "파드가 이미 존재하지 않음" {
				// 테스트용 파드 추가
				_, err := client.CoreV1().Pods(tt.pod.Namespace).Create(context.Background(), &tt.pod, metaV1.CreateOptions{})
				if err != nil {
					t.Fatalf("파드 생성 실패: %v", err)
				}
			}

			if tt.deletePod {
				// 파드를 삭제하여 테스트 케이스 준비
				err := client.CoreV1().Pods(tt.pod.Namespace).Delete(context.Background(), tt.pod.Name, metaV1.DeleteOptions{})
				if err != nil {
					t.Fatalf("파드 삭제 실패: %v", err)
				}
			}

			err := waitForPodDeletion(context.Background(), client, tt.pod, tt.config)

			if (err != nil) != tt.expectedError {
				t.Errorf("waitForPodDeletion() error = %v, expectedError %v", err, tt.expectedError)
			}
		})
	}
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

func TestEvictPodWithRetry(t *testing.T) {
	tests := []struct {
		name          string
		pod           coreV1.Pod
		config        *EvictionConfig
		podExists     bool
		expectedError bool
	}{
		{
			name: "정상적인 파드 제거",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			config:        DefaultEvictionConfig(),
			podExists:     true,
			expectedError: false,
		},
		{
			name: "파드가 이미 존재하지 않음",
			pod: coreV1.Pod{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      "pod-2",
					Namespace: "default",
				},
			},
			config:        DefaultEvictionConfig(),
			podExists:     false,
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			if tt.podExists {
				// 테스트용 파드 추가
				_, err := client.CoreV1().Pods(tt.pod.Namespace).Create(context.Background(), &tt.pod, metaV1.CreateOptions{})
				if err != nil {
					t.Fatalf("파드 생성 실패: %v", err)
				}
			}

			// evictPodWithRetry 테스트
			err := evictPodWithRetry(context.Background(), client, tt.pod, tt.config)

			if (err != nil) != tt.expectedError {
				t.Errorf("evictPodWithRetry() error = %v, expectedError %v", err, tt.expectedError)
			}
		})
	}
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
