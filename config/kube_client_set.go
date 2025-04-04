package config

import (
	"flag"
	"fmt"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientCmd "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func GetKubeClientSet(kubeConfigFile string) (kubernetes.Interface, error) {
	switch {
	case kubeConfigFile == "github_action", kubeConfigFile == "local":
		var kubeConfig *string
		// github_action 에서 실행 시 config 를 가져올 때 사용
		// kubeConfig 경로를 지정하지 않으면 $HOME/.kube/config 로 지정
		if home := homedir.HomeDir(); home != "" {
			kubeConfig = flag.String("kubeConfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeConfig file")
		} else {
			kubeConfig = flag.String("kubeConfig", "", "absolute path to the kubeConfig file")
		}
		flag.Parse()
		config, configErr := clientCmd.BuildConfigFromFlags("", *kubeConfig)
		if configErr != nil {
			return nil, configErr
		}
		return getClientSet(config)
	case kubeConfigFile == "cluster":
		//클러스터 내부에서 config 를 가져올 때 사용
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		return getClientSet(config)
	default:
		return nil, fmt.Errorf("couldn't parse bencoded string")
	}
}

func getClientSet(config *rest.Config) (kubernetes.Interface, error) {
	// create the clientSet
	clientSet, clientSdtErr := kubernetes.NewForConfig(config)
	if clientSdtErr != nil {
		return nil, clientSdtErr
	}

	return clientSet, nil
}
