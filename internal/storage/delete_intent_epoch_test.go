package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const deleteIntentEpochCoordinationTimeout = 3 * time.Second

type deleteIntentEpochPrepareResult struct {
	intent DeleteIntentSnapshot
	err    error
}

func closeDeleteIntentEpochGate(gate chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(gate) })
	}
}

func waitDeleteIntentEpochStart(t *testing.T, started <-chan struct{}, prepared <-chan deleteIntentEpochPrepareResult, operation string) {
	t.Helper()
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case <-started:
		return
	case result := <-prepared:
		t.Fatalf("%s completed before reaching the coordinated point: %v", operation, result.err)
	case <-timer.C:
		t.Fatalf("timed out waiting for %s to reach the coordinated point", operation)
	}
}

func waitDeleteIntentEpochSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case <-signal:
		return
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func waitDeleteIntentEpochError(t *testing.T, result <-chan error, operation string) error {
	t.Helper()
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func waitDeleteIntentEpochPrepare(t *testing.T, result <-chan deleteIntentEpochPrepareResult, operation string) deleteIntentEpochPrepareResult {
	t.Helper()
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case prepared := <-result:
		return prepared
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", operation)
		return deleteIntentEpochPrepareResult{}
	}
}

func waitDeleteIntentEpochGate(ctx context.Context, gate <-chan struct{}, operation string) error {
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s", operation)
	}
}

func waitDeleteIntentEpochWriter(ctx context.Context, result <-chan error, operation string) error {
	timer := time.NewTimer(deleteIntentEpochCoordinationTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out waiting for %s", operation)
	}
}

func TestFileSystem_PrepareDeleteIntentsRetriesAfterConcurrentWriter(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/optimistic-retry.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("retry target"))); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	staleHash := strings.Repeat("a", 64)
	freshHash := strings.Repeat("b", 64)
	hashStarted := make(chan struct{})
	releaseHash := make(chan struct{})
	releaseHashOnce := closeDeleteIntentEpochGate(releaseHash)
	t.Cleanup(releaseHashOnce)
	var hashCalls atomic.Int32
	fs.hashDeleteTargetFile = func(ctx context.Context, path string) (string, error) {
		if path != targetPath {
			return "", fmt.Errorf("unexpected hash path %q", path)
		}
		if hashCalls.Add(1) == 1 {
			close(hashStarted)
			if err := waitDeleteIntentEpochGate(ctx, releaseHash, "optimistic hash release"); err != nil {
				return "", err
			}
			return staleHash, nil
		}
		return freshHash, nil
	}

	prepared := make(chan deleteIntentEpochPrepareResult, 1)
	go func() {
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		prepared <- deleteIntentEpochPrepareResult{intent: intent, err: err}
	}()
	waitDeleteIntentEpochStart(t, hashStarted, prepared, "initial optimistic hash")

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- fs.Mkdir(ctx, "/optimistic-writer")
	}()
	if err := waitDeleteIntentEpochError(t, writerDone, "concurrent optimistic writer"); err != nil {
		t.Fatalf("Mkdir(concurrent writer) error: %v", err)
	}
	releaseHashOnce()

	result := waitDeleteIntentEpochPrepare(t, prepared, "retried delete intent preparation")
	if result.err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", result.err)
	}
	if got := hashCalls.Load(); got != 2 {
		t.Fatalf("hash calls = %d, want 2 after one optimistic retry", got)
	}
	if len(result.intent.Targets) != 1 {
		t.Fatalf("prepared targets = %d, want 1", len(result.intent.Targets))
	}

	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		return freshHash, nil
	}
	want, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(fresh oracle) error: %v", err)
	}
	if result.intent.Targets[0].Token != want.Targets[0].Token {
		t.Fatalf("prepared token = %q, want retried token %q", result.intent.Targets[0].Token, want.Targets[0].Token)
	}
}

func TestFileSystem_PrepareDeleteIntentsDiscardsTransientScanErrorAfterEpochChange(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/transient-scan-error.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("transient scan error"))); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	transientErr := errors.New("transient optimistic scan error")
	hashStarted := make(chan struct{})
	releaseHash := make(chan struct{})
	releaseHashOnce := closeDeleteIntentEpochGate(releaseHash)
	t.Cleanup(releaseHashOnce)
	var hashCalls atomic.Int32
	fs.hashDeleteTargetFile = func(ctx context.Context, path string) (string, error) {
		if path != targetPath {
			return "", fmt.Errorf("unexpected hash path %q", path)
		}
		if hashCalls.Add(1) == 1 {
			close(hashStarted)
			if err := waitDeleteIntentEpochGate(ctx, releaseHash, "transient scan error release"); err != nil {
				return "", err
			}
			return "", transientErr
		}
		return strings.Repeat("9", 64), nil
	}

	prepared := make(chan deleteIntentEpochPrepareResult, 1)
	go func() {
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		prepared <- deleteIntentEpochPrepareResult{intent: intent, err: err}
	}()
	waitDeleteIntentEpochStart(t, hashStarted, prepared, "transient-error optimistic hash")

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- fs.Mkdir(ctx, "/transient-error-writer")
	}()
	if err := waitDeleteIntentEpochError(t, writerDone, "writer before transient scan error"); err != nil {
		t.Fatalf("Mkdir(concurrent writer) error: %v", err)
	}
	releaseHashOnce()

	result := waitDeleteIntentEpochPrepare(t, prepared, "retry after transient scan error")
	if result.err != nil {
		t.Fatalf("PrepareDeleteIntents() error = %v, want retry success", result.err)
	}
	if got := hashCalls.Load(); got != 2 {
		t.Fatalf("hash calls = %d, want 2 after discarding stale scan error", got)
	}
	if len(result.intent.Targets) != 1 || result.intent.Targets[0].Token == "" {
		t.Fatalf("prepared targets = %+v, want one target with a token", result.intent.Targets)
	}
}

func TestFileSystem_PrepareDeleteIntentsRetriesWholeTargetBatch(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targets := []string{"/batch-first.txt", "/batch-second.txt"}
	for _, target := range targets {
		if err := fs.WriteFile(ctx, target, bytes.NewReader([]byte(target))); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", target, err)
		}
	}

	freshHash := strings.Repeat("c", 64)
	staleHash := strings.Repeat("d", 64)
	secondHashStarted := make(chan struct{})
	releaseSecondHash := make(chan struct{})
	releaseSecondHashOnce := closeDeleteIntentEpochGate(releaseSecondHash)
	t.Cleanup(releaseSecondHashOnce)
	var firstCalls atomic.Int32
	var secondCalls atomic.Int32
	fs.hashDeleteTargetFile = func(ctx context.Context, path string) (string, error) {
		switch path {
		case targets[0]:
			firstCalls.Add(1)
			return freshHash, nil
		case targets[1]:
			if secondCalls.Add(1) == 1 {
				close(secondHashStarted)
				if err := waitDeleteIntentEpochGate(ctx, releaseSecondHash, "second target hash release"); err != nil {
					return "", err
				}
				return staleHash, nil
			}
			return freshHash, nil
		default:
			return "", fmt.Errorf("unexpected hash path %q", path)
		}
	}

	prepared := make(chan deleteIntentEpochPrepareResult, 1)
	go func() {
		intent, err := fs.PrepareDeleteIntents(ctx, targets, nil)
		prepared <- deleteIntentEpochPrepareResult{intent: intent, err: err}
	}()
	waitDeleteIntentEpochStart(t, secondHashStarted, prepared, "second target optimistic hash")

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- fs.Mkdir(ctx, "/batch-writer")
	}()
	if err := waitDeleteIntentEpochError(t, writerDone, "concurrent batch writer"); err != nil {
		t.Fatalf("Mkdir(concurrent writer) error: %v", err)
	}
	releaseSecondHashOnce()

	result := waitDeleteIntentEpochPrepare(t, prepared, "whole-batch retry")
	if result.err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", result.err)
	}
	if got := firstCalls.Load(); got != 2 {
		t.Fatalf("first target hash calls = %d, want 2 for whole-batch retry", got)
	}
	if got := secondCalls.Load(); got != 2 {
		t.Fatalf("second target hash calls = %d, want 2 for whole-batch retry", got)
	}
	if len(result.intent.Targets) != len(targets) {
		t.Fatalf("prepared targets = %d, want %d", len(result.intent.Targets), len(targets))
	}

	fs.hashDeleteTargetFile = func(context.Context, string) (string, error) {
		return freshHash, nil
	}
	want, err := fs.PrepareDeleteIntents(ctx, targets, nil)
	if err != nil {
		t.Fatalf("PrepareDeleteIntents(fresh oracle) error: %v", err)
	}
	for i := range targets {
		if result.intent.Targets[i].Token != want.Targets[i].Token {
			t.Fatalf("target %q token = %q, want retried token %q", targets[i], result.intent.Targets[i].Token, want.Targets[i].Token)
		}
	}
}

func TestFileSystem_PrepareDeleteIntentsCancellationDoesNotRetry(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	const targetPath = "/cancel-no-retry.txt"
	if err := fs.WriteFile(context.Background(), targetPath, bytes.NewReader([]byte("cancel target"))); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hashStarted := make(chan struct{})
	releaseHash := make(chan struct{})
	releaseHashOnce := closeDeleteIntentEpochGate(releaseHash)
	t.Cleanup(releaseHashOnce)
	var hashCalls atomic.Int32
	fs.hashDeleteTargetFile = func(ctx context.Context, path string) (string, error) {
		if path != targetPath {
			return "", fmt.Errorf("unexpected hash path %q", path)
		}
		if hashCalls.Add(1) != 1 {
			return "", errors.New("unexpected retry after cancellation")
		}
		close(hashStarted)
		if err := waitDeleteIntentEpochGate(ctx, releaseHash, "canceled hash release"); err != nil {
			return "", err
		}
		return strings.Repeat("e", 64), nil
	}

	prepared := make(chan deleteIntentEpochPrepareResult, 1)
	go func() {
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		prepared <- deleteIntentEpochPrepareResult{intent: intent, err: err}
	}()
	waitDeleteIntentEpochStart(t, hashStarted, prepared, "cancelable optimistic hash")

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- fs.Mkdir(context.Background(), "/cancel-writer")
	}()
	if err := waitDeleteIntentEpochError(t, writerDone, "concurrent writer before cancellation"); err != nil {
		t.Fatalf("Mkdir(concurrent writer) error: %v", err)
	}
	cancel()
	releaseHashOnce()

	result := waitDeleteIntentEpochPrepare(t, prepared, "canceled delete intent preparation")
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("PrepareDeleteIntents() error = %v, want context.Canceled", result.err)
	}
	if got := hashCalls.Load(); got != 1 {
		t.Fatalf("hash calls = %d, want 1 without cancellation retry", got)
	}
}

func TestFileSystem_PrepareDeleteIntentsFallsBackToReadLockAfterOptimisticRaces(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/fallback-lock.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("fallback target"))); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	fallbackHashStarted := make(chan struct{})
	releaseFallbackHash := make(chan struct{})
	releaseFallbackHashOnce := closeDeleteIntentEpochGate(releaseFallbackHash)
	t.Cleanup(releaseFallbackHashOnce)
	var hashCalls atomic.Int32
	fs.hashDeleteTargetFile = func(ctx context.Context, path string) (string, error) {
		if path != targetPath {
			return "", fmt.Errorf("unexpected hash path %q", path)
		}
		call := hashCalls.Add(1)
		if call <= int32(maxOptimisticDeleteIntentAttempts) {
			writerDone := make(chan error, 1)
			go func(call int32) {
				writerDone <- fs.Mkdir(ctx, fmt.Sprintf("/fallback-race-%d", call))
			}(call)
			if err := waitDeleteIntentEpochWriter(ctx, writerDone, fmt.Sprintf("optimistic writer %d", call)); err != nil {
				return "", fmt.Errorf("concurrent writer %d: %w", call, err)
			}
			return strings.Repeat("f", 64), nil
		}
		if call == int32(maxOptimisticDeleteIntentAttempts+1) {
			close(fallbackHashStarted)
			if err := waitDeleteIntentEpochGate(ctx, releaseFallbackHash, "fallback hash release"); err != nil {
				return "", err
			}
			return strings.Repeat("f", 64), nil
		}
		return "", fmt.Errorf("unexpected hash call %d", call)
	}

	prepared := make(chan deleteIntentEpochPrepareResult, 1)
	go func() {
		intent, err := fs.PrepareDeleteIntents(ctx, []string{targetPath}, nil)
		prepared <- deleteIntentEpochPrepareResult{intent: intent, err: err}
	}()
	waitDeleteIntentEpochStart(t, fallbackHashStarted, prepared, "locked fallback hash")

	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		writerDone <- fs.Mkdir(ctx, "/fallback-blocked-writer")
	}()
	waitDeleteIntentEpochSignal(t, writerStarted, "fallback writer start")
	select {
	case err := <-writerDone:
		t.Fatalf("writer completed while fallback hash held the read lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseFallbackHashOnce()
	result := waitDeleteIntentEpochPrepare(t, prepared, "locked fallback preparation")
	if result.err != nil {
		t.Fatalf("PrepareDeleteIntents() error: %v", result.err)
	}
	if err := waitDeleteIntentEpochError(t, writerDone, "writer after fallback release"); err != nil {
		t.Fatalf("Mkdir(writer after fallback release) error: %v", err)
	}
	if got := hashCalls.Load(); got != int32(maxOptimisticDeleteIntentAttempts+1) {
		t.Fatalf("hash calls = %d, want %d optimistic attempts plus locked fallback", got, maxOptimisticDeleteIntentAttempts+1)
	}
}

func TestFileSystem_WriterAdmissionsAdvanceDeleteIntentEpoch(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	const targetPath = "/versioning-epoch.txt"
	if err := fs.WriteFile(ctx, targetPath, bytes.NewReader([]byte("versioning"))); err != nil {
		t.Fatalf("WriteFile(target) error: %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "trash settings",
			run: func() error {
				policy := fs.CurrentDeletePolicy()
				fs.UpdateTrashSettings(policy.Mode == DeleteModeTrash, policy.TrashRetentionDays, policy.MaxTrashSize)
				return nil
			},
		},
		{
			name: "retention settings",
			run: func() error {
				fs.UpdateRetentionSettings(fs.config.MaxVersions, fs.config.MaxVersionAge, fs.config.MinFreeSpace, fs.config.RetentionSweepInterval)
				return nil
			},
		},
		{
			name: "runtime policy settings",
			run: func() error {
				policy := fs.CurrentDeletePolicy()
				fs.UpdateRuntimePolicySettings(RuntimePolicySettings{
					MaxVersions:        fs.config.MaxVersions,
					MaxVersionAge:      fs.config.MaxVersionAge,
					MinFreeSpace:       fs.config.MinFreeSpace,
					SweepInterval:      fs.config.RetentionSweepInterval,
					TrashEnabled:       policy.Mode == DeleteModeTrash,
					TrashRetentionDays: policy.TrashRetentionDays,
					MaxTrashSize:       policy.MaxTrashSize,
				})
				return nil
			},
		},
		{
			name: "versioning settings",
			run: func() error {
				fs.UpdateVersioningSettings(fs.config.AutoVersionedExtensions, fs.config.AutoVersionedFilenames, fs.config.MaxVersionedSize)
				return nil
			},
		},
		{
			name: "dataplane client",
			run: func() error {
				fs.SetDataplaneClient(fs.config.Dataplane)
				return nil
			},
		},
		{
			name: "set versioning success",
			run: func() error {
				return fs.SetVersioning(ctx, targetPath, false)
			},
		},
		{
			name: "set versioning failure",
			run: func() error {
				err := fs.SetVersioning(ctx, "/missing-versioning-epoch.txt", false)
				if !errors.Is(err, ErrNotFound) {
					return fmt.Errorf("SetVersioning(missing) error = %v, want ErrNotFound", err)
				}
				return nil
			},
		},
		{
			name: "failed mkdir admission",
			run: func() error {
				err := fs.Mkdir(ctx, "../invalid-epoch-path")
				if err == nil {
					return errors.New("Mkdir(invalid path) error = nil")
				}
				return nil
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			before := readDeleteIntentEpochForTest(fs)
			if err := testCase.run(); err != nil {
				t.Fatal(err)
			}
			after := readDeleteIntentEpochForTest(fs)
			if after != before+1 {
				t.Fatalf("mutation epoch = %d after admission, want %d", after, before+1)
			}
		})
	}
}

func readDeleteIntentEpochForTest(fs *FileSystem) uint64 {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.mutationEpoch
}
