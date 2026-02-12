package config

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientCmd "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// GetKubeClientSet returns a Kubernetes clientset by configuration mode.
func GetKubeClientSet(kubeConfigMode string, kubeConfigPath string) (kubernetes.Interface, error) {
	switch {
	case kubeConfigMode == "github_action", kubeConfigMode == "local":
		configPath := resolveKubeConfigPath(kubeConfigPath)
		config, err := clientCmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return nil, err
		}
		return getClientSet(config)
	case kubeConfigMode == "cluster":
		// 클러스터 내부에서 config 를 가져올 때 사용
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		return getClientSet(config)
	default:
		return nil, fmt.Errorf("invalid kube config mode: %s", kubeConfigMode)
	}
}

func resolveKubeConfigPath(path string) string {
	if path != "" {
		return path
	}

	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		return envPath
	}

	if home := homedir.HomeDir(); home != "" {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}

func getClientSet(config *rest.Config) (kubernetes.Interface, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientSet, nil
}
