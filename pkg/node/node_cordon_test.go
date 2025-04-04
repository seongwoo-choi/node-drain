package node

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func CreateMockNode(name string, labels, annotations map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func TestCordonNode(t *testing.T) {
	// 테스트용 클라이언트 생성
	client := fake.NewSimpleClientset()

	// 테스트용 노드 생성
	node := CreateMockNode("test-node", nil, nil)
	_, err := client.CoreV1().Nodes().Create(context.Background(), node, metaV1.CreateOptions{})
	if err != nil {
		t.Fatalf("목킹용 : %v", err)
	}

	// 테스트 실행
	err = CordonNode(client, "test-node")
	if err != nil {
		t.Errorf("노드 Cordon 을 실패했습니다.: %v", err)
	}

	// 결과 확인
	updatedNode, err := client.CoreV1().Nodes().Get(context.Background(), "test-node", metaV1.GetOptions{})
	if err != nil {
		t.Errorf("노드를 가져오는 도중 오류가 발생했습니다.: %v", err)
	}
	if !updatedNode.Spec.Unschedulable {
		t.Error("노드 Cordon 이후 노드가 스케줄링 불가능 상태가 아닙니다.")
	}
}
