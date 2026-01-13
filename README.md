# node-drain

Karpenter가 관리하는 특정 NodePool의 워크로드 노드를 **안전하게 비우고(Drain) 교체하는 과정**을 자동화하는 CLI 도구입니다.  
Prometheus(또는 Mimir) 메트릭을 기반으로 현재 NodePool의 사용률을 산출하고, 그 결과를 바탕으로 **동시에 몇 대의 노드를 드레인할지 계산**한 뒤 **cordon → 파드 제거(삭제) → 종료 대기** 순으로 처리합니다.

---

## 주요 기능

- **Karpenter Allocate Rate 기반 드레인 대수 계산**
  - NodePool 사용률이 높으면 보수적으로, 낮으면 더 공격적으로 드레인 대상 노드 수를 산정합니다.
- **노드 우선순위(오래된 노드부터)**
  - Node 생성 시간이 오래된 노드부터 순차 처리합니다.
- **안전 장치**
  - 드레인 대상 노드에 먼저 `cordon`을 적용해 신규 스케줄링을 차단합니다.
  - DaemonSet 파드는 제외하고 워크로드 파드만 제거합니다.
  - 문제 상태 파드는 grace period를 0으로 두고 빠르게 정리해 드레인 지연을 줄입니다.
- **Slack 알림**
  - 시작 정보(노드 수), Karpenter 사용률, 드레인 완료/실패를 Slack Webhook으로 전송합니다.

---

## 사전 준비(로컬 실행 기준)

### 요구 사항

- Go `1.24+`
- Kubernetes 접근 권한(로컬 실행 시 `~/.kube/config` 또는 지정한 kubeconfig)
- Prometheus/Mimir 접근 경로(포트포워딩 또는 내부 네트워크)

1. **kube context 설정**
   - 드레인하려는 클러스터의 컨텍스트로 이동합니다.
2. **Prometheus/Mimir 접근**
   - K9S 또는 OpenLens 등을 이용해 Prometheus(Mimir) 쿼리를 수행할 엔드포인트를 확보합니다.
   - 예: `8080:8080` 포트포워딩 후 `http://localhost:8080/prometheus`로 접근
3. **의존성 정리**

```sh
go mod tidy
```

### 설치/빌드(선택)

바이너리로 빌드해서 실행할 수도 있습니다.

```sh
go build -o node-manager .
./node-manager drain --help
```

---

## 빠른 시작(예시)

아래 예시는 `devel_eks_cluster` 환경에서 실행하는 예시입니다.

### 1) NodePool 사용률 확인(Allocate Rate)

```sh
go run main.go karpenter allocate-rate \
  --prometheus-address "http://localhost:8080/prometheus" \
  --prometheus-org-id "organization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

### 2) 노드 드레인 실행

```sh
go run main.go drain \
  --prometheus-address "http://localhost:8080/prometheus" \
  --prometheus-org-id "organization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --slack-webhook-url "https://hooks.slack.com/services/XXXXXXXX" \
  --kube-config "local" \
  --cluster-name "devel_eks_cluster"
```

---

## 실행 모드(`--kube-config`)

- `local`: 로컬 kubeconfig(`~/.kube/config`)를 사용해 클러스터에 접근합니다.
- `cluster`: 클러스터 내부(in-cluster)에서 실행할 때 사용합니다(서비스 어카운트/Role 필요).
- `github_action`: GitHub Actions 등에서 로컬 kubeconfig를 사용하는 시나리오를 위한 모드입니다.

예: 클러스터 내부 실행(예시)

```sh
go run main.go drain \
  --kube-config "cluster" \
  --prometheus-address "http://prometheus.monitoring.svc:9090" \
  --prometheus-org-id "organization-dev" \
  --nodepool-name "worker-nodepool-name" \
  --cluster-name "devel_eks_cluster"
```

---

## 커맨드 레퍼런스

### `drain`

지정한 NodePool의 워크로드 노드를 안전하게 비우고 교체할 수 있도록 파드를 순차적으로 다른 노드로 이동시키는 커맨드입니다. 진행 상황은 Slack 알림 및 로그로 확인할 수 있습니다.

### `karpenter allocate-rate`

Karpenter 관련 메트릭을 조회해 NodePool의 자원 사용률(Allocate Rate)을 계산합니다. `drain` 커맨드가 동시에 드레인할 노드 수를 산정하는 데 참고하는 값입니다.

---

## 전역 플래그(공통)

모든 커맨드는 아래 플래그를 공통으로 받습니다(`cmd/root.go`).

| 플래그 | 기본값 | 설명 |
|---|---:|---|
| `--prometheus-address` | `http://localhost:8080/prometheus` | Prometheus 서버 주소 |
| `--prometheus-org-id` | `organization-dev` | 멀티테넌시 환경에서 사용하는 Org ID (`X-Scope-OrgID`) |
| `--slack-webhook-url` | `""` | Slack Webhook URL (`drain`에서 권장, 미지정 시 알림 실패) |
| `--kube-config` | `local` | `local`, `cluster`, `github_action` |
| `--cluster-name` | `""` | 알림 메시지에 포함될 클러스터 이름 |
| `--nodepool-name` | `devel-nodepool-name` | 드레인 대상 NodePool 이름 |

---

## 환경 변수

CLI는 실행 시 플래그 값을 아래 환경 변수로 주입합니다.

| 환경 변수 | 설명 |
|---|---|
| `PROMETHEUS_ADDRESS` | Prometheus 주소 |
| `PROMETHEUS_SCOPE_ORG_ID` | `X-Scope-OrgID` 헤더 값 |
| `SLACK_WEBHOOK_URL` | Slack Webhook URL |
| `KUBE_CONFIG` | kube config 모드(`local/cluster/github_action`) |
| `CLUSTER_NAME` | 클러스터 이름 |
| `NODEPOOL_NAME` | NodePool 이름 |

---

## 동작 원리(핵심 로직 요약)

### 대상 노드 식별

- Kubernetes Node 라벨 셀렉터를 사용합니다.
  - `karpenter.sh/nodepool=<NODEPOOL_NAME>`

### 우선순위

- Node 생성 시간이 오래된 순으로 정렬 후 앞에서부터 처리합니다.

### 드레인 대수 산정(Allocate Rate 기반)

1. Prometheus에서 Memory/CPU 사용률을 조회합니다.
2. `max(memoryAllocateRate, cpuAllocateRate)`를 계산합니다.
3. 아래 식으로 드레인 비율을 계산합니다.

```text
drainRate = (99 - maxAllocateRate) / 100
drainNodeCount = floor(lenNodes * drainRate)
```

> 예: 최대 사용률이 63%이고 노드가 8대면 drainRate=0.36, drainNodeCount=2로 계산됩니다.

### 드레인 프로세스

- 대상 노드들에 `cordon` 적용
- 노드별로:
  - DaemonSet 제외 워크로드 파드 조회
  - 일반 파드는 동시성 제한 하에 제거(삭제) 시도
  - 문제 상태 파드는 빠르게 강제 삭제(grace period 0)
  - 모든 워크로드 파드 종료까지 대기(최대 타임아웃 존재)

---

## 아키텍처(코드 구조)

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
| Kubernetes / Prometheus Client (config)                      |
| - kube_client_set.go                                          |
| - prometheus_client.go                                        |
+--------------+--------------+
               |
+--------------v--------------+    +--------------------------+
| Node Drain Logic            |    | Slack Notification       |
| (pkg/node)                  |    | (pkg/notification)       |
+--------------+--------------+    +-------------+------------+
               |                               |
               |                               |
         +-----v-----+                    +----v----+
         | Pod Logic |                    | Slack   |
         | (pkg/pod) |                    | Webhook |
         +-----------+                    +---------+
```

---

## 실행 로그 예시

### NodePool 사용률 확인

```text
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

```text
INFO 노드 드레인 커맨드를 실행합니다.
INFO 노드 사용률 조회 중
INFO 현재 노드 개수 lenNodes=8
INFO query query="karpenter_nodepool_usage{nodepool='nodepool-name', resource_type='memory'}"
INFO Memory 사용률 memoryAllocateRate=22
INFO Cpu 사용률 cpuAllocateRate=63
INFO 최대 사용률 maxAllocateRate=63
INFO 드레인 비율 drainRate=0.36
INFO 드레인 할 노드 개수 drainNodeCount=2
INFO 노드 Cordon 완료 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 노드에서 pod evict 시작 nodeName=ip-10-xxx-xx-xxx.compute.internal
INFO 파드 eviction 시작 pod=service-controller-xxxx-xxxx
INFO 문제 상태 파드 강제 삭제 pod=problem-pod status=Pending
INFO 노드에서 데몬셋을 제외한 모든 Pod가 종료됨 nodeName=ip-10-xxx-xx-xxx.compute.internal
```

---

## 주의사항 / 제한사항

- **PDB(PodDisruptionBudget)**
  - 본 도구는 파드 제거 시 PDB를 조회해 제한 여부를 확인하지만, 쿠버네티스의 eviction subresource를 사용하는 전통적인 방식과는 차이가 있을 수 있습니다.
- **드레인 대상이 0일 수 있음**
  - 현재 사용률이 매우 높거나, 노드 수가 적으면 계산 결과가 0이 될 수 있습니다. 이 경우 드레인을 수행하지 않습니다.
- **메트릭/라벨 의존성**
  - Prometheus에 Karpenter 관련 메트릭이 수집되고 있어야 하며, 노드는 `karpenter.sh/nodepool` 라벨을 기준으로 선택합니다.

---

## 권한(RBAC) 가이드(클러스터 내부 실행 시)

클러스터 내부에서 실행하려면 최소한 아래 권한이 필요합니다(환경에 맞게 조정하세요).

- Nodes: `get`, `list`, `watch`, `update`
- Pods: `get`, `list`, `watch`, `delete`
- PodDisruptionBudgets: `get`, `list`, `watch`

예시(참고용):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: node-manager
rules:
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["get", "list", "watch", "update"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch", "delete"]
  - apiGroups: ["policy"]
    resources: ["poddisruptionbudgets"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: node-manager
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: node-manager
subjects:
  - kind: ServiceAccount
    name: node-manager
    namespace: default
```

---

## 트러블슈팅

- **`drainNodeCount=0`으로 나오는 경우**
  - 현재 Allocate Rate가 매우 높거나, 노드 수가 적어서 계산 결과가 0일 수 있습니다.
  - 먼저 `karpenter allocate-rate`로 사용률을 확인한 뒤, 트래픽이 낮은 시간대에 실행하세요.
- **Prometheus 쿼리 실패**
  - `--prometheus-address`가 올바른지, 포트포워딩이 살아있는지 확인하세요.
  - 멀티테넌시 환경이면 `--prometheus-org-id`가 맞는지 확인하세요.
- **Slack 알림이 실패하는 경우**
  - `--slack-webhook-url`이 비어 있거나, Webhook이 200을 반환하지 않으면 실패합니다.
- **드레인이 오래 걸리거나 멈춘 것처럼 보이는 경우**
  - PDB 제약/재스케줄링 지연/이미지 pull 이슈 등이 원인일 수 있습니다.
  - 로그에서 “PDB 체크 실패” 또는 “Pod 삭제 타임아웃” 메시지를 확인하세요.

---

## 개발/테스트

```sh
go test ./...
```

