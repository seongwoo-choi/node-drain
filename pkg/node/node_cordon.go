package node

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CordonNode marks a node unschedulable.
func CordonNode(ctx context.Context, clientSet kubernetes.Interface, nodeName string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if clientSet == nil {
		return fmt.Errorf("kubernetes client is required")
	}
	if strings.TrimSpace(nodeName) == "" {
		return fmt.Errorf("node name is required")
	}

	node, err := clientSet.CoreV1().Nodes().Get(ctx, nodeName, metaV1.GetOptions{})
	if err != nil {
		return err
	}

	// 이미 스케줄링 불가능 상태라면 스킵
	if node.Spec.Unschedulable {
		return nil
	}

	node.Spec.Unschedulable = true
	if _, err = clientSet.CoreV1().Nodes().Update(ctx, node, metaV1.UpdateOptions{}); err != nil {
		return err
	}
	slog.Info("노드 Cordon 완료", "nodeName", nodeName)

	return nil
}
