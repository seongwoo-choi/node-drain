package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestDrainCommandReturnsKubeClientError(t *testing.T) {
	if drainCmd.RunE == nil {
		t.Fatal("drain command RunE is nil")
	}

	restore := snapshotCommandGlobals()
	defer restore()
	restoreCommandEnv(t)

	prometheusAddress = "http://localhost:8080/prometheus"
	prometheusOrgID = "organization-dev"
	slackWebhookURL = ""
	kubeConfig = "invalid-mode"
	kubeConfigPath = ""
	clusterName = "test-cluster"
	nodepoolName = "test-nodepool"

	drainPolicy = "formula"
	drainRounding = "floor"
	drainMin = 0
	drainMaxAbsolute = 0
	drainMaxFraction = 0
	drainStepRules = ""
	drainSafetyMaxAllocateRate = 0
	drainSafetyQueries = ""
	drainSafetyFailClosed = true
	drainProgressive = true

	podEvictionMode = "evict"
	podForce = false
	podForceProblemPods = true
	podPDBToken = true
	podPDBTokenMaxInFlight = 1
	podMaxConcurrent = 30
	podMaxRetries = 3
	podRetryBackoff = "10s"
	podDeletionTimeout = "2m"
	podCheckInterval = "20s"

	err := drainCmd.RunE(drainCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "쿠버네티스 클라이언트 생성 실패") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func restoreCommandEnv(t *testing.T) {
	t.Helper()

	keys := []string{
		"PROMETHEUS_ADDRESS",
		"PROMETHEUS_SCOPE_ORG_ID",
		"SLACK_WEBHOOK_URL",
		"KUBE_CONFIG",
		"KUBECONFIG",
		"CLUSTER_NAME",
		"NODEPOOL_NAME",
		"DRAIN_POLICY",
		"DRAIN_ROUNDING",
		"DRAIN_MIN",
		"DRAIN_MAX_ABSOLUTE",
		"DRAIN_MAX_FRACTION",
		"DRAIN_STEP_RULES",
		"DRAIN_SAFETY_MAX_ALLOCATE_RATE",
		"DRAIN_SAFETY_QUERIES",
		"DRAIN_SAFETY_FAIL_CLOSED",
		"DRAIN_PROGRESSIVE",
		"POD_EVICTION_MODE",
		"POD_FORCE",
		"POD_FORCE_PROBLEM_PODS",
		"POD_PDB_TOKEN",
		"POD_PDB_TOKEN_MAX_IN_FLIGHT",
		"POD_MAX_CONCURRENT",
		"POD_MAX_RETRIES",
		"POD_RETRY_BACKOFF",
		"POD_DELETION_TIMEOUT",
		"POD_CHECK_INTERVAL",
	}
	for _, key := range keys {
		t.Setenv(key, os.Getenv(key))
	}
}

func snapshotCommandGlobals() func() {
	origPrometheusAddress := prometheusAddress
	origPrometheusOrgID := prometheusOrgID
	origSlackWebhookURL := slackWebhookURL
	origKubeConfig := kubeConfig
	origKubeConfigPath := kubeConfigPath
	origClusterName := clusterName
	origNodepoolName := nodepoolName

	origDrainPolicy := drainPolicy
	origDrainRounding := drainRounding
	origDrainMin := drainMin
	origDrainMaxAbsolute := drainMaxAbsolute
	origDrainMaxFraction := drainMaxFraction
	origDrainStepRules := drainStepRules
	origDrainSafetyMaxAllocateRate := drainSafetyMaxAllocateRate
	origDrainSafetyQueries := drainSafetyQueries
	origDrainSafetyFailClosed := drainSafetyFailClosed
	origDrainProgressive := drainProgressive

	origPodEvictionMode := podEvictionMode
	origPodForce := podForce
	origPodForceProblemPods := podForceProblemPods
	origPodPDBToken := podPDBToken
	origPodPDBTokenMaxInFlight := podPDBTokenMaxInFlight
	origPodMaxConcurrent := podMaxConcurrent
	origPodMaxRetries := podMaxRetries
	origPodRetryBackoff := podRetryBackoff
	origPodDeletionTimeout := podDeletionTimeout
	origPodCheckInterval := podCheckInterval

	return func() {
		prometheusAddress = origPrometheusAddress
		prometheusOrgID = origPrometheusOrgID
		slackWebhookURL = origSlackWebhookURL
		kubeConfig = origKubeConfig
		kubeConfigPath = origKubeConfigPath
		clusterName = origClusterName
		nodepoolName = origNodepoolName

		drainPolicy = origDrainPolicy
		drainRounding = origDrainRounding
		drainMin = origDrainMin
		drainMaxAbsolute = origDrainMaxAbsolute
		drainMaxFraction = origDrainMaxFraction
		drainStepRules = origDrainStepRules
		drainSafetyMaxAllocateRate = origDrainSafetyMaxAllocateRate
		drainSafetyQueries = origDrainSafetyQueries
		drainSafetyFailClosed = origDrainSafetyFailClosed
		drainProgressive = origDrainProgressive

		podEvictionMode = origPodEvictionMode
		podForce = origPodForce
		podForceProblemPods = origPodForceProblemPods
		podPDBToken = origPodPDBToken
		podPDBTokenMaxInFlight = origPodPDBTokenMaxInFlight
		podMaxConcurrent = origPodMaxConcurrent
		podMaxRetries = origPodMaxRetries
		podRetryBackoff = origPodRetryBackoff
		podDeletionTimeout = origPodDeletionTimeout
		podCheckInterval = origPodCheckInterval
	}
}
