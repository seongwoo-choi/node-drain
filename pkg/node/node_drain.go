package node

import (
	"app/pkg/karpenter"
	"app/pkg/notification"
	"app/pkg/pod"
	"app/types"
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func NodeDrain(clientSet kubernetes.Interface) ([]types.NodeDrainResult, error) {
	nodepoolName := os.Getenv("NODEPOOL_NAME")

	// 노드 목록을 가져옴
	nodepoolNode, err := getNodepoolNode(clientSet)
	if err != nil {
		return nil, err
	}

	// 생성 시간 기준으로 오름차순 정렬 (오래된 순)
	sort.Slice(nodepoolNode, func(i, j int) bool {
		return nodepoolNode[i].CreationTimestamp.Before(&nodepoolNode[j].CreationTimestamp)
	})

	// 드레인 할 노드 대수 가져오기
	drainNodeCount, err := getDrainNodeCount(clientSet, len(nodepoolNode))
	if err != nil {
		return nil, err
	}
	slog.Info("드레인 할 노드 개수", "drainNodeCount", drainNodeCount)

	// 노드 드레인 수행
	nodesToDrain := nodepoolNode[:drainNodeCount]
	for _, node := range nodesToDrain {
		if err := CordonNode(clientSet, node.Name); err != nil {
			return nil, fmt.Errorf("노드 %s를 cordon하는 데 실패했습니다: %w", node.Name, err)
		}
	}

	return handleDrain(clientSet, &coreV1.NodeList{Items: nodesToDrain}, drainNodeCount, nodepoolName)
}

func getNodepoolNode(clientSet kubernetes.Interface) ([]coreV1.Node, error) {
	nodepoolName := os.Getenv("NODEPOOL_NAME")
	nodes, err := clientSet.CoreV1().Nodes().List(context.Background(), metaV1.ListOptions{
		LabelSelector: fmt.Sprintf("karpenter.sh/nodepool=%s", nodepoolName),
	})
	if err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

func getDrainNodeCount(clientSet kubernetes.Interface, lenNodes int) (int, error) {
	slog.Info("노드 사용률 조회 중")
	slog.Info("현재 노드 개수", "lenNodes", lenNodes)

	// 초기 노드 수를 Slack으로 전송
	if err := notification.SendNodeCount(lenNodes); err != nil {
		slog.Error("초기 노드 수 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	memoryAllocateRate, err := karpenter.GetAllocateRate(clientSet, "memory")
	if err != nil {
		return 0, err
	}

	cpuAllocateRate, err := karpenter.GetAllocateRate(clientSet, "cpu")
	if err != nil {
		return 0, err
	}

	if err := notification.SendKarpenterAllocateRate(memoryAllocateRate, cpuAllocateRate); err != nil {
		slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	maxAllocateRate := int(math.Max(float64(memoryAllocateRate), float64(cpuAllocateRate)))
	slog.Info("최대 사용률", "maxAllocateRate", maxAllocateRate)

	drainRate := float64(99-maxAllocateRate) / 100.0
	slog.Info("드레인 비율", "drainRate", drainRate)

	drainNodeCount := int(float64(lenNodes) * drainRate)

	return drainNodeCount, nil
}

func handleDrain(clientSet kubernetes.Interface, nodes *coreV1.NodeList, drainNodeCount int, nodepoolName string) ([]types.NodeDrainResult, error) {
	var results []types.NodeDrainResult

	// drainNodeCount 만큼만 가장 오래된 노드부터 처리
	for i := 0; i < drainNodeCount && i < len(nodes.Items); i++ {
		node := nodes.Items[i] // 이미 정렬된 상태이므로 순서대로 처리

		// nodepool 확인
		if strings.TrimSpace(node.Labels["karpenter.sh/nodepool"]) == nodepoolName {
			if err := drainSingleNode(clientSet, node.Name); err != nil {
				return nil, err
			}

			results = append(results, types.NodeDrainResult{
				NodeName:     node.Name,
				InstanceType: node.Labels["beta.kubernetes.io/instance-type"],
				NodepoolName: nodepoolName,
				Age:          node.CreationTimestamp.Format(time.RFC3339),
			})
		}
	}

	memoryAllocateRate, err := karpenter.GetAllocateRate(clientSet, "memory")
	if err != nil {
		return nil, err
	}

	cpuAllocateRate, err := karpenter.GetAllocateRate(clientSet, "cpu")
	if err != nil {
		return nil, err
	}

	slog.Info("Memory 사용률", "memoryAllocateRate", memoryAllocateRate)
	slog.Info("Cpu 사용률", "cpuAllocateRate", cpuAllocateRate)

	if err := notification.SendKarpenterAllocateRate(memoryAllocateRate, cpuAllocateRate); err != nil {
		slog.Error("Karpenter 사용률 알림 전송 실패", "error", err)
		// 알림 실패는 드레인 작업 진행에 영향을 주지 않도록 에러를 반환하지 않음
	}

	return results, nil
}

func drainSingleNode(clientSet kubernetes.Interface, nodeName string) error {
	err := pod.EvictPods(clientSet, nodeName)
	if err != nil {
		return fmt.Errorf("노드 %s 에서 파드를 제거하는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	if err := waitForPodsToTerminate(clientSet, nodeName); err != nil {
		return fmt.Errorf("노드 %s 에서 파드가 종료되는 중 오류가 발생했습니다.: %w", nodeName, err)
	}

	time.Sleep(150 * time.Second)

	return nil
}

func waitForPodsToTerminate(clientSet kubernetes.Interface, nodeName string) error {
	slog.Info("노드에서 데몬셋을 제외한 모든 파드가 종료될 때까지 기다리는 중", "nodeName", nodeName)

	_, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for {
		pods, err := pod.GetNonCriticalPods(clientSet, nodeName)
		if err != nil {
			return fmt.Errorf("노드 %s 에서 데몬셋을 제외한 파드를 가져오는 중 오류가 발생했습니다.: %v", nodeName, err)
		}

		if len(pods) == 0 {
			slog.Info("데몬셋을 제외한 모든 Pod가 종료됨", "nodeName", nodeName)
			return nil
		}

		slog.Info("Pod 종료 대기 중", "nodeName", nodeName, "remainingPods", len(pods))
		time.Sleep(15 * time.Second)
	}
}
