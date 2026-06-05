package pod

import (
	"context"
	"sort"
	"sync"
)

type pdbTokenManager struct {
	mu   sync.Mutex
	sems map[string]chan struct{}
}

var globalPDBTokenManager = &pdbTokenManager{
	sems: map[string]chan struct{}{},
}

func (m *pdbTokenManager) getOrCreate(key string, maxInFlight int) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.sems[key]; ok {
		return ch
	}
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	ch := make(chan struct{}, maxInFlight)
	m.sems[key] = ch
	return ch
}

// acquirePDBTokens는 여러 PDB 키에 대한 토큰을 고정 순서로 획득해 deadlock을 방지합니다.
// 반환되는 release 함수를 반드시 호출해야 합니다.
func acquirePDBTokens(ctx context.Context, keys []string, maxInFlight int) (func(), error) {
	if len(keys) == 0 {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	keysCopy := append([]string(nil), keys...)
	sort.Strings(keysCopy)

	var acquired []chan struct{}
	for _, k := range keysCopy {
		sem := globalPDBTokenManager.getOrCreate(k, maxInFlight)
		select {
		case sem <- struct{}{}:
			acquired = append(acquired, sem)
		case <-ctx.Done():
			releasePDBTokenChannels(acquired)
			return func() {}, ctx.Err()
		}
	}

	return func() {
		releasePDBTokenChannels(acquired)
	}, nil
}

func releasePDBTokenChannels(acquired []chan struct{}) {
	for i := len(acquired) - 1; i >= 0; i-- {
		<-acquired[i]
	}
}
