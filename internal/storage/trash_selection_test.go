package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTrashRootAuthorizationError_IsNilSafe(t *testing.T) {
	var nilError *TrashRootAuthorizationError
	if got := nilError.Error(); got != "trash root authorization failed" {
		t.Fatalf("nil Error() = %q", got)
	}
	if err := nilError.Unwrap(); err != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", err)
	}

	emptyError := &TrashRootAuthorizationError{}
	if got := emptyError.Error(); got != "trash root authorization failed" {
		t.Fatalf("empty Error() = %q", got)
	}
}

func TestFileSystem_EmptyTrashSelection_RejectsInvalidSelection(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()

	overLimit := make([]string, MaxTrashSelectionIDs+1)
	for i := range overLimit {
		overLimit[i] = fmt.Sprintf("id-%d", i)
	}

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "empty"},
		{name: "empty id", ids: []string{""}},
		{name: "duplicate", ids: []string{"first", "first"}},
		{name: "too long", ids: []string{strings.Repeat("a", MaxTrashSelectionIDLength+1)}},
		{name: "space", ids: []string{"invalid id"}},
		{name: "slash", ids: []string{"invalid/id"}},
		{name: "dot", ids: []string{"invalid.id"}},
		{name: "unicode", ids: []string{"无效"}},
		{name: "over limit", ids: overLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := fs.EmptyTrashSelection(ctx, test.ids, nil)
			if !errors.Is(err, ErrInvalidTrashSelection) {
				t.Fatalf("EmptyTrashSelection() error = %v, want ErrInvalidTrashSelection", err)
			}
			assertTrashSelectionResult(t, result, nil, nil, nil)
		})
	}
}

func TestFileSystem_EmptyTrashSelection_HoldsMutationLockAndPreservesLaterDelete(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	items := createTrashSelectionItems(t, fs, "/selected.txt")
	selectedID := items["/selected.txt"].ID
	if err := fs.WriteFile(ctx, "/later.txt", bytes.NewReader([]byte("later"))); err != nil {
		t.Fatalf("WriteFile(later) error: %v", err)
	}

	authorizeStarted := make(chan struct{})
	releaseAuthorize := make(chan struct{})
	selectionDone := make(chan struct{})
	var selectionResult TrashSelectionResult
	var selectionErr error
	go func() {
		selectionResult, selectionErr = fs.EmptyTrashSelection(ctx, []string{selectedID}, func(string) error {
			close(authorizeStarted)
			<-releaseAuthorize
			return nil
		})
		close(selectionDone)
	}()
	<-authorizeStarted

	deleteStarted := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		close(deleteStarted)
		deleteDone <- fs.Delete(ctx, "/later.txt")
	}()
	<-deleteStarted
	select {
	case err := <-deleteDone:
		close(releaseAuthorize)
		<-selectionDone
		t.Fatalf("Delete(later) completed while trash selection held the mutation lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseAuthorize)
	<-selectionDone
	if selectionErr != nil {
		t.Fatalf("EmptyTrashSelection() error: %v", selectionErr)
	}
	assertTrashSelectionResult(t, selectionResult, []string{selectedID}, nil, nil)
	if err := <-deleteDone; err != nil {
		t.Fatalf("Delete(later) error: %v", err)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].OriginalPath != "/later.txt" {
		t.Fatalf("remaining trash items = %+v, want only later delete", remaining)
	}
}

func TestFileSystem_EmptyTrashSelection_DeletesOnlyRequestedIDsInRequestOrder(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	items := createTrashSelectionItems(t, fs, "/selected-first.txt", "/selected-second.txt")

	selectedIDs := []string{items["/selected-second.txt"].ID, "missing", items["/selected-first.txt"].ID}

	if err := fs.WriteFile(ctx, "/added-after-selection.txt", bytes.NewReader([]byte("new"))); err != nil {
		t.Fatalf("WriteFile(added item) error: %v", err)
	}
	if err := fs.Delete(ctx, "/added-after-selection.txt"); err != nil {
		t.Fatalf("Delete(added item) error: %v", err)
	}
	allItems, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	var addedID string
	for _, item := range allItems {
		if item.OriginalPath == "/added-after-selection.txt" {
			addedID = item.ID
		}
	}
	if addedID == "" {
		t.Fatal("added trash item not found")
	}

	var deleteOrder []string
	originalRemoveMetadata := fs.removeTrashMetadata
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		deleteOrder = append(deleteOrder, id)
		return originalRemoveMetadata(ctx, id)
	}

	result, err := fs.EmptyTrashSelection(ctx, selectedIDs, nil)
	if err != nil {
		t.Fatalf("EmptyTrashSelection() error: %v", err)
	}
	assertTrashSelectionResult(t, result, []string{selectedIDs[0], selectedIDs[2]}, nil, []string{"missing"})
	if !slices.Equal(deleteOrder, result.DeletedIDs) {
		t.Fatalf("delete order = %v, want %v", deleteOrder, result.DeletedIDs)
	}

	remaining, err := fs.ListTrash(ctx)
	if err != nil {
		t.Fatalf("ListTrash(after selection) error: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != addedID {
		t.Fatalf("remaining trash items = %+v, want only added item %q", remaining, addedID)
	}
}

func TestFileSystem_EmptyTrashSelection_AuthorizesEveryRestorePathBeforeDeleting(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/allowed.txt", bytes.NewReader([]byte("allowed"))); err != nil {
		t.Fatalf("WriteFile(allowed) error: %v", err)
	}
	if err := fs.Mkdir(ctx, "/denied"); err != nil {
		t.Fatalf("Mkdir(denied) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/denied/child.txt", bytes.NewReader([]byte("denied"))); err != nil {
		t.Fatalf("WriteFile(denied child) error: %v", err)
	}
	if err := fs.Delete(ctx, "/allowed.txt"); err != nil {
		t.Fatalf("Delete(allowed) error: %v", err)
	}
	if err := fs.Delete(ctx, "/denied"); err != nil {
		t.Fatalf("Delete(denied) error: %v", err)
	}
	items := trashSelectionItemsByPath(t, fs)
	ids := []string{items["/allowed.txt"].ID, "missing", items["/denied"].ID}

	deniedErr := errors.New("denied")
	var authorized []string
	result, err := fs.EmptyTrashSelection(ctx, ids, func(restoredPath string) error {
		authorized = append(authorized, restoredPath)
		if restoredPath == "/denied/child.txt" {
			return deniedErr
		}
		return nil
	})
	if !errors.Is(err, deniedErr) {
		t.Fatalf("EmptyTrashSelection() error = %v, want denied error", err)
	}
	var rootAuthorizationError *TrashRootAuthorizationError
	if errors.As(err, &rootAuthorizationError) {
		t.Fatalf("descendant denial was wrapped as root authorization failure: %+v", rootAuthorizationError)
	}
	assertTrashSelectionResult(t, result, nil, []string{ids[0], ids[2]}, []string{"missing"})
	if !slices.Equal(authorized, []string{"/allowed.txt", "/denied", "/denied/child.txt"}) {
		t.Fatalf("authorized paths = %v", authorized)
	}

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash(after denied selection) error: %v", listErr)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining trash count = %d, want 2", len(remaining))
	}
}

func TestFileSystem_EmptyTrashSelection_WrapsRootAuthorizationFailure(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	items := createTrashSelectionItems(t, fs, "/denied-root.txt", "/other.txt")
	ids := []string{items["/denied-root.txt"].ID, items["/other.txt"].ID}

	deniedErr := errors.New("root denied")
	result, err := fs.EmptyTrashSelection(ctx, ids, func(restoredPath string) error {
		if restoredPath == "/denied-root.txt" {
			return deniedErr
		}
		return nil
	})
	if !errors.Is(err, deniedErr) {
		t.Fatalf("EmptyTrashSelection() error = %v, want root denial", err)
	}
	var rootAuthorizationError *TrashRootAuthorizationError
	if !errors.As(err, &rootAuthorizationError) {
		t.Fatalf("EmptyTrashSelection() error = %v, want TrashRootAuthorizationError", err)
	}
	if rootAuthorizationError.Path != "/denied-root.txt" {
		t.Fatalf("root authorization path = %q, want /denied-root.txt", rootAuthorizationError.Path)
	}
	assertTrashSelectionResult(t, result, nil, ids, nil)

	remaining, listErr := fs.ListTrash(ctx)
	if listErr != nil {
		t.Fatalf("ListTrash() error: %v", listErr)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining trash count = %d, want 2", len(remaining))
	}
}

func TestFileSystem_WalkTrashItemRestorePaths_PreservesRootCallbackError(t *testing.T) {
	fs := setupFileSystem(t)
	items := createTrashSelectionItems(t, fs, "/walk-root.txt")

	callbackErr := errors.New("callback failed")
	err := fs.WalkTrashItemRestorePaths(context.Background(), items["/walk-root.txt"].ID, func(string, bool, int64) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("WalkTrashItemRestorePaths() error = %v, want callback error", err)
	}
	var rootAuthorizationError *TrashRootAuthorizationError
	if errors.As(err, &rootAuthorizationError) {
		t.Fatalf("public walker callback error was wrapped as authorization failure: %+v", rootAuthorizationError)
	}
}

func TestFileSystem_EmptyTrashSelection_HardFailureReturnsCompletePartition(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	items := createTrashSelectionItems(t, fs, "/first.txt", "/failed.txt", "/later.txt")
	ids := []string{
		"missing-before",
		items["/first.txt"].ID,
		items["/failed.txt"].ID,
		"missing-after",
		items["/later.txt"].ID,
	}

	failure := errors.New("metadata delete failed")
	originalRemoveMetadata := fs.removeTrashMetadata
	var attempted []string
	fs.removeTrashMetadata = func(ctx context.Context, id string) error {
		attempted = append(attempted, id)
		if id == items["/failed.txt"].ID {
			return failure
		}
		return originalRemoveMetadata(ctx, id)
	}

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("EmptyTrashSelection() error = %v, want hard failure", err)
	}
	assertTrashSelectionResult(t, result,
		[]string{items["/first.txt"].ID},
		[]string{items["/failed.txt"].ID, items["/later.txt"].ID},
		[]string{"missing-before", "missing-after"},
	)
	if !slices.Equal(attempted, []string{items["/first.txt"].ID, items["/failed.txt"].ID}) {
		t.Fatalf("attempted deletions = %v", attempted)
	}
}

func TestFileSystem_EmptyTrashSelection_ContinuesAfterCleanupWarning(t *testing.T) {
	fs := setupFileSystem(t)
	ctx := context.Background()
	if err := fs.WriteFile(ctx, "/versioned.md", bytes.NewReader([]byte("v1"))); err != nil {
		t.Fatalf("WriteFile(versioned v1) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/versioned.md", bytes.NewReader([]byte("v2"))); err != nil {
		t.Fatalf("WriteFile(versioned v2) error: %v", err)
	}
	if err := fs.WriteFile(ctx, "/plain.txt", bytes.NewReader([]byte("plain"))); err != nil {
		t.Fatalf("WriteFile(plain) error: %v", err)
	}
	if err := fs.Delete(ctx, "/versioned.md"); err != nil {
		t.Fatalf("Delete(versioned) error: %v", err)
	}
	if err := fs.Delete(ctx, "/plain.txt"); err != nil {
		t.Fatalf("Delete(plain) error: %v", err)
	}
	items := trashSelectionItemsByPath(t, fs)
	ids := []string{items["/versioned.md"].ID, "missing", items["/plain.txt"].ID}

	cleanupFailure := errors.New("delete object failed")
	fs.deleteVersionObject = func(context.Context, string) error { return cleanupFailure }

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	var warning *TrashDeleteWarningError
	if !errors.As(err, &warning) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("EmptyTrashSelection() error = %v, want TrashDeleteWarningError", err)
	}
	if warning.Partial() {
		t.Fatalf("cleanup-only warning unexpectedly marked partial: %v", err)
	}
	assertTrashSelectionResult(t, result, []string{ids[0], ids[2]}, nil, []string{"missing"})
}

func TestFileSystem_EmptyTrashSelection_ContextCancellationStopsAtNextItem(t *testing.T) {
	fs := setupFileSystem(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	items := createTrashSelectionItems(t, fs, "/first.txt", "/second.txt")
	ids := []string{items["/first.txt"].ID, "missing", items["/second.txt"].ID}

	originalRemoveTrashPath := fs.removeTrashPath
	removeCalls := 0
	fs.removeTrashPath = func(target string) error {
		removeCalls++
		if err := originalRemoveTrashPath(target); err != nil {
			return err
		}
		if removeCalls == 1 {
			cancel()
		}
		return nil
	}

	result, err := fs.EmptyTrashSelection(ctx, ids, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EmptyTrashSelection() error = %v, want context.Canceled", err)
	}
	assertTrashSelectionResult(t, result, []string{ids[0]}, []string{ids[2]}, []string{"missing"})
}

func createTrashSelectionItems(t *testing.T, fs *FileSystem, paths ...string) map[string]*TrashItem {
	t.Helper()
	ctx := context.Background()
	for _, itemPath := range paths {
		if err := fs.WriteFile(ctx, itemPath, bytes.NewReader([]byte(itemPath))); err != nil {
			t.Fatalf("WriteFile(%s) error: %v", itemPath, err)
		}
		if err := fs.Delete(ctx, itemPath); err != nil {
			t.Fatalf("Delete(%s) error: %v", itemPath, err)
		}
	}
	return trashSelectionItemsByPath(t, fs)
}

func trashSelectionItemsByPath(t *testing.T, fs *FileSystem) map[string]*TrashItem {
	t.Helper()
	items, err := fs.ListTrash(context.Background())
	if err != nil {
		t.Fatalf("ListTrash() error: %v", err)
	}
	byPath := make(map[string]*TrashItem, len(items))
	for _, item := range items {
		byPath[item.OriginalPath] = item
	}
	return byPath
}

func assertTrashSelectionResult(t *testing.T, result TrashSelectionResult, deleted, remaining, skipped []string) {
	t.Helper()
	if !slices.Equal(result.DeletedIDs, deleted) {
		t.Errorf("DeletedIDs = %v, want %v", result.DeletedIDs, deleted)
	}
	if !slices.Equal(result.RemainingIDs, remaining) {
		t.Errorf("RemainingIDs = %v, want %v", result.RemainingIDs, remaining)
	}
	if !slices.Equal(result.SkippedIDs, skipped) {
		t.Errorf("SkippedIDs = %v, want %v", result.SkippedIDs, skipped)
	}
	if got := len(result.DeletedIDs) + len(result.RemainingIDs) + len(result.SkippedIDs); got != len(deleted)+len(remaining)+len(skipped) {
		t.Errorf("result partition size = %d, want %d", got, len(deleted)+len(remaining)+len(skipped))
	}
}
