package cmd

import (
	"app/config"
	"app/pkg/karpenter"
	"app/pkg/node"
	"app/pkg/notification"
	"app/pkg/pod"
	"app/types"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	coordinationV1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	drainPolicy                string
	drainRounding              string
	drainMin                   int
	drainMaxAbsolute           int
	drainMaxFraction           float64
	drainStepRules             string
	drainSafetyMaxAllocateRate int
	drainSafetyQueries         string
	drainSafetyFailClosed      bool
	drainProgressive           bool
	drainDryRun                bool
	drainLockMode              string
	drainLockNamespace         string
	drainLockLeaseDuration     string
	drainNodeSelectionStrategy string
	drainSkipUnschedulable     bool
	drainOutputFormat          string

	podEvictionMode        string
	podForce               bool
	podForceProblemPods    bool
	podDeleteAfterEviction bool
	podPDBToken            bool
	podPDBTokenMaxInFlight int
	podMaxConcurrent       int
	podMaxRetries          int
	podRetryBackoff        string
	podDeletionTimeout     string
	podCheckInterval       string
)

var drainCmd = &cobra.Command{
	Use:   "drain",
	Short: "노드 드레인 실행",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		applyRootFlagEnv(command)
		applyDrainFlagEnv(command)

		if err := validateRequiredEnvValues(
			requiredEnvValue{flagName: "prometheus-address", envKey: "PROMETHEUS_ADDRESS"},
			requiredEnvValue{flagName: "cluster-name", envKey: "CLUSTER_NAME"},
			requiredEnvValue{flagName: "nodepool-name", envKey: "NODEPOOL_NAME"},
		); err != nil {
			return err
		}
		if err := validateDrainCommandConfig(); err != nil {
			return err
		}

		ctx := command.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		kubeConfigMode := os.Getenv("KUBE_CONFIG")
		kubeConfigPath := os.Getenv("KUBECONFIG")

		clientSet, err := config.GetKubeClientSet(kubeConfigMode, kubeConfigPath)
		if err != nil {
			slog.Error("쿠버네티스 클라이언트 생성 실패", "error", err)
			return fmt.Errorf("쿠버네티스 클라이언트 생성 실패: %w", err)
		}

		if !drainDryRun {
			lock, err := acquireDrainRunLockFromEnv(ctx, clientSet, drainLockMode, drainLockNamespace, drainLockLeaseDuration)
			if err != nil {
				slog.Error("드레인 중복 실행 차단", "error", err)
				return err
			}
			defer releaseDrainRunLock(ctx, lock)
		}

		return handleNodeDrain(ctx, clientSet)
	},
}

func handleNodeDrain(ctx context.Context, clientSet kubernetes.Interface) error {
	slog.Info("노드 드레인 커맨드를 실행합니다.")

	prometheusClient, err := config.CreatePrometheusClient()
	if err != nil {
		slog.Error("Prometheus 클라이언트 생성 실패", "error", err)
		return fmt.Errorf("Prometheus 클라이언트 생성 실패: %w", err)
	}

	nodepool := os.Getenv("NODEPOOL_NAME")
	metricsQuerier := karpenter.NewPrometheusQuerier(prometheusClient)
	karpenterClient := karpenter.NewClientForCluster(nodepool, os.Getenv("CLUSTER_NAME"), metricsQuerier)
	notifier := notification.NewSlackNotifier(notification.SlackConfig{
		WebhookURL:   os.Getenv("SLACK_WEBHOOK_URL"),
		ClusterName:  os.Getenv("CLUSTER_NAME"),
		NodepoolName: nodepool,
	})

	drainConfig := node.DefaultDrainConfig(nodepool)
	drainConfig.Eviction = pod.GetEvictionConfigFromEnv()
	drainConfig.DryRun = drainDryRun
	drainConfig.NodeSelectionStrategy = node.DrainNodeSelectionStrategy(drainNodeSelectionStrategy)
	drainConfig.SkipUnschedulable = drainSkipUnschedulable

	report, err := node.NodeDrainWithReport(ctx, clientSet, node.DrainDependencies{
		AllocateRateProvider: karpenterClient,
		Notifier:             notifier,
	}, drainConfig)
	if err != nil {
		slog.Error("노드 드레인 실패", "error", err)
		if notifyErr := notifier.SendNodeDrainErrorWithSummary(ctx, err, report.Summary); notifyErr != nil {
			slog.Error("슬랙 알림 전송 실패", "error", notifyErr)
		}
		if outputErr := writeNodeDrainReport(os.Stdout, report, drainOutputFormat); outputErr != nil {
			slog.Error("드레인 결과 출력 실패", "error", outputErr)
		}
		return err
	}

	if err = notifier.SendNodeDrainCompleteWithSummary(ctx, report.Results, report.Summary); err != nil {
		slog.Error("슬랙 알림 전송 실패", "error", err)
	}
	if err = writeNodeDrainReport(os.Stdout, report, drainOutputFormat); err != nil {
		return fmt.Errorf("드레인 결과 출력 실패: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(drainCmd)

	drainCmd.Flags().StringVar(&drainPolicy, "drain-policy", "formula", "드레인 정책 (formula|step)")
	drainCmd.Flags().StringVar(&drainRounding, "drain-rounding", "floor", "드레인 계산 라운딩 (floor|round|ceil)")
	drainCmd.Flags().IntVar(&drainMin, "drain-min", 0, "드레인 최소 노드 수 (0이면 비활성)")
	drainCmd.Flags().IntVar(&drainMaxAbsolute, "drain-max-absolute", 0, "드레인 최대 노드 수(절대값, 0이면 비활성)")
	drainCmd.Flags().Float64Var(&drainMaxFraction, "drain-max-fraction", 0, "드레인 최대 비율(예: 0.2=최대 20%, 0이면 비활성)")
	drainCmd.Flags().StringVar(&drainStepRules, "drain-step-rules", "", "계단식 정책 규칙 (예: \"80:1,60:2\")")
	drainCmd.Flags().IntVar(&drainSafetyMaxAllocateRate, "drain-safety-max-allocate-rate", 0, "안전 조건: maxAllocateRate가 이 값 이상이면 0대로 강제 (0이면 비활성)")
	drainCmd.Flags().StringVar(&drainSafetyQueries, "drain-safety-queries", "", "안전 조건 PromQL(세미콜론/개행 구분). 하나라도 결과가 >0이면 0대로 강제")
	drainCmd.Flags().BoolVar(&drainSafetyFailClosed, "drain-safety-fail-closed", true, "안전 조건 쿼리 실패 시 0대로 강제할지 여부")
	drainCmd.Flags().BoolVar(&drainProgressive, "drain-progressive", true, "점진적 드레인: 노드 1대 처리 후 안전 조건 재평가")
	drainCmd.Flags().BoolVar(&drainDryRun, "dry-run", false, "실제 cordon/evict 없이 드레인 대상 노드와 파드만 계산")
	drainCmd.Flags().StringVar(&drainLockMode, "drain-lock-mode", "local", "중복 실행 lock 방식 (local|kubernetes|none)")
	drainCmd.Flags().StringVar(&drainLockNamespace, "drain-lock-namespace", "kube-system", "kubernetes lock Lease를 저장할 namespace")
	drainCmd.Flags().StringVar(&drainLockLeaseDuration, "drain-lock-lease-duration", "10m", "kubernetes lock Lease 만료 시간")
	drainCmd.Flags().StringVar(&drainNodeSelectionStrategy, "drain-node-selection", string(node.DrainNodeSelectionOldest), "드레인 대상 노드 선택 전략 (oldest|empty-first|least-pods|most-pods)")
	drainCmd.Flags().BoolVar(&drainSkipUnschedulable, "drain-skip-unschedulable", false, "이미 cordon된 노드를 새 드레인 대상으로 선택하지 않음")
	drainCmd.Flags().StringVar(&drainOutputFormat, "output", "text", "결과 출력 형식 (text|json)")

	drainCmd.Flags().StringVar(&podEvictionMode, "pod-eviction-mode", "evict", "파드 제거 방식 (evict|delete)")
	drainCmd.Flags().BoolVar(&podForce, "force", false, "eviction 반복 실패/타임아웃 시 delete 강제 전환 여부")
	drainCmd.Flags().BoolVar(&podForceProblemPods, "force-problem-pods", true, "문제 파드를 즉시 delete(grace=0)로 처리할지 여부")
	drainCmd.Flags().BoolVar(&podDeleteAfterEviction, "pod-delete-after-eviction", false, "eviction 성공 후 delete 보정을 수행할지 여부")
	drainCmd.Flags().BoolVar(&podPDBToken, "pdb-token", true, "같은 PDB에 매칭되는 파드 동시 처리 제한 여부")
	drainCmd.Flags().IntVar(&podPDBTokenMaxInFlight, "pdb-token-max-in-flight", 1, "같은 PDB 토큰 동시 처리 개수")
	drainCmd.Flags().IntVar(&podMaxConcurrent, "pod-max-concurrent", 30, "동시 제거 Pod 최대 개수")
	drainCmd.Flags().IntVar(&podMaxRetries, "pod-max-retries", 3, "Pod 제거 최대 재시도 횟수")
	drainCmd.Flags().StringVar(&podRetryBackoff, "pod-retry-backoff", "10s", "Pod 제거 재시도 간격")
	drainCmd.Flags().StringVar(&podDeletionTimeout, "pod-deletion-timeout", "2m", "Pod 삭제 대기 타임아웃")
	drainCmd.Flags().StringVar(&podCheckInterval, "pod-check-interval", "20s", "Pod 삭제 상태 확인 주기")
}

func validateDrainCommandConfig() error {
	if err := node.ValidateDrainPolicyEnv(); err != nil {
		return err
	}
	if err := pod.ValidateEvictionConfigEnv(); err != nil {
		return err
	}
	if _, err := parseDrainOutputFormat(drainOutputFormat); err != nil {
		return err
	}
	if err := validateDrainNodeSelectionStrategy(drainNodeSelectionStrategy); err != nil {
		return err
	}
	if err := validateDrainLockMode(drainLockMode); err != nil {
		return err
	}
	if err := validatePositiveDurationString("drain-lock-lease-duration", drainLockLeaseDuration); err != nil {
		return err
	}
	return nil
}

func validateDrainNodeSelectionStrategy(strategy string) error {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", string(node.DrainNodeSelectionOldest), string(node.DrainNodeSelectionEmptyFirst), string(node.DrainNodeSelectionLeastPods), string(node.DrainNodeSelectionMostPods):
		return nil
	default:
		return fmt.Errorf("지원하지 않는 drain-node-selection: %s", strategy)
	}
}

func validateDrainLockMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "local", "kubernetes", "none":
		return nil
	default:
		return fmt.Errorf("지원하지 않는 drain-lock-mode: %s", mode)
	}
}

func validatePositiveDurationString(flagName string, value string) error {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid %s: %w", flagName, err)
	}
	if duration <= 0 {
		return fmt.Errorf("invalid %s: must be greater than 0", flagName)
	}
	return nil
}

func parseDrainOutputFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("지원하지 않는 output 형식: %s", format)
	}
}

func applyDrainFlagEnv(command *cobra.Command) {
	setEnvFromChangedFlag(command, "drain-policy", "DRAIN_POLICY", drainPolicy)
	setEnvFromChangedFlag(command, "drain-rounding", "DRAIN_ROUNDING", drainRounding)
	setEnvFromChangedFlag(command, "drain-min", "DRAIN_MIN", fmt.Sprintf("%d", drainMin))
	setEnvFromChangedFlag(command, "drain-max-absolute", "DRAIN_MAX_ABSOLUTE", fmt.Sprintf("%d", drainMaxAbsolute))
	setEnvFromChangedFlag(command, "drain-max-fraction", "DRAIN_MAX_FRACTION", fmt.Sprintf("%g", drainMaxFraction))
	setEnvFromChangedFlag(command, "drain-step-rules", "DRAIN_STEP_RULES", drainStepRules)
	setEnvFromChangedFlag(command, "drain-safety-max-allocate-rate", "DRAIN_SAFETY_MAX_ALLOCATE_RATE", fmt.Sprintf("%d", drainSafetyMaxAllocateRate))
	setEnvFromChangedFlag(command, "drain-safety-queries", "DRAIN_SAFETY_QUERIES", drainSafetyQueries)
	setEnvFromChangedFlag(command, "drain-safety-fail-closed", "DRAIN_SAFETY_FAIL_CLOSED", fmt.Sprintf("%t", drainSafetyFailClosed))
	setEnvFromChangedFlag(command, "drain-progressive", "DRAIN_PROGRESSIVE", fmt.Sprintf("%t", drainProgressive))

	setEnvFromChangedFlag(command, "pod-eviction-mode", "POD_EVICTION_MODE", podEvictionMode)
	setEnvFromChangedFlag(command, "force", "POD_FORCE", fmt.Sprintf("%t", podForce))
	setEnvFromChangedFlag(command, "force-problem-pods", "POD_FORCE_PROBLEM_PODS", fmt.Sprintf("%t", podForceProblemPods))
	setEnvFromChangedFlag(command, "pod-delete-after-eviction", "POD_DELETE_AFTER_EVICTION", fmt.Sprintf("%t", podDeleteAfterEviction))
	setEnvFromChangedFlag(command, "pdb-token", "POD_PDB_TOKEN", fmt.Sprintf("%t", podPDBToken))
	setEnvFromChangedFlag(command, "pdb-token-max-in-flight", "POD_PDB_TOKEN_MAX_IN_FLIGHT", fmt.Sprintf("%d", podPDBTokenMaxInFlight))
	setEnvFromChangedFlag(command, "pod-max-concurrent", "POD_MAX_CONCURRENT", fmt.Sprintf("%d", podMaxConcurrent))
	setEnvFromChangedFlag(command, "pod-max-retries", "POD_MAX_RETRIES", fmt.Sprintf("%d", podMaxRetries))
	setEnvFromChangedFlag(command, "pod-retry-backoff", "POD_RETRY_BACKOFF", podRetryBackoff)
	setEnvFromChangedFlag(command, "pod-deletion-timeout", "POD_DELETION_TIMEOUT", podDeletionTimeout)
	setEnvFromChangedFlag(command, "pod-check-interval", "POD_CHECK_INTERVAL", podCheckInterval)
}

func writeNodeDrainReport(w io.Writer, report types.NodeDrainReport, outputFormat string) error {
	format, err := parseDrainOutputFormat(outputFormat)
	if err != nil {
		return err
	}
	if format != "json" {
		return nil
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

type drainRunLock interface {
	Release(ctx context.Context) error
}

type noopDrainRunLock struct{}

func (noopDrainRunLock) Release(ctx context.Context) error {
	return nil
}

type localDrainRunLock struct {
	file *os.File
}

func acquireDrainRunLock(ctx context.Context, clientSet kubernetes.Interface, clusterName string, nodepoolName string, mode string, namespace string, leaseDuration string) (drainRunLock, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "local":
		return acquireLocalDrainRunLock(clusterName, nodepoolName)
	case "kubernetes":
		return acquireKubernetesDrainRunLock(ctx, clientSet, clusterName, nodepoolName, namespace, leaseDuration)
	case "none":
		return noopDrainRunLock{}, nil
	default:
		return nil, fmt.Errorf("지원하지 않는 drain lock mode: %s", mode)
	}
}

func acquireLocalDrainRunLock(clusterName string, nodepoolName string) (*localDrainRunLock, error) {
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf(
		"node-drain-%s-%s.lock",
		sanitizeLockName(clusterName),
		sanitizeLockName(nodepoolName),
	))

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("드레인 lock 파일 생성 실패: %w", err)
	}

	if err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("이미 동일 cluster/nodepool 드레인이 실행 중입니다: %s", lockPath)
	}

	return &localDrainRunLock{file: lockFile}, nil
}

func acquireDrainRunLockFromEnv(ctx context.Context, clientSet kubernetes.Interface, mode string, namespace string, leaseDuration string) (drainRunLock, error) {
	cluster := strings.TrimSpace(os.Getenv("CLUSTER_NAME"))
	nodepool := strings.TrimSpace(os.Getenv("NODEPOOL_NAME"))
	return acquireDrainRunLock(ctx, clientSet, cluster, nodepool, mode, namespace, leaseDuration)
}

func releaseDrainRunLock(_ context.Context, lock drainRunLock) {
	if lock == nil {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lock.Release(releaseCtx); err != nil {
		slog.Warn("드레인 lock 해제 실패", "error", err)
	}
}

func (l *localDrainRunLock) Release(ctx context.Context) error {
	if l == nil || l.file == nil {
		return nil
	}
	lockFile := l.file
	l.file = nil
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	if err := lockFile.Close(); err != nil {
		slog.Warn("드레인 lock 파일 닫기 실패", "error", err)
	}
	return nil
}

func sanitizeLockName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

type kubernetesLeaseLock struct {
	clientSet  kubernetes.Interface
	namespace  string
	name       string
	holder     string
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
	releaseErr error
}

func acquireKubernetesDrainRunLock(ctx context.Context, clientSet kubernetes.Interface, clusterName string, nodepoolName string, namespace string, leaseDuration string) (*kubernetesLeaseLock, error) {
	if clientSet == nil {
		return nil, fmt.Errorf("kubernetes client is required for kubernetes drain lock")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "kube-system"
	}

	duration, err := time.ParseDuration(strings.TrimSpace(leaseDuration))
	if err != nil || duration <= 0 {
		return nil, fmt.Errorf("invalid drain lock lease duration: %s", leaseDuration)
	}

	durationSeconds := int32(duration.Seconds())
	if durationSeconds <= 0 {
		durationSeconds = 1
	}

	holder := drainLockHolderIdentity()
	leaseName := kubernetesDrainLockLeaseName(clusterName, nodepoolName)
	leases := clientSet.CoordinationV1().Leases(namespace)

	for attempt := 0; attempt < 3; attempt++ {
		now := metaV1.NewMicroTime(time.Now())
		lease, getErr := leases.Get(ctx, leaseName, metaV1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			lease = &coordinationV1.Lease{
				ObjectMeta: metaV1.ObjectMeta{
					Name:      leaseName,
					Namespace: namespace,
					Labels: map[string]string{
						"app.kubernetes.io/name": "node-drain",
						"node-drain/cluster":     sanitizeKubernetesLabelValue(clusterName),
						"node-drain/nodepool":    sanitizeKubernetesLabelValue(nodepoolName),
					},
				},
				Spec: coordinationV1.LeaseSpec{
					HolderIdentity:       &holder,
					LeaseDurationSeconds: &durationSeconds,
					AcquireTime:          &now,
					RenewTime:            &now,
				},
			}
			if _, createErr := leases.Create(ctx, lease, metaV1.CreateOptions{}); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					continue
				}
				return nil, fmt.Errorf("drain lease 생성 실패: %w", createErr)
			}
			lock := newKubernetesLeaseLock(clientSet, namespace, leaseName, holder)
			lock.startRenewal(duration)
			return lock, nil
		}
		if getErr != nil {
			return nil, fmt.Errorf("drain lease 조회 실패: %w", getErr)
		}
		if !isLeaseExpired(lease, time.Now()) && lease.Spec.HolderIdentity != nil {
			return nil, fmt.Errorf("이미 동일 cluster/nodepool 드레인이 실행 중입니다: lease %s/%s holder=%s", namespace, leaseName, *lease.Spec.HolderIdentity)
		}

		lease.Spec.HolderIdentity = &holder
		lease.Spec.LeaseDurationSeconds = &durationSeconds
		if lease.Spec.AcquireTime == nil || isLeaseExpired(lease, time.Now()) {
			lease.Spec.AcquireTime = &now
		}
		lease.Spec.RenewTime = &now
		if _, updateErr := leases.Update(ctx, lease, metaV1.UpdateOptions{}); updateErr != nil {
			if apierrors.IsConflict(updateErr) {
				continue
			}
			return nil, fmt.Errorf("drain lease 갱신 실패: %w", updateErr)
		}
		lock := newKubernetesLeaseLock(clientSet, namespace, leaseName, holder)
		lock.startRenewal(duration)
		return lock, nil
	}

	return nil, fmt.Errorf("drain lease 획득 재시도 초과: %s/%s", namespace, leaseName)
}

func newKubernetesLeaseLock(clientSet kubernetes.Interface, namespace string, name string, holder string) *kubernetesLeaseLock {
	return &kubernetesLeaseLock{
		clientSet: clientSet,
		namespace: namespace,
		name:      name,
		holder:    holder,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (l *kubernetesLeaseLock) startRenewal(duration time.Duration) {
	interval := duration / 3
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	go func() {
		defer close(l.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-ticker.C:
				if err := l.renew(context.Background(), duration); err != nil {
					slog.Warn("drain lease 갱신 실패", "namespace", l.namespace, "name", l.name, "error", err)
				}
			}
		}
	}()
}

func (l *kubernetesLeaseLock) renew(ctx context.Context, duration time.Duration) error {
	leases := l.clientSet.CoordinationV1().Leases(l.namespace)
	lease, err := leases.Get(ctx, l.name, metaV1.GetOptions{})
	if err != nil {
		return err
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != l.holder {
		return fmt.Errorf("lease holder changed")
	}
	durationSeconds := int32(duration.Seconds())
	now := metaV1.NewMicroTime(time.Now())
	lease.Spec.LeaseDurationSeconds = &durationSeconds
	lease.Spec.RenewTime = &now
	_, err = leases.Update(ctx, lease, metaV1.UpdateOptions{})
	return err
}

func (l *kubernetesLeaseLock) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		close(l.stop)
		<-l.done

		leases := l.clientSet.CoordinationV1().Leases(l.namespace)
		lease, err := leases.Get(ctx, l.name, metaV1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		if err != nil {
			l.releaseErr = err
			return
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != l.holder {
			return
		}
		err = leases.Delete(ctx, l.name, metaV1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			l.releaseErr = err
		}
	})
	return l.releaseErr
}

func isLeaseExpired(lease *coordinationV1.Lease, now time.Time) bool {
	if lease == nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return true
	}
	if lease.Spec.RenewTime == nil {
		return true
	}
	leaseDurationSeconds := int32(0)
	if lease.Spec.LeaseDurationSeconds != nil {
		leaseDurationSeconds = *lease.Spec.LeaseDurationSeconds
	}
	if leaseDurationSeconds <= 0 {
		return true
	}
	return now.Sub(lease.Spec.RenewTime.Time) > time.Duration(leaseDurationSeconds)*time.Second
}

func drainLockHolderIdentity() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func kubernetesDrainLockLeaseName(clusterName string, nodepoolName string) string {
	raw := fmt.Sprintf("node-drain-%s-%s", clusterName, nodepoolName)
	name := sanitizeKubernetesName(raw)
	if len(name) <= 253 {
		return name
	}
	sum := sha1.Sum([]byte(raw))
	suffix := hex.EncodeToString(sum[:])
	return strings.Trim(name[:212], "-") + "-" + suffix
}

func sanitizeKubernetesName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "node-drain"
	}
	return out
}

func sanitizeKubernetesLabelValue(value string) string {
	value = sanitizeKubernetesName(value)
	if len(value) <= 63 {
		return value
	}
	sum := sha1.Sum([]byte(value))
	suffix := hex.EncodeToString(sum[:])[:8]
	return strings.Trim(value[:54], "-") + "-" + suffix
}
