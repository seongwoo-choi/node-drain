package pod

import (
	"context"
	"sort"
	"sync"
)

type pdbTokenManager struct {
	mu     sync.Mutex
	tokens map[string]*pdbTokenState
}

type pdbTokenState struct {
	inFlight int
	notify   chan struct{}
}

var globalPDBTokenManager = &pdbTokenManager{
	tokens: map[string]*pdbTokenState{},
}

func (m *pdbTokenManager) acquire(ctx context.Context, key string, maxInFlight int) error {
	if maxInFlight <= 0 {
		maxInFlight = 1
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		m.mu.Lock()
		state := m.getOrCreateLocked(key)
		if state.inFlight < maxInFlight {
			state.inFlight++
			m.mu.Unlock()
			return nil
		}
		notify := state.notify
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notify:
		}
	}
}

func (m *pdbTokenManager) release(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.tokens[key]
	if state == nil {
		return
	}
	if state.inFlight > 0 {
		state.inFlight--
	}
	close(state.notify)
	state.notify = make(chan struct{})
}

func (m *pdbTokenManager) getOrCreateLocked(key string) *pdbTokenState {
	if state, ok := m.tokens[key]; ok {
		return state
	}
	state := &pdbTokenState{notify: make(chan struct{})}
	m.tokens[key] = state
	return state
}

func normalizePDBTokenMaxInFlight(maxInFlight int) int {
	if maxInFlight <= 0 {
		return 1
	}
	return maxInFlight
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
	maxInFlight = normalizePDBTokenMaxInFlight(maxInFlight)

	var acquired []string
	for _, k := range keysCopy {
		if err := globalPDBTokenManager.acquire(ctx, k, maxInFlight); err != nil {
			releasePDBTokenKeys(acquired)
			return func() {}, err
		}
		acquired = append(acquired, k)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releasePDBTokenKeys(acquired)
		})
	}, nil
}

func releasePDBTokenKeys(acquired []string) {
	for i := len(acquired) - 1; i >= 0; i-- {
		globalPDBTokenManager.release(acquired[i])
	}
}
