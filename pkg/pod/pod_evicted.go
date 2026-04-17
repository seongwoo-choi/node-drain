package pod

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	coreV1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type EvictionMode string

const (
	// EvictionModeEvict uses pod eviction subresource.
	EvictionModeEvict EvictionMode = "evict"
	// EvictionModeDelete deletes pod directly.
	EvictionModeDelete EvictionMode = "delete"
)

// EvictionConfig defines pod eviction and wait strategy.
type EvictionConfig struct {
	MaxConcurrentEvictions   int
	MaxRetries               int
	RetryBackoffDuration     time.Duration
	PodDeletionTimeout       time.Duration
	CheckInterval            time.Duration
	EvictionTimeout          time.Duration
	NodeTerminationTimeout   time.Duration
	NodeTerminationCheckTick time.Duration
	PostEvictionNodeDelay    time.Duration

	EvictionMode        EvictionMode
	Force               bool
	ForceProblemPods    bool
	PDBToken            bool
	PDBTokenMaxInFlight int
}

type pdbCache struct {
	sync.RWMutex
	cache map[string]pdbCacheEntry
	ttl   time.Duration
}

type pdbCacheEntry struct {
	pdbs []*policyv1.PodDisruptionBudget
	last time.Time
}

var globalPDBCache = &pdbCache{
	cache: make(map[string]pdbCacheEntry),
	ttl:   30 * time.Second,
}

// DefaultEvictionConfig returns safe defaults for eviction workflows.
func DefaultEvictionConfig() *EvictionConfig {
	return &EvictionConfig{
		MaxConcurrentEvictions:   30,
		MaxRetries:               3,
		RetryBackoffDuration:     10 * time.Second,
		PodDeletionTimeout:       2 * time.Minute,
		CheckInterval:            20 * time.Second,
		EvictionTimeout:          10 * time.Minute,
		NodeTerminationTimeout:   10 * time.Minute,
		NodeTerminationCheckTick: 15 * time.Second,
		PostEvictionNodeDelay:    50 * time.Second,
		EvictionMode:             EvictionModeEvict,
		Force:                    false,
		ForceProblemPods:         true,
		PDBToken:                 true,
		PDBTokenMaxInFlight:      1,
	}
}

// EvictPods evicts non-critical pods from a node with retry and concurrency control.
func EvictPods(ctx context.Context, clientSet kubernetes.Interface, nodeName string, cfg *EvictionConfig) error {
	cfg = normalizeEvictionConfig(cfg)
	if ctx == nil {
		ctx = context.Background()
	}

	if cfg.EvictionTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.EvictionTimeout)
		defer cancel()
	}

	slog.Info("노드에서 pod evict 시작", "nodeName", nodeName)

	pods, err := GetNonCriticalPods(ctx, clientSet, nodeName)
	if err != nil {
		return fmt.Errorf("노드 %s 데몬셋 제외 파드 조회 실패: %w", nodeName, err)
	}

	var normalPods []coreV1.Pod
	var problemPods []coreV1.Pod
	for _, p := range pods {
		podObj, getErr := clientSet.CoreV1().Pods(p.Namespace).Get(ctx, p.Name, metaV1.GetOptions{})
		if getErr == nil && cfg.ForceProblemPods && isPodInProblemState(podObj) {
			problemPods = append(problemPods, p)
			continue
		}
		normalPods = append(normalPods, p)
	}

	semaphore := make(chan struct{}, cfg.MaxConcurrentEvictions)
	var wg sync.WaitGroup
	errChan := make(chan error, len(normalPods))

	for _, p := range normalPods {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			case semaphore <- struct{}{}:
			}
			defer func() { <-semaphore }()

			if evictErr := evictPodWithRetry(ctx, clientSet, p, cfg); evictErr != nil {
				errChan <- fmt.Errorf("파드 %s eviction 실패: %w", p.Name, evictErr)
			}
		}()
	}

	wg.Wait()
	close(errChan)

	var errs []error
	for evictErr := range errChan {
		if evictErr != nil && evictErr != context.Canceled && evictErr != context.DeadlineExceeded {
			errs = append(errs, evictErr)
		}
	}

	if len(problemPods) > 0 {
		slog.Info("문제 상태의 파드 강제 제거 시작", "nodeName", nodeName, "count", len(problemPods))
		for _, p := range problemPods {
			gracePeriod := int64(0)
			delErr := clientSet.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metaV1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			if delErr != nil && !apierrors.IsNotFound(delErr) {
				errs = append(errs, fmt.Errorf("문제 파드 %s 강제 제거 실패: %w", p.Name, delErr))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("일부 파드 eviction 실패: %v", errs)
	}

	slog.Info("노드에서 pod evict 완료", "nodeName", nodeName)
	return nil
}

func normalizeEvictionConfig(cfg *EvictionConfig) *EvictionConfig {
	defaults := DefaultEvictionConfig()
	if cfg == nil {
		return defaults
	}

	normalized := *cfg
	if normalized.MaxConcurrentEvictions <= 0 {
		normalized.MaxConcurrentEvictions = defaults.MaxConcurrentEvictions
	}
	if normalized.MaxRetries <= 0 {
		normalized.MaxRetries = defaults.MaxRetries
	}
	if normalized.RetryBackoffDuration <= 0 {
		normalized.RetryBackoffDuration = defaults.RetryBackoffDuration
	}
	if normalized.PodDeletionTimeout <= 0 {
		normalized.PodDeletionTimeout = defaults.PodDeletionTimeout
	}
	if normalized.CheckInterval <= 0 {
		normalized.CheckInterval = defaults.CheckInterval
	}
	if normalized.EvictionTimeout <= 0 {
		normalized.EvictionTimeout = defaults.EvictionTimeout
	}
	if normalized.NodeTerminationTimeout <= 0 {
		normalized.NodeTerminationTimeout = defaults.NodeTerminationTimeout
	}
	if normalized.NodeTerminationCheckTick <= 0 {
		normalized.NodeTerminationCheckTick = defaults.NodeTerminationCheckTick
	}
	if normalized.PostEvictionNodeDelay < 0 {
		normalized.PostEvictionNodeDelay = 0
	}
	if normalized.EvictionMode != EvictionModeEvict && normalized.EvictionMode != EvictionModeDelete {
		normalized.EvictionMode = defaults.EvictionMode
	}
	if normalized.PDBTokenMaxInFlight <= 0 {
		normalized.PDBTokenMaxInFlight = defaults.PDBTokenMaxInFlight
	}
	return &normalized
}

func evictPodWithRetry(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, cfg *EvictionConfig) error {
	var lastErr error

	if cfg.PDBToken {
		podObj, err := clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
		if err == nil && podObj != nil {
			pdbKeys, keyErr := getMatchingPDBKeys(ctx, clientSet, podObj)
			if keyErr != nil {
				slog.Warn("PDB 키 계산 실패(토큰 미적용)", "pod", pod.Name, "error", keyErr)
			} else {
				release := acquirePDBTokens(pdbKeys, cfg.PDBTokenMaxInFlight)
				defer release()
			}
		}
	}

	for retry := 0; retry < cfg.MaxRetries; retry++ {
		if retry > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.RetryBackoffDuration):
			}
		}

		if cfg.EvictionMode != EvictionModeDelete {
			if err := checkPDB(ctx, clientSet, pod); err != nil {
				lastErr = err
				slog.Warn("PDB 체크 실패, 재시도 예정", "pod", pod.Name, "retry", retry+1, "error", err)
				continue
			}
		}

		if err := evictPod(ctx, clientSet, pod, cfg); err != nil {
			lastErr = err
			if cfg.EvictionMode == EvictionModeEvict && cfg.Force {
				slog.Warn("eviction 실패로 delete 강제 전환", "pod", pod.Name, "retry", retry+1, "error", err)
				forceErr := fallbackDeletePod(ctx, clientSet, pod, int64(0), metaV1.DeletePropagationBackground)
				if forceErr == nil {
					return nil
				}
				lastErr = fmt.Errorf("eviction 실패(%v), delete 강제 전환 실패(%w)", err, forceErr)
			}
			slog.Warn("Pod eviction 실패, 재시도 예정", "pod", pod.Name, "retry", retry+1, "error", err)
			continue
		}

		if err := waitForPodDeletion(ctx, clientSet, pod, cfg); err != nil {
			lastErr = err
			slog.Warn("Pod 삭제 대기 실패, 재시도 예정", "pod", pod.Name, "retry", retry+1, "error", err)
			continue
		}

		return nil
	}

	return fmt.Errorf("최대 재시도 횟수 초과: %w", lastErr)
}

func checkPDB(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod) error {
	pdbs, err := getPDBsWithCache(ctx, clientSet, pod.Namespace)
	if err != nil {
		return fmt.Errorf("PDB 조회 실패: %w", err)
	}

	for _, pdb := range pdbs {
		selector, selectorErr := metaV1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if selectorErr != nil {
			slog.ErrorContext(ctx, "PDB 레이블 선택자 변환 실패", "pdb", pdb.Name, "error", selectorErr)
			continue
		}
		if selector.Matches(labels.Set(pod.Labels)) && pdb.Status.DisruptionsAllowed < 1 {
			return fmt.Errorf("PDB %s 에 의해 eviction 제한됨 (허용 disruption: %d)", pdb.Name, pdb.Status.DisruptionsAllowed)
		}
	}

	return nil
}

func getPDBsWithCache(ctx context.Context, clientSet kubernetes.Interface, namespace string) ([]*policyv1.PodDisruptionBudget, error) {
	globalPDBCache.RLock()
	now := time.Now()
	if entry, ok := globalPDBCache.cache[namespace]; ok && now.Sub(entry.last) < globalPDBCache.ttl {
		globalPDBCache.RUnlock()
		return entry.pdbs, nil
	}
	globalPDBCache.RUnlock()

	globalPDBCache.Lock()
	defer globalPDBCache.Unlock()

	now = time.Now()
	if entry, ok := globalPDBCache.cache[namespace]; ok && now.Sub(entry.last) < globalPDBCache.ttl {
		return entry.pdbs, nil
	}

	pdbList, err := clientSet.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pdbs := make([]*policyv1.PodDisruptionBudget, 0, len(pdbList.Items))
	for i := range pdbList.Items {
		pdbs = append(pdbs, &pdbList.Items[i])
	}

	globalPDBCache.cache[namespace] = pdbCacheEntry{
		pdbs: pdbs,
		last: time.Now(),
	}

	return pdbs, nil
}

func evictPod(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, cfg *EvictionConfig) error {
	podObj, err := clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
	if apierrors.IsNotFound(err) {
		slog.Info("파드가 이미 제거됨", "pod", pod.Name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("파드 상태 조회 실패: %w", err)
	}

	gracePeriod := int64(60)
	if isPodInProblemState(podObj) {
		gracePeriod = int64(0)
		slog.Info("문제 상태 파드 강제 삭제", "pod", pod.Name, "status", podObj.Status.Phase)
	}

	propagationPolicy := metaV1.DeletePropagationOrphan

	if cfg.EvictionMode == EvictionModeDelete {
		return fallbackDeletePod(ctx, clientSet, pod, gracePeriod, propagationPolicy)
	}

	eviction := &policyv1.Eviction{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		DeleteOptions: &metaV1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
			PropagationPolicy:  &propagationPolicy,
		},
	}

	slog.Info("파드 eviction 시작", "pod", pod.Name)
	if err = clientSet.CoreV1().Pods(pod.Namespace).EvictV1(ctx, eviction); err != nil {
		if apierrors.IsNotFound(err) {
			slog.Info("파드가 이미 제거됨", "pod", pod.Name)
			return nil
		}

		if shouldFallbackToDelete(err) {
			return fallbackDeletePod(ctx, clientSet, pod, gracePeriod, propagationPolicy)
		}
		return err
	}

	// Eviction accepted after PDB checks; issue a best-effort delete so fake clients and
	// non-standard API servers converge quickly.
	deleteErr := clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metaV1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	})
	if deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
		slog.Warn("eviction 이후 delete 보정 실패", "pod", pod.Name, "error", deleteErr)
	}
	return nil
}

func shouldFallbackToDelete(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsMethodNotSupported(err) || apierrors.IsNotFound(err) {
		return true
	}

	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "could not find the requested resource") ||
		strings.Contains(errStr, "evictions") && strings.Contains(errStr, "not found")
}

func fallbackDeletePod(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, gracePeriod int64, propagationPolicy metaV1.DeletionPropagation) error {
	slog.Warn("eviction API 미지원으로 delete fallback 수행", "pod", pod.Name)
	err := clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metaV1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func getMatchingPDBKeys(ctx context.Context, clientSet kubernetes.Interface, podObj *coreV1.Pod) ([]string, error) {
	pdbs, err := getPDBsWithCache(ctx, clientSet, podObj.Namespace)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(pdbs))
	for _, pdb := range pdbs {
		selector, selectorErr := metaV1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if selectorErr != nil {
			continue
		}
		if selector.Matches(labels.Set(podObj.Labels)) {
			keys = append(keys, fmt.Sprintf("%s/%s", podObj.Namespace, pdb.Name))
		}
	}
	return keys, nil
}

func isPodInProblemState(pod *coreV1.Pod) bool {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.State.Waiting == nil {
			continue
		}
		reason := containerStatus.State.Waiting.Reason
		if reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "CrashLoopBackOff" {
			return true
		}
	}

	if pod.Status.Phase == coreV1.PodPending && time.Since(pod.CreationTimestamp.Time) > 10*time.Minute {
		return true
	}
	return false
}

func waitForPodDeletion(ctx context.Context, clientSet kubernetes.Interface, pod coreV1.Pod, cfg *EvictionConfig) error {
	timeoutMultiplier := 1.0
	if isBatchJob(pod) {
		timeoutMultiplier = 1.5
	}

	timeout := time.Duration(float64(cfg.PodDeletionTimeout) * timeoutMultiplier)
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	_, err := clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
	if apierrors.IsNotFound(err) {
		slog.Info("파드가 이미 제거됨", "pod", pod.Name)
		return nil
	}
	if err != nil {
		slog.Warn("파드 상태 확인 중 오류 발생, 계속 진행", "pod", pod.Name, "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutTimer.C:
			_, err = clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil && isRateLimitError(err) {
				slog.Warn("rate limit으로 인해 파드 상태 확인 불가, 파드 삭제로 간주", "pod", pod.Name, "error", err)
				return nil
			}
			return fmt.Errorf("파드 삭제 타임아웃: %s", pod.Name)
		case <-ticker.C:
			_, err = clientSet.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metaV1.GetOptions{})
			if apierrors.IsNotFound(err) {
				slog.Info("파드 eviction 완료", "pod", pod.Name)
				return nil
			}
			if err != nil {
				slog.Warn("파드 상태 확인 중 일시적 오류", "pod", pod.Name, "error", err)
			}
		}
	}
}

func isBatchJob(pod coreV1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "Job" || ref.Kind == "CronJob" {
			return true
		}
	}

	podName := strings.ToLower(pod.Name)
	return strings.Contains(podName, "job") ||
		strings.Contains(podName, "cron") ||
		strings.Contains(podName, "batch")
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "too many requests")
}

// GetNonCriticalPods lists pods on a node excluding daemonset and completed/failed pods.
func GetNonCriticalPods(ctx context.Context, clientSet kubernetes.Interface, nodeName string) ([]coreV1.Pod, error) {
	podList, err := clientSet.CoreV1().Pods("").List(ctx, metaV1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase!=Succeeded,status.phase!=Failed", nodeName),
	})
	if err != nil {
		return nil, fmt.Errorf("%s 노드에서 파드 리스트 조회 실패: %w", nodeName, err)
	}

	pods := make([]coreV1.Pod, 0, len(podList.Items))
	for _, p := range podList.Items {
		if !isManagedByDaemonSet(p) {
			pods = append(pods, p)
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
