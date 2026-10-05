package core

import (
	"errors"
	"sync"
	"testing"
)

func TestCredentialCohortIsPerGroup(t *testing.T) {
	if credentialCohort(1) != 0 || credentialCohort(2) != 1 || credentialCohort(3) != 2 {
		t.Fatalf("cohorts=%d,%d,%d want 0,1,2", credentialCohort(1), credentialCohort(2), credentialCohort(3))
	}
	if credentialStreamID(0) != 100 || credentialStreamID(1) != 200 {
		t.Fatalf("stream ids %d %d", credentialStreamID(0), credentialStreamID(1))
	}
	if workersPerCredential != 18 {
		t.Fatalf("workersPerCredential=%d want 18", workersPerCredential)
	}
}

func TestTurnCredRejectedOnlyAuthCodes(t *testing.T) {
	reject := []string{
		"Allocate error response (error 401: Unauthorized)",
		"Allocate error response (error 438: Stale Nonce)",
		"Allocate error response (error 441: Wrong Credentials)",
		"invalid credential",
	}
	for _, msg := range reject {
		if !turnCredRejected(errors.New(msg)) {
			t.Fatalf("must reject %q", msg)
		}
	}
	keep := []string{
		"TURN Allocate: all retransmissions failed",
		"TURN квота: error 486",
		"attribute not found",
		"use of closed network connection",
		"",
	}
	for _, msg := range keep {
		if turnCredRejected(errors.New(msg)) {
			t.Fatalf("must keep credentials on %q", msg)
		}
	}
	if turnCredRejected(nil) {
		t.Fatal("nil is not a credential rejection")
	}
}

func TestCredCohortRotatesAfterEighteenAllocations(t *testing.T) {
	cohort := newCredCohortState()
	n := 0
	fetch := func() (*Credentials, error) {
		n++
		return &Credentials{User: string(rune('a' + n)), Pass: "p", TurnURLs: []string{"turn.example:3478"}}, nil
	}
	var first string
	for i := 0; i < workersPerCredential; i++ {
		cred, err := cohort.lease(fetch)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = cred.User
		} else if cred.User != first {
			t.Fatalf("allocation %d user %q want %q", i, cred.User, first)
		}
	}
	if n != 1 {
		t.Fatalf("fetches=%d want 1 for %d allocations", n, workersPerCredential)
	}
	cred, err := cohort.lease(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || cred.User == first {
		t.Fatalf("after 18 allocations fetches=%d user=%q", n, cred.User)
	}
}

func TestCredCohortInvalidateFetchesAgain(t *testing.T) {
	cohort := newCredCohortState()
	n := 0
	fetch := func() (*Credentials, error) {
		n++
		return &Credentials{User: string(rune('a' + n)), Pass: "p"}, nil
	}
	if _, err := cohort.lease(fetch); err != nil {
		t.Fatal(err)
	}
	cohort.invalidate()
	cred, err := cohort.lease(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || cred.User == "b" {
		t.Fatalf("fetches=%d user=%q", n, cred.User)
	}
}

func TestCredCohortSingleflight(t *testing.T) {
	cohort := newCredCohortState()
	var n int
	var mu sync.Mutex
	started := make(chan struct{})
	release := make(chan struct{})
	fetch := func() (*Credentials, error) {
		mu.Lock()
		n++
		mu.Unlock()
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return &Credentials{User: "same", Pass: "p", TurnURLs: []string{"t"}}, nil
	}
	var wg sync.WaitGroup
	errCh := make(chan error, workersPerCredential)
	for i := 0; i < workersPerCredential; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cred, err := cohort.lease(fetch)
			if err != nil {
				errCh <- err
				return
			}
			if cred.User != "same" {
				errCh <- errors.New(cred.User)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("parallel leases fetched %d times", n)
	}
}

func TestPlanWorkerGroupsUsesEveryHash(t *testing.T) {
	sizes := planWorkerGroups(18, 4)
	if len(sizes) != 4 {
		t.Fatalf("groups=%v want 4", sizes)
	}
	sum := 0
	for _, n := range sizes {
		if n < 1 {
			t.Fatalf("empty group in %v", sizes)
		}
		sum += n
	}
	if sum != 18 {
		t.Fatalf("sum=%d sizes=%v", sum, sizes)
	}
	one := planWorkerGroups(9, 1)
	if len(one) != 1 || one[0] != 9 {
		t.Fatalf("single hash: %v", one)
	}
	two := planWorkerGroups(18, 2)
	if len(two) != 2 || two[0] != 9 || two[1] != 9 {
		t.Fatalf("two hashes: %v", two)
	}
}

func TestResetBudgetKeepsLogin(t *testing.T) {
	cohort := newCredCohortState()
	n := 0
	fetch := func() (*Credentials, error) {
		n++
		return &Credentials{User: "kept", Pass: "p", TurnURLs: []string{"t"}}, nil
	}
	for i := 0; i < workersPerCredential; i++ {
		if _, err := cohort.lease(fetch); err != nil {
			t.Fatal(err)
		}
	}
	cohort.resetBudget()
	cred, err := cohort.lease(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || cred.User != "kept" {
		t.Fatalf("fetches=%d user=%q, бюджет должен сброситься без нового VK", n, cred.User)
	}
}
