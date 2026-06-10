package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	prometheusAddress  string
	prometheusTenantID string
	prometheusOrgID    string
	slackWebhookURL    string
	kubeConfig         string
	kubeConfigPath     string
	clusterName        string
	nodepoolName       string
)

var rootCmd = &cobra.Command{
	Use:           "node-manager",
	Short:         "노드 관리 CLI 도구",
	Long:          `노드의 메모리/디스크 사용량을 모니터링하고 드레인을 수행하는 CLI 도구입니다.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&prometheusAddress, "prometheus-address", "", "Prometheus 서버 주소")
	rootCmd.PersistentFlags().StringVar(&prometheusTenantID, "prometheus-tenant-id", "", "Prometheus 테넌트 ID (X-Scope-OrgID)")
	rootCmd.PersistentFlags().StringVar(&prometheusOrgID, "prometheus-org-id", "", "Deprecated: Prometheus 테넌트 ID (use --prometheus-tenant-id)")
	rootCmd.PersistentFlags().StringVar(&slackWebhookURL, "slack-webhook-url", "", "Slack Webhook URL")
	rootCmd.PersistentFlags().StringVar(&kubeConfig, "kube-config", "local", "Kubernetes 설정 (local 또는 cluster)")
	rootCmd.PersistentFlags().StringVar(&kubeConfigPath, "kube-config-path", "", "Kubernetes config 파일 경로 (선택)")
	rootCmd.PersistentFlags().StringVar(&clusterName, "cluster-name", "", "클러스터 이름")
	rootCmd.PersistentFlags().StringVar(&nodepoolName, "nodepool-name", "", "노드풀 이름")
}

func initConfig() {
	applyRootFlagEnv(rootCmd)
}

func applyRootFlagEnv(command *cobra.Command) {
	setEnvFromFlagOrDefault(command, "prometheus-address", "PROMETHEUS_ADDRESS", prometheusAddress)
	applyPrometheusTenantEnv(command)
	setEnvFromFlagOrDefault(command, "slack-webhook-url", "SLACK_WEBHOOK_URL", slackWebhookURL)
	setEnvFromFlagOrDefault(command, "kube-config", "KUBE_CONFIG", kubeConfig)
	setEnvFromFlagOrDefault(command, "kube-config-path", "KUBECONFIG", kubeConfigPath)
	setEnvFromFlagOrDefault(command, "cluster-name", "CLUSTER_NAME", clusterName)
	setEnvFromFlagOrDefault(command, "nodepool-name", "NODEPOOL_NAME", nodepoolName)
}

func applyPrometheusTenantEnv(command *cobra.Command) {
	if commandFlagChanged(command, "prometheus-tenant-id") {
		_ = os.Setenv("PROMETHEUS_TENANT_ID", prometheusTenantID)
		if strings.TrimSpace(prometheusTenantID) == "" {
			_ = os.Setenv("PROMETHEUS_SCOPE_ORG_ID", "")
		}
		return
	}
	if commandFlagChanged(command, "prometheus-org-id") {
		_ = os.Setenv("PROMETHEUS_SCOPE_ORG_ID", prometheusOrgID)
		return
	}
	if envValue, exists := os.LookupEnv("PROMETHEUS_TENANT_ID"); exists && strings.TrimSpace(envValue) != "" {
		return
	}
	if envValue, exists := os.LookupEnv("PROMETHEUS_SCOPE_ORG_ID"); exists && strings.TrimSpace(envValue) != "" {
		return
	}
	if strings.TrimSpace(prometheusTenantID) != "" {
		_ = os.Setenv("PROMETHEUS_TENANT_ID", prometheusTenantID)
		return
	}
	if strings.TrimSpace(prometheusOrgID) != "" {
		_ = os.Setenv("PROMETHEUS_SCOPE_ORG_ID", prometheusOrgID)
	}
}

func setEnvFromFlagOrDefault(command *cobra.Command, flagName string, envKey string, value string) {
	if commandFlagChanged(command, flagName) {
		_ = os.Setenv(envKey, value)
		return
	}
	if envValue, exists := os.LookupEnv(envKey); exists && strings.TrimSpace(envValue) != "" {
		return
	}
	if value != "" {
		_ = os.Setenv(envKey, value)
	}
}

func setEnvFromChangedFlag(command *cobra.Command, flagName string, envKey string, value string) {
	if commandFlagChanged(command, flagName) {
		_ = os.Setenv(envKey, value)
	}
}

func commandFlagChanged(command *cobra.Command, flagName string) bool {
	if command == nil {
		command = rootCmd
	}
	flag := command.Flag(flagName)
	return flag != nil && flag.Changed
}

type requiredEnvValue struct {
	envKey   string
	flagName string
}

func validateRequiredEnvValues(values ...requiredEnvValue) error {
	for _, value := range values {
		if strings.TrimSpace(os.Getenv(value.envKey)) != "" {
			continue
		}
		return fmt.Errorf("필수 설정이 비어 있습니다: --%s 또는 %s를 설정하세요", value.flagName, value.envKey)
	}
	return nil
}
