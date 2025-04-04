# k8s-automation

## Start

로컬에서 사용 시 아래를 순차적으로 적용하면 됩니다.

kube context 를 변경하여 EKS 워크로드 노드를 드레인하고 싶은 클러스터의 context 로 위치시킵니다.

K9S 혹은 Open Lens 를 사용하여 deployment 접근, mimir 검색 후 8080:8080 으로 포트포워딩

포트포워딩 설정 이후 아래 스크립트를 실행합니다.

```sh
go mod tidy
```

node drain
```sh
go run main.go drain \
  --prometheus-address "http://localhost:8080/prometheus" \
  --prometheus-org-id "orgainization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --slack-webhook-url "https://hooks.slack.com/services/XXXXXXXX" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

karpenter allocate rate
```sh
go run main.go karpenter allocate-rate \
  --prometheus-address "http://localhost:8080/prometheus" \
  --prometheus-org-id "orgainization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

## 워크로드 노드 정리 플로우

### 정리 대상 노드 식별
karpenter allocate rate 를 통해 노드 드레인 대수를 정합니다.

### 우선순위 설정
노드의 age(생성 시간)이 오래된 노드부터 순서를 매깁니다.

### 단계적 cordon 적용
1. 리스트 업 된 노드들에 cordon 을 적용합니다.
2. Cordon 된 노드들을 순서대로 Drain 한 뒤, N분 동안 대기합니다.
3. 문제가 없다면 다음 노드를 드레인합니다. 문제가 발생한다면 프로세스를 일시 중지하고 상황을 평가합니다.

### 정리 프로세스 시작
Cordon 된 노드들의 파드들에 대해 graceful shutdown 프로세스를 시작합니다.
한 노드의 정리가 완료되면 다음 노드로 넘어갑니다.(파드들이 다른 노드에 안정적으로 갈 수 있도록 3~5분 텀을 두도록 합니다.)

### 모니터링 및 조정
전체 프로세스 동안 클러스터의 상태를 지속적으로 모니터링하여 적절한 메모리 퍼센테이지를 찾습니다.
