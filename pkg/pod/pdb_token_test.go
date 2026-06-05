package pod

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcquirePDBTokensHonorsContextCancellationWhileWaiting(t *testing.T) {
	resetPDBTokenManagerForTest()

	releaseFirst, err := acquirePDBTokens(context.Background(), []string{"default/pdb-a"}, 1)
	if err != nil {
		t.Fatalf("initial acquirePDBTokens failed: %v", err)
	}
	defer releaseFirst()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	releaseBlocked, err := acquirePDBTokens(ctx, []string{"default/pdb-a"}, 1)
	if err == nil {
		releaseBlocked()
		t.Fatal("expected context cancellation while waiting for PDB token")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAcquirePDBTokensReleasesPartialAcquisitionOnCancellation(t *testing.T) {
	resetPDBTokenManagerForTest()

	releaseB, err := acquirePDBTokens(context.Background(), []string{"b"}, 1)
	if err != nil {
		t.Fatalf("initial acquirePDBTokens failed: %v", err)
	}
	defer releaseB()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, acquireErr := acquirePDBTokens(ctx, []string{"a", "b"}, 1)
		if release != nil {
			release()
		}
		done <- acquireErr
	}()

	waitForPDBTokenCount(t, "a", 1)
	cancel()

	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled PDB token acquisition")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	waitForPDBTokenCount(t, "a", 0)
}

func TestAcquirePDBTokensHonorsLowerLimitAfterHigherLimitAcquire(t *testing.T) {
	resetPDBTokenManagerForTest()

	releaseFirst, err := acquirePDBTokens(context.Background(), []string{"default/pdb-a"}, 2)
	if err != nil {
		t.Fatalf("initial acquirePDBTokens failed: %v", err)
	}
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	releaseSecond, err := acquirePDBTokens(ctx, []string{"default/pdb-a"}, 1)
	if err == nil {
		releaseSecond()
		t.Fatal("expected lower maxInFlight to block while one token is already in flight")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestAcquirePDBTokensHonorsHigherLimitAfterLowerLimitAcquire(t *testing.T) {
	resetPDBTokenManagerForTest()

	releaseFirst, err := acquirePDBTokens(context.Background(), []string{"default/pdb-a"}, 1)
	if err != nil {
		t.Fatalf("initial acquirePDBTokens failed: %v", err)
	}
	defer releaseFirst()

	releaseSecond, err := acquirePDBTokens(context.Background(), []string{"default/pdb-a"}, 2)
	if err != nil {
		t.Fatalf("higher maxInFlight should allow second acquisition: %v", err)
	}
	defer releaseSecond()

	if got := pdbTokenCountForTest("default/pdb-a"); got != 2 {
		t.Fatalf("PDB token count = %d, want=2", got)
	}
}

func resetPDBTokenManagerForTest() {
	globalPDBTokenManager.mu.Lock()
	defer globalPDBTokenManager.mu.Unlock()
	globalPDBTokenManager.tokens = map[string]*pdbTokenState{}
}

func waitForPDBTokenCount(t *testing.T, key string, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := pdbTokenCountForTest(key); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("PDB token count for %s did not become %d, got %d", key, want, pdbTokenCountForTest(key))
}

func pdbTokenCountForTest(key string) int {
	globalPDBTokenManager.mu.Lock()
	defer globalPDBTokenManager.mu.Unlock()
	state := globalPDBTokenManager.tokens[key]
	if state == nil {
		return 0
	}
	return state.inFlight
}
