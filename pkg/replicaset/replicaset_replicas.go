package replicaset

import (
	"context"

	coreV1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetReplicaSetCount(clientSet kubernetes.Interface, pod coreV1.Pod) (int32, error) {
	// Deployment/ReplicaSet 파드만 처리
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Kind == "ReplicaSet" {
			rs, err := clientSet.AppsV1().ReplicaSets(pod.Namespace).Get(
				context.Background(),
				ownerRef.Name,
				metaV1.GetOptions{},
			)
			if err != nil {
				return 0, err
			}
			return *rs.Spec.Replicas, nil
		}
	}

	// ReplicaSet이 아닌 다른 종류의 파드는 3으로 처리하여 드레인 가능하도록 함
	return 3, nil
}
