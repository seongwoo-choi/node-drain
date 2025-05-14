package pod

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	coreV1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// EvictionConfig는 eviction 관련 설정을 담는 구조체입니다
type EvictionConfig struct {
	MaxConcurrentEvictions int           // 동시 eviction 최대 개수
	MaxRetries             int           // 최대 재시도 횟수
	RetryBackoffDuration   time.Duration // 재시도 간격
	PodDeletionTimeout     time.Duration // 파드 삭제 대기 시간
	CheckInterval          time.Duration // 상태 확인 주기
}

// PDB 캐시를 위한 구조체
type pdbCache struct {
	sync.RWMutex
	cache map[string][]*policyv1.PodDisruptionBudget // namespace -> PDBs
	ttl   time.Duration
	last  time.Time
}

// 글로벌 PDB 캐시
var globalPDBCache = &pdbCache{
	cache: make(map[string][]*policyv1.PodDisruptionBudget),
	ttl:   30 * time.Second, // 캐시 유효 시간
}

// DefaultEvictionConfig는 기본 설정값을 제공합니다
func DefaultEvictionConfig() *EvictionConfig {
	return &EvictionConfig{
		MaxConcurrentEvictions: 2, // 동시 eviction 감소
		MaxRetries:             3,
		RetryBackoffDuration:   10 * time.Second, // 재시도 간격 증가
		PodDeletionTimeout:     2 * time.Minute,
		CheckInterval:          15 * time.Second, // 상태 확인 간격 증가
	}
}

// EvictPods는 노드의 파드들을 안전하게 evict합니다
func EvictPods(clientSet kubernetes.Interface, nodeName string, config *EvictionConfig) error {
	if config == nil {
		config = DefaultEvictionConfig()
	}

	slog.Info("노드에서 pod evict 시작", "nodeName", nodeName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pods, err := GetNonCriticalPods(clientSet, nodeName)
	if err != nil {
		return fmt.Errorf("노드 %s 에서 데몬셋을 제외한 파드를 가져오는 중 오류가 발생했습니다.: %v", nodeName, err)
	}

	// 동시성 제한을 위한 세마포어
	semaphore := make(chan struct{}, config.MaxConcurrentEvictions)
	var wg sync.WaitGroup
	errChan := make(chan error, len(pods))

	for _, pod := range pods {
		wg.Add(1)
		go func(p coreV1.Pod) {
			defer wg.Done()
			semaphore <- struct{}{}        // 세마포어 획득
			defer func() { <-semaphore }() // 세마포어 해제

			if err := evictPodWithRetry(ctx, clientSet, p, config); err != nil {
				errChan <- fmt.Errorf("파드 %s evict 실패: %v", p.Name, err)
			}
		}(pod)
	}

	wg.Wait()
	close(errChan)

	// 에러 수집
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("일부 파드 evict 실패: %v", errs)
	}

	slog.Info("노드에서 pod evict 완료", "nodeName", nodeName)
	return nil
}

// evictPodWithRetry는 개별 파드에 대한 eviction을 재시도 로직과 함께 수행합니다
func evictPodWithRetry(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, config *EvictionConfig) error {
	var lastErr error
	for retry := 0; retry < config.MaxRetries; retry++ {
		if retry > 0 {
			time.Sleep(config.RetryBackoffDuration)
		}

		// PDB 체크
		if err := checkPDB(ctx, clientSet, pod); err != nil {
			lastErr = err
			slog.Warn("PDB 체크 실패, 재시도 예정",
				"pod", pod.Name,
				"retry", retry+1,
				"error", err)
			continue
		}

		// Eviction 요청
		if err := evictPod(ctx, clientSet, pod); err != nil {
			lastErr = err
			slog.Warn("Pod eviction 실패, 재시도 예정",
				"pod", pod.Name,
				"retry", retry+1,
				"error", err)
			continue
		}

		// 파드 종료 감시
		if err := waitForPodDeletion(ctx, clientSet, pod, config); err != nil {
			lastErr = err
			slog.Warn("Pod 삭제 대기 실패, 재시도 예정",
				"pod", pod.Name,
				"retry", retry+1,
				"error", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("최대 재시도 횟수 초과: %v", lastErr)
}

// checkPDB는 PodDisruptionBudget을 체크합니다
func checkPDB(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod) error {
	pdbs, err := getPDBsWithCache(ctx, clientSet, pod.Namespace)
	if err != nil {
		return fmt.Errorf("PDB 조회 실패: %v", err)
	}

	for _, pdb := range pdbs {
		selector, err := metaV1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			slog.ErrorContext(ctx, "PDB 레이블 선택자 변환 실패",
				"pdb", pdb.Name,
				"error", err)
			continue
		}
		if selector.Matches(labels.Set(pod.Labels)) {
			// 테스트에서는 Status.DisruptionsAllowed가 직접 설정될 수 있음
			if pdb.Status.DisruptionsAllowed < 1 {
				return fmt.Errorf("PDB %s에 의해 eviction이 제한됨 (현재 허용된 disruption: %d)",
					pdb.Name, pdb.Status.DisruptionsAllowed)
			}
			slog.InfoContext(ctx, "PDB 체크 통과",
				"pdb", pdb.Name,
				"allowedDisruptions", pdb.Status.DisruptionsAllowed)
		}
	}

	return nil
}

// getPDBsWithCache는 캐시된 PDB 정보를 반환하거나 API에서 가져옵니다
func getPDBsWithCache(ctx context.Context, clientSet kubernetes.Interface, namespace string) ([]*policyv1.PodDisruptionBudget, error) {
	globalPDBCache.RLock()
	now := time.Now()
	// 캐시가 유효한지 확인
	if pdbs, ok := globalPDBCache.cache[namespace]; ok && now.Sub(globalPDBCache.last) < globalPDBCache.ttl {
		globalPDBCache.RUnlock()
		return pdbs, nil
	}
	globalPDBCache.RUnlock()

	// 캐시가 없거나 만료된 경우 새로 조회
	globalPDBCache.Lock()
	defer globalPDBCache.Unlock()

	// 다른 고루틴이 이미 캐시를 업데이트했는지 다시 확인
	if pdbs, ok := globalPDBCache.cache[namespace]; ok && now.Sub(globalPDBCache.last) < globalPDBCache.ttl {
		return pdbs, nil
	}

	pdbList, err := clientSet.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var pdbs []*policyv1.PodDisruptionBudget
	for i := range pdbList.Items {
		pdbs = append(pdbs, &pdbList.Items[i])
	}

	// 캐시 업데이트
	globalPDBCache.cache[namespace] = pdbs
	globalPDBCache.last = time.Now()

	return pdbs, nil
}

// evictPod는 개별 파드에 대한 eviction을 수행합니다
func evictPod(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod) error {
	gracePeriod := int64(60)
	propagationPolicy := metaV1.DeletePropagationOrphan

	slog.Info("파드 eviction 시작", "pod", pod.Name)

	err := clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metaV1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	})

	// Pod가 이미 없는 경우 성공으로 처리
	if errors.IsNotFound(err) {
		slog.Info("파드가 이미 제거됨", "pod", pod.Name)
		return nil
	}

	return err
}

// waitForPodDeletion은 파드가 완전히 삭제될 때까지 대기합니다
func waitForPodDeletion(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, config *EvictionConfig) error {
	deadline := time.Now().Add(config.PodDeletionTimeout)

	// 파드 상태 첫 확인 시 NotFound이면 바로 성공 처리
	_, err := clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
	if errors.IsNotFound(err) {
		slog.Info("파드가 이미 제거됨", "pod", pod.Name)
		return nil // 파드가 이미 삭제됨
	}

	if err != nil {
		slog.Warn("파드 상태 확인 중 오류 발생, 일시적인 오류로 간주하고 계속 진행",
			"pod", pod.Name, "error", err)
		// 오류가 있더라도 일시적인 것으로 간주하고 계속 진행
	}

	for time.Now().Before(deadline) {
		_, err := clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
		if errors.IsNotFound(err) {
			slog.Info("파드 eviction 완료", "pod", pod.Name)
			return nil // 파드가 정상적으로 삭제됨
		}

		if err != nil {
			// API 서버 오류는 파드가 아직 있다고 가정하고 일시적인 오류로 간주
			slog.Warn("파드 상태 확인 중 일시적 오류, 계속 진행",
				"pod", pod.Name, "error", err)
		}

		time.Sleep(config.CheckInterval)
	}

	return fmt.Errorf("파드 삭제 타임아웃: %s", pod.Name)
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
