package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	prometheusAddress string
	prometheusOrgID   string
	slackWebhookURL   string
	kubeConfig        string
	kubeConfigPath    string
	clusterName       string
	nodepoolName      string
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

	rootCmd.PersistentFlags().StringVar(&prometheusAddress, "prometheus-address", "http://localhost:8080/prometheus", "Prometheus 서버 주소")
	rootCmd.PersistentFlags().StringVar(&prometheusOrgID, "prometheus-org-id", "organization-dev", "Prometheus 조직 ID")
	rootCmd.PersistentFlags().StringVar(&slackWebhookURL, "slack-webhook-url", "", "Slack Webhook URL")
	rootCmd.PersistentFlags().StringVar(&kubeConfig, "kube-config", "local", "Kubernetes 설정 (local 또는 cluster)")
	rootCmd.PersistentFlags().StringVar(&kubeConfigPath, "kube-config-path", "", "Kubernetes config 파일 경로 (선택)")
	rootCmd.PersistentFlags().StringVar(&clusterName, "cluster-name", "", "클러스터 이름")
	rootCmd.PersistentFlags().StringVar(&nodepoolName, "nodepool-name", "devel-nodepool-name", "노드풀 이름")
}

func initConfig() {
	applyRootFlagEnv(rootCmd)
}

func applyRootFlagEnv(command *cobra.Command) {
	setEnvFromFlagOrDefault(command, "prometheus-address", "PROMETHEUS_ADDRESS", prometheusAddress)
	setEnvFromFlagOrDefault(command, "prometheus-org-id", "PROMETHEUS_SCOPE_ORG_ID", prometheusOrgID)
	setEnvFromFlagOrDefault(command, "slack-webhook-url", "SLACK_WEBHOOK_URL", slackWebhookURL)
	setEnvFromFlagOrDefault(command, "kube-config", "KUBE_CONFIG", kubeConfig)
	setEnvFromFlagOrDefault(command, "kube-config-path", "KUBECONFIG", kubeConfigPath)
	setEnvFromFlagOrDefault(command, "cluster-name", "CLUSTER_NAME", clusterName)
	setEnvFromFlagOrDefault(command, "nodepool-name", "NODEPOOL_NAME", nodepoolName)
}

func setEnvFromFlagOrDefault(command *cobra.Command, flagName string, envKey string, value string) {
	if commandFlagChanged(command, flagName) {
		_ = os.Setenv(envKey, value)
		return
	}
	if _, exists := os.LookupEnv(envKey); exists {
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
