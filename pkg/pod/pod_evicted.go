package pod

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreV1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func EvictPods(clientSet kubernetes.Interface, nodeName string) error {
	slog.Info("노드에서 pod evict 시작", "nodeName", nodeName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	gracePeriod := int64(60) // 일반 삭제 시 grace period seconds
	propagationPolicy := metaV1.DeletePropagationOrphan

	pods, err := GetNonCriticalPods(clientSet, nodeName)
	if err != nil {
		return fmt.Errorf("노드 %s 에서 데몬셋을 제외한 파드를 가져오는 중 오류가 발생했습니다.: %v", nodeName, err)
	}

	for _, pod := range pods {
		grace := &gracePeriod

		slog.Info("Pod 삭제 중", "nodeName", nodeName, "podName", pod.Name, "gracePeriod", *grace)
		err := clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metaV1.DeleteOptions{
			GracePeriodSeconds: grace,
			PropagationPolicy:  &propagationPolicy,
		})

		if err != nil {
			if errors.IsNotFound(err) {
				slog.Info(nodeName + "노드에서 pod evict 완료")
				continue
			}
			return fmt.Errorf("노드 %s 에서 %s 파드를 삭제하는 중 오류가 발생했습니다.: %v", nodeName, pod.Name, err)
		}
	}

	slog.Info("노드에서 pod evict 완료", "nodeName", nodeName)
	return nil
}

func GetNonCriticalPods(clientSet kubernetes.Interface, nodeName string) ([]coreV1.Pod, error) {
	podList, err := clientSet.CoreV1().Pods("").List(context.Background(), metaV1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase!=Succeeded,status.phase!=Failed", nodeName),
	})
	if err != nil {
		return nil, fmt.Errorf("%s 노드에서 파드 리스트를 가져오는 중 오류가 발생했습니다: %v", nodeName, err)
	}

	var pods []coreV1.Pod
	for _, pod := range podList.Items {
		if !isManagedByDaemonSet(pod) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

func isManagedByDaemonSet(pod coreV1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}
