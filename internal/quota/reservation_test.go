package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cancelOnSecondErrContext struct {
	done   chan struct{}
	calls  atomic.Int32
	closed atomic.Bool
}

func newCancelOnSecondErrContext() *cancelOnSecondErrContext {
	return &cancelOnSecondErrContext{done: make(chan struct{})}
}

func (c *cancelOnSecondErrContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *cancelOnSecondErrContext) Done() <-chan struct{} {
	return c.done
}

func (c *cancelOnSecondErrContext) Err() error {
	if c.calls.Add(1) == 1 {
		return nil
	}
	if c.closed.CompareAndSwap(false, true) {
		close(c.done)
	}
	return context.Canceled
}

func (c *cancelOnSecondErrContext) Value(any) any {
	return nil
}

func TestCoordinatorReserveAccountsForOutstandingReservations(t *testing.T) {
	coordinator := NewCoordinator()
	var used atomic.Int64

	first, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{
			Key:           ScopeKey("directory", "/team"),
			UsedBytes:     used.Load(),
			LimitBytes:    10,
			RequiredBytes: 6,
		}}, nil
	})
	if err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	defer first.Release()

	_, err = coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{
			Key:           ScopeKey("directory", "/team"),
			UsedBytes:     used.Load(),
			LimitBytes:    10,
			RequiredBytes: 5,
		}}, nil
	})
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("second Reserve() error = %v, want ExceededError", err)
	}
	if exceeded.UsedBytes != 0 || exceeded.ReservedBytes != 6 || exceeded.AvailableBytes != 4 {
		t.Fatalf("ExceededError = %+v, want used=0 reserved=6 available=4", exceeded)
	}
}

func TestCoordinatorReserveRecomputesUsageAfterRelease(t *testing.T) {
	coordinator := NewCoordinator()
	var used atomic.Int64

	first, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{
			Key:           ScopeKey("directory", "/team"),
			UsedBytes:     used.Load(),
			LimitBytes:    10,
			RequiredBytes: 6,
		}}, nil
	})
	if err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}

	used.Store(6)
	first.Release()

	_, err = coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{
			Key:           ScopeKey("directory", "/team"),
			UsedBytes:     used.Load(),
			LimitBytes:    10,
			RequiredBytes: 5,
		}}, nil
	})
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("Reserve() after committed release error = %v, want ExceededError", err)
	}
	if exceeded.UsedBytes != 6 || exceeded.ReservedBytes != 0 || exceeded.AvailableBytes != 4 {
		t.Fatalf("ExceededError = %+v, want used=6 reserved=0 available=4", exceeded)
	}
}

func TestCoordinatorReserveIsAtomicAcrossScopes(t *testing.T) {
	coordinator := NewCoordinator()
	userKey := ScopeKey("user", "/users/alice")
	directoryKey := ScopeKey("directory", "/team")

	_, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{
			{Key: userKey, UsedBytes: 0, LimitBytes: 10, RequiredBytes: 4},
			{Key: directoryKey, UsedBytes: 8, LimitBytes: 10, RequiredBytes: 4},
		}, nil
	})
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("Reserve() error = %v, want ExceededError", err)
	}

	reservation, err := coordinator.Reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(userKey); got != 0 {
			t.Fatalf("user reservation after rejected batch = %d, want 0", got)
		}
		if got := view.ReservedBytes(directoryKey); got != 0 {
			t.Fatalf("directory reservation after rejected batch = %d, want 0", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection Reserve() error = %v", err)
	}
	reservation.Release()
}

func TestReservationReleaseIsIdempotent(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{
			Key:           key,
			LimitBytes:    10,
			RequiredBytes: 6,
		}}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	reservation.Release()
	reservation.Release()

	inspection, err := coordinator.Reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 0 {
			t.Fatalf("reserved bytes after repeated release = %d, want 0", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection Reserve() error = %v", err)
	}
	inspection.Release()
}

func TestCoordinatorReserveAggregatesDuplicateScopeClaims(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")

	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{
			{Key: key, UsedBytes: 1, LimitBytes: 10, RequiredBytes: 2},
			{Key: key, UsedBytes: 2, LimitBytes: 9, RequiredBytes: 3},
		}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	defer reservation.Release()

	inspection, err := coordinator.Reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 5 {
			t.Fatalf("aggregated reserved bytes = %d, want 5", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection Reserve() error = %v", err)
	}
	inspection.Release()
}

func TestCoordinatorReserveRejectsRequiredByteOverflow(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	_, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{
			{Key: key, LimitBytes: maxInt64, RequiredBytes: maxInt64},
			{Key: key, LimitBytes: maxInt64, RequiredBytes: 1},
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("Reserve() overflow error = %v, want fail-closed overflow", err)
	}
}

func TestCoordinatorReserveTreatsNegativeAndZeroLimitsAsNoCapacity(t *testing.T) {
	for _, limit := range []int64{-1, 0} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			coordinator := NewCoordinator()
			_, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
				return []Claim{{
					Key:           ScopeKey("directory", "/team"),
					LimitBytes:    limit,
					RequiredBytes: 1,
				}}, nil
			})
			var exceeded *ExceededError
			if !errors.As(err, &exceeded) {
				t.Fatalf("Reserve() error = %v, want ExceededError", err)
			}
		})
	}
}

func TestCoordinatorReserveChecksContextBeforeAndAfterPrepare(t *testing.T) {
	coordinator := NewCoordinator()
	canceledBefore, cancelBefore := context.WithCancel(context.Background())
	cancelBefore()
	called := false
	_, err := coordinator.Reserve(canceledBefore, func(context.Context, View) ([]Claim, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("pre-canceled Reserve() = (%v, called=%t), want context.Canceled without callback", err, called)
	}

	canceledAfter, cancelAfter := context.WithCancel(context.Background())
	_, err = coordinator.Reserve(canceledAfter, func(context.Context, View) ([]Claim, error) {
		cancelAfter()
		return []Claim{{Key: ScopeKey("directory", "/team"), LimitBytes: 10, RequiredBytes: 1}}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("post-prepare canceled Reserve() error = %v, want context.Canceled", err)
	}
}

func TestCoordinatorReserveCancelsWhileWaitingForCriticalSection(t *testing.T) {
	coordinator := NewCoordinator()
	prepareEntered := make(chan struct{})
	allowPrepareReturn := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		reservation, err := coordinator.Reserve(context.Background(), func(context.Context, View) ([]Claim, error) {
			close(prepareEntered)
			<-allowPrepareReturn
			return nil, nil
		})
		if reservation != nil {
			reservation.Release()
		}
		firstDone <- err
	}()
	<-prepareEntered

	waitContext, cancelWait := context.WithCancel(context.Background())
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	var secondPrepareCalled atomic.Bool
	go func() {
		close(secondStarted)
		_, err := coordinator.Reserve(waitContext, func(context.Context, View) ([]Claim, error) {
			secondPrepareCalled.Store(true)
			return nil, nil
		})
		secondDone <- err
	}()
	<-secondStarted
	cancelWait()

	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting Reserve() error = %v, want context.Canceled", err)
		}
		if secondPrepareCalled.Load() {
			t.Fatal("waiting Reserve() invoked prepare after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("waiting Reserve() did not stop after context cancellation")
	}

	close(allowPrepareReturn)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
}

func TestCoordinatorReserveRechecksContextBeforePrepare(t *testing.T) {
	coordinator := NewCoordinator()
	ctx := newCancelOnSecondErrContext()
	prepareCalled := false

	_, err := coordinator.Reserve(ctx, func(context.Context, View) ([]Claim, error) {
		prepareCalled = true
		return nil, nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reserve() error = %v, want context.Canceled", err)
	}
	if prepareCalled {
		t.Fatal("Reserve() invoked prepare after cancellation at critical-section admission")
	}
}

func TestCoordinatorAcquireMutationCancelsWhileWaiting(t *testing.T) {
	coordinator := NewCoordinator()
	first, err := coordinator.AcquireMutation(t.Context())
	if err != nil {
		t.Fatalf("first AcquireMutation() error = %v", err)
	}
	defer first.Release()

	waitContext, cancel := context.WithCancel(t.Context())
	waitDone := make(chan error, 1)
	go func() {
		_, err := coordinator.AcquireMutation(waitContext)
		waitDone <- err
	}()
	cancel()

	select {
	case err := <-waitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting AcquireMutation() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting AcquireMutation() did not stop after context cancellation")
	}
}

func TestMutationLeaseRefreshAtomicallyReplacesReservation(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{Key: key, UsedBytes: 10, LimitBytes: 100, RequiredBytes: 20}}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	defer reservation.Release()

	mutation, err := coordinator.AcquireMutation(t.Context())
	if err != nil {
		t.Fatalf("AcquireMutation() error = %v", err)
	}
	defer mutation.Release()
	err = mutation.Refresh(t.Context(), reservation, func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 0 {
			t.Fatalf("Refresh() view includes replaced reservation: %d", got)
		}
		return []Claim{{Key: key, UsedBytes: 40, LimitBytes: 100, RequiredBytes: 50}}, nil
	})
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	inspection, err := mutation.reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 50 {
			t.Fatalf("refreshed reservation = %d, want 50", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection reserve error = %v", err)
	}
	inspection.Release()
}

func TestMutationLeaseRefreshFailureKeepsOriginalReservation(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{Key: key, UsedBytes: 10, LimitBytes: 100, RequiredBytes: 20}}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	defer reservation.Release()

	mutation, err := coordinator.AcquireMutation(t.Context())
	if err != nil {
		t.Fatalf("AcquireMutation() error = %v", err)
	}
	defer mutation.Release()
	err = mutation.Refresh(t.Context(), reservation, func(context.Context, View) ([]Claim, error) {
		return []Claim{{Key: key, UsedBytes: 90, LimitBytes: 100, RequiredBytes: 20}}, nil
	})
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("Refresh() error = %v, want ExceededError", err)
	}

	inspection, err := mutation.reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 20 {
			t.Fatalf("reservation after failed refresh = %d, want 20", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection reserve error = %v", err)
	}
	inspection.Release()
}

func TestMutationLeaseRefreshAndReleaseRaceDoesNotLeakReservation(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{Key: key, LimitBytes: 100, RequiredBytes: 20}}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	mutation, err := coordinator.AcquireMutation(t.Context())
	if err != nil {
		t.Fatalf("AcquireMutation() error = %v", err)
	}
	defer mutation.Release()
	refreshEntered := make(chan struct{})
	allowRefresh := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- mutation.Refresh(t.Context(), reservation, func(context.Context, View) ([]Claim, error) {
			close(refreshEntered)
			<-allowRefresh
			return []Claim{{Key: key, LimitBytes: 100, RequiredBytes: 40}}, nil
		})
	}()
	<-refreshEntered
	releaseDone := make(chan struct{})
	go func() {
		reservation.Release()
		close(releaseDone)
	}()
	close(allowRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	<-releaseDone

	inspection, err := mutation.reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 0 {
			t.Fatalf("reservation after refresh/release race = %d, want 0", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection reserve error = %v", err)
	}
	inspection.Release()
}

func TestMutationLeaseRefreshRejectsReleasedReservation(t *testing.T) {
	coordinator := NewCoordinator()
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	reservation.Release()

	mutation, err := coordinator.AcquireMutation(t.Context())
	if err != nil {
		t.Fatalf("AcquireMutation() error = %v", err)
	}
	defer mutation.Release()
	err = mutation.Refresh(t.Context(), reservation, func(context.Context, View) ([]Claim, error) {
		return nil, nil
	})
	if !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("Refresh(released) error = %v, want ErrReservationReleased", err)
	}
}

func TestScopeKeyNormalizesEquivalentPaths(t *testing.T) {
	if left, right := ScopeKey("DIRECTORY", "/team/"), ScopeKey(" directory ", "/team"); left != right {
		t.Fatalf("ScopeKey equivalents differ: %q != %q", left, right)
	}
	for _, invalid := range [][2]string{
		{"", "/team"},
		{"directory", ""},
		{"directory", "team"},
		{"directory", `\team`},
		{"directory", `/team\private`},
		{"directory\x00forged", "/team"},
		{"directory", "/team\x00forged"},
		{"directory\nforged", "/team"},
		{"directory", "/team\nforged"},
		{"directory\tforged", "/team"},
		{"directory", "/team\tforged"},
	} {
		if got := ScopeKey(invalid[0], invalid[1]); got != "" {
			t.Fatalf("ScopeKey(%q, %q) = %q, want empty", invalid[0], invalid[1], got)
		}
	}
}

func TestReservationReleaseIsConcurrentAndIdempotent(t *testing.T) {
	coordinator := NewCoordinator()
	key := ScopeKey("directory", "/team")
	reservation, err := coordinator.Reserve(t.Context(), func(context.Context, View) ([]Claim, error) {
		return []Claim{{Key: key, LimitBytes: 100, RequiredBytes: 80}}, nil
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	done := make(chan struct{}, 32)
	for range 32 {
		go func() {
			reservation.Release()
			done <- struct{}{}
		}()
	}
	for range 32 {
		<-done
	}

	inspection, err := coordinator.Reserve(t.Context(), func(_ context.Context, view View) ([]Claim, error) {
		if got := view.ReservedBytes(key); got != 0 {
			t.Fatalf("reserved bytes after concurrent release = %d, want 0", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("inspection Reserve() error = %v", err)
	}
	inspection.Release()
}

func TestCoordinatorReserveRejectsNilContext(t *testing.T) {
	coordinator := NewCoordinator()
	if _, err := coordinator.Reserve(nil, func(context.Context, View) ([]Claim, error) {
		return nil, nil
	}); err == nil {
		t.Fatal("Reserve(nil) error = nil, want fail-closed error")
	}
}
