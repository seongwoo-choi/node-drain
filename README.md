# node-drain

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
  --prometheus-org-id "organization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --slack-webhook-url "https://hooks.slack.com/services/XXXXXXXX" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

karpenter allocate rate
```sh
go run main.go karpenter allocate-rate \
  --prometheus-address "http://localhost:8080/prometheus" \
  --prometheus-org-id "organization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

## 워크로드 노드 정리 플로우

```
+------------------------------------------------------------+
|                      node-manager CLI                      |
+-------------------+----------------------+-----------------+
| Drain Command     | Karpenter Command    | Root Command    |
| (cmd/drain.go)    | (cmd/karpenter.go)   | (cmd/root.go)   |
+-------------------+----------------------+-----------------+
               |                 |
               |                 +-------------------------------+
               |                            Prometheus
               |                 +-------------------------------+
+--------------v---------------+                                  
| Kubernetes Client (config)  |                                  
| - kube_client_set.go        |                                  
| - prometheus_client.go      |                                  
+--------------+--------------+                                  
               |                                              
               |                                              
+--------------v--------------+    +--------------------------+
| Node Drain Logic            |    | Slack Notification       |
| (pkg/node/node_drain.go)    |    | (pkg/notification)       |
+--------------+--------------+    +-------------+------------+
               |                               |
               |                               |
         +-----v-----+                    +----v----+
         | Pod Evict |                    | Slack   |
         | (pkg/pod) |                    | Webhook |
         +-----------+                    +---------+

```

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

## 실행 결과 예시

### 노드풀 사용률 확인

```
INFO Karpenter Allocate Rate 사용량 조회 커맨드를 실행합니다.
INFO query query="karpenter_nodepool_usage{nodepool='nodepool-name', resource_type='memory'}"
INFO query query="sum(karpenter_nodes_total_pod_requests{nodepool='nodepool-name',resource_type='memory'} + karpenter_nodes_total_daemon_requests{nodepool='nodepool-name',resource_type='memory'})"
INFO Karpenter nodepoolUsage="331 GB"
INFO Karpenter podRequest="91 GB"
INFO query query="karpenter_nodepool_usage{nodepool='nodepool-name', resource_type='cpu'}"
INFO query query="sum(karpenter_nodes_total_pod_requests{nodepool='nodepool-name',resource_type='cpu'} + karpenter_nodes_total_daemon_requests{nodepool='nodepool-name',resource_type='cpu'})"
INFO Karpenter nodepoolUsage="48 vCPU"
INFO Karpenter podRequest="34 vCPU"
INFO Karpenter memoryAllocateRate="27 %"
INFO Karpenter cpuAllocateRate="71 %"
```

### 노드 드레인 실행

```
INFO 노드 드레인 커맨드를 실행합니다.
INFO 노드 사용률 조회 중
INFO 현재 노드 개수 lenNodes=8
INFO Slack 알림 전송 완료
INFO query query="karpenter_nodepool_usage{nodepool='nodepool-name', resource_type='memory'}"
INFO Karpenter nodepoolUsage="464 GB"
INFO Karpenter podRequest="104 GB"
INFO Karpenter nodepoolUsage="64 vCPU"
INFO Karpenter podRequest="40 vCPU"
INFO Memory 사용률 memoryAllocateRate=22
INFO Cpu 사용률 cpuAllocateRate=63
INFO 최대 사용률 maxAllocateRate=63
INFO 드레인 비율 drainRate=0.36
INFO 드레인 할 노드 개수 drainNodeCount=2
INFO 노드 Cordon 완료 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 노드 Cordon 완료 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 노드에서 pod evict 시작 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 파드 eviction 시작 pod=service-controller-xxxx-xxxx
INFO 파드 eviction 시작 pod=snapshot-xxxx-xxxx
INFO 파드 eviction 완료 pod=service-controller-xxxx-xxxx
INFO 파드 eviction 시작 pod=portal-web-xxxx-xxxx
INFO 파드 eviction 완료 pod=snapshot-xxxx-xxxx
INFO 파드 eviction 시작 pod=api-xxxx-xxxx
INFO 문제 상태 파드 강제 삭제 pod=problem-pod status=Pending
INFO 파드 eviction 완료 pod=portal-web-xxxx-xxxx
INFO 노드에서 pod evict 완료 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 노드에서 데몬셋을 제외한 모든 파드가 종료될 때까지 기다리는 중 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 데몬셋을 제외한 모든 Pod가 종료됨 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO Memory 사용률 memoryAllocateRate=27
INFO Cpu 사용률 cpuAllocateRate=71
INFO Slack 알림 전송 완료
```

## 주요 기능

### 문제 상태의 파드 자동 감지 및 처리
- `ImagePullBackOff`, `ErrImagePull`, `CrashLoopBackOff` 등의 상태인 파드 자동 감지
- 문제 파드는 강제 삭제 처리(gracePeriod=0)로 빠른 드레인 지원

### Completed 상태 파드 처리
- 컨설리데이션 과정에서 자동으로 완료된 배치 작업이 정리됨
- 잔여 Completed 파드는 짧은 gracePeriod로 빠르게 삭제

### 효율적인 드레인 계산
- 클러스터 사용률에 따라 최적의 드레인 노드 수 자동 계산
- 사용률이 낮을수록 더 많은 노드 드레인, 높을수록 보수적 접근

### 안전한 파드 이동
- PDB(PodDisruptionBudget) 확인을 통한 서비스 안정성 보장
- 노드 당 최대 동시 eviction 수 제한으로 부하 분산
