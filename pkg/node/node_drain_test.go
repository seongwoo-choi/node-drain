package node

import (
	"context"
	"testing"
	"time"

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

	t.Run("성공적인 노드 드레인", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cancel()

		clientSet := fake.NewSimpleClientset()

		// 테스트용 노드와 파드 생성
		node := &coreV1.Node{
			ObjectMeta: metaV1.ObjectMeta{
				Name: "test-node",
			},
		}

		pod := &coreV1.Pod{
			ObjectMeta: metaV1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Spec: coreV1.PodSpec{
				NodeName: "test-node",
			},
		}

		clientSet.CoreV1().Nodes().Create(ctx, node, metaV1.CreateOptions{})
		clientSet.CoreV1().Pods("default").Create(ctx, pod, metaV1.CreateOptions{})

		err := drainSingleNode(clientSet, "test-node")
		assert.NoError(t, err)
	})
}
