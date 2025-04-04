package node

import (
	"context"
	"log/slog"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func CordonNode(clientSet kubernetes.Interface, nodeName string) error {
	node, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metaV1.GetOptions{})
	if err != nil {
		return err
	}

	// 이미 스케줄링 불가능 상태라면 스킵
	if node.Spec.Unschedulable {
		return nil
	}

	node.Spec.Unschedulable = true
	if _, err = clientSet.CoreV1().Nodes().Update(context.Background(), node, metaV1.UpdateOptions{}); err != nil {
		return err
	}
	slog.Info("노드 Cordon 완료", "nodeName", nodeName)

	return nil
}
