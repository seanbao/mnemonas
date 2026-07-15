package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

type writeReadResult[T any] struct {
	value T
	err   error
}

func startStatMetadataRead(fs *FileSystem, name string) <-chan writeReadResult[*FileInfo] {
	result := make(chan writeReadResult[*FileInfo], 1)
	go func() {
		info, err := fs.StatMetadata(context.Background(), name)
		result <- writeReadResult[*FileInfo]{value: info, err: err}
	}()
	return result
}

func startDirectoryRead(fs *FileSystem, name string) <-chan writeReadResult[[]*FileInfo] {
	result := make(chan writeReadResult[[]*FileInfo], 1)
	go func() {
		entries, err := fs.ReadDir(context.Background(), name)
		result <- writeReadResult[[]*FileInfo]{value: entries, err: err}
	}()
	return result
}

func startSnapshotRead(fs *FileSystem, name string) <-chan writeReadResult[string] {
	result := make(chan writeReadResult[string], 1)
	go func() {
		file, _, err := fs.OpenFileSnapshotMetadata(context.Background(), name)
		if err != nil {
			result <- writeReadResult[string]{err: err}
			return
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		result <- writeReadResult[string]{value: string(data), err: errors.Join(readErr, closeErr)}
	}()
	return result
}

func observeWriteReadBlocked[T any](result <-chan writeReadResult[T]) (writeReadResult[T], bool) {
	select {
	case got := <-result:
		return got, false
	case <-time.After(100 * time.Millisecond):
		return writeReadResult[T]{}, true
	}
}

func requireDirectoryFile(t *testing.T, result writeReadResult[[]*FileInfo], name string, size int64) {
	t.Helper()
	if result.err != nil {
		t.Fatalf("ReadDir() error: %v", result.err)
	}
	for _, entry := range result.value {
		if entry != nil && entry.Name == name {
			if entry.Size != size {
				t.Fatalf("ReadDir() %s size = %d, want %d", name, entry.Size, size)
			}
			return
		}
	}
	t.Fatalf("ReadDir() entries = %+v, want %s", result.value, name)
}

func TestFileSystemWriteReadersDoNotObserveCapturedTargetGap(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	const target = "/visible.bin"
	const oldContent = "old"
	const newContent = "complete replacement"
	if err := fs.WriteFile(t.Context(), target, strings.NewReader(oldContent)); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}

	captured := make(chan struct{})
	releasePublish := make(chan struct{})
	nativeRead := make(chan writeReadResult[string], 1)
	originalRuntimeHook := writeTransactionRuntimeFaultHook
	writeTransactionRuntimeFaultHook = func(point string) error {
		if point == "after-visible-publish" {
			data, err := os.ReadFile(fs.workspace.FullPath(target))
			nativeRead <- writeReadResult[string]{value: string(data), err: err}
			close(captured)
			<-releasePublish
		}
		return originalRuntimeHook(point)
	}
	t.Cleanup(func() {
		writeTransactionRuntimeFaultHook = originalRuntimeHook
	})

	writeResult := make(chan error, 1)
	go func() {
		writeResult <- fs.WriteFile(context.Background(), target, strings.NewReader(newContent))
	}()
	<-captured
	if got := <-nativeRead; got.err != nil || got.value != newContent {
		close(releasePublish)
		<-writeResult
		t.Fatalf("native read inside publish barrier = %q, %v; want %q", got.value, got.err, newContent)
	}

	statResult := startStatMetadataRead(fs, target)
	dirResult := startDirectoryRead(fs, "/")
	snapshotResult := startSnapshotRead(fs, target)
	earlyStat, statBlocked := observeWriteReadBlocked(statResult)
	earlyDir, dirBlocked := observeWriteReadBlocked(dirResult)
	earlySnapshot, snapshotBlocked := observeWriteReadBlocked(snapshotResult)

	close(releasePublish)
	if err := <-writeResult; err != nil {
		t.Fatalf("WriteFile(new) error: %v", err)
	}
	if !statBlocked {
		t.Fatalf("StatMetadata completed inside the capture-to-publish window: %+v", earlyStat)
	}
	if !dirBlocked {
		t.Fatalf("ReadDir completed inside the capture-to-publish window: %+v", earlyDir)
	}
	if !snapshotBlocked {
		t.Fatalf("OpenFileSnapshotMetadata completed inside the capture-to-publish window: %+v", earlySnapshot)
	}
	if got := <-statResult; got.err != nil || got.value == nil || got.value.Size != int64(len(newContent)) {
		t.Fatalf("StatMetadata() = %+v, want complete replacement metadata", got)
	}
	requireDirectoryFile(t, <-dirResult, path.Base(target), int64(len(newContent)))
	if got := <-snapshotResult; got.err != nil || got.value != newContent {
		t.Fatalf("OpenFileSnapshotMetadata() content = %q, %v; want %q", got.value, got.err, newContent)
	}
}

func TestFileSystemWriteReadersWaitThroughPublishedRollback(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	const target = "/rollback-visible.bin"
	const oldContent = "stable old content"
	const newContent = "replacement that must rollback"
	if err := fs.WriteFile(t.Context(), target, strings.NewReader(oldContent)); err != nil {
		t.Fatalf("WriteFile(old) error: %v", err)
	}

	published := make(chan struct{})
	releaseRollback := make(chan struct{})
	indexErr := errors.New("injected index failure")
	originalRuntimeHook := writeTransactionRuntimeFaultHook
	writeTransactionRuntimeFaultHook = func(point string) error {
		if point == "after-visible-publish" {
			close(published)
			<-releaseRollback
			return indexErr
		}
		return originalRuntimeHook(point)
	}
	t.Cleanup(func() { writeTransactionRuntimeFaultHook = originalRuntimeHook })

	writeResult := make(chan error, 1)
	go func() {
		writeResult <- fs.WriteFile(context.Background(), target, strings.NewReader(newContent))
	}()
	<-published

	statResult := startStatMetadataRead(fs, target)
	dirResult := startDirectoryRead(fs, "/")
	snapshotResult := startSnapshotRead(fs, target)
	earlyStat, statBlockedDuringRollback := observeWriteReadBlocked(statResult)
	earlyDir, dirBlockedDuringRollback := observeWriteReadBlocked(dirResult)
	earlySnapshot, snapshotBlockedDuringRollback := observeWriteReadBlocked(snapshotResult)

	close(releaseRollback)
	if err := <-writeResult; !errors.Is(err, indexErr) {
		t.Fatalf("WriteFile(new) error = %v, want %v", err, indexErr)
	}
	if !statBlockedDuringRollback {
		t.Fatalf("StatMetadata completed before rollback: %+v", earlyStat)
	}
	if !dirBlockedDuringRollback {
		t.Fatalf("ReadDir completed before rollback: %+v", earlyDir)
	}
	if !snapshotBlockedDuringRollback {
		t.Fatalf("OpenFileSnapshotMetadata completed before rollback: %+v", earlySnapshot)
	}
	if got := <-statResult; got.err != nil || got.value == nil || got.value.Size != int64(len(oldContent)) {
		t.Fatalf("StatMetadata() after rollback = %+v, want old metadata", got)
	}
	requireDirectoryFile(t, <-dirResult, path.Base(target), int64(len(oldContent)))
	if got := <-snapshotResult; got.err != nil || got.value != oldContent {
		t.Fatalf("OpenFileSnapshotMetadata() after rollback = %q, %v; want %q", got.value, got.err, oldContent)
	}

	data, err := os.ReadFile(fs.workspace.FullPath(target))
	if err != nil || string(data) != oldContent {
		t.Fatalf("canonical content after rollback = %q, %v; want %q", data, err, oldContent)
	}
}

func TestFileSystemNativeReadsObserveCompleteOldOrNewContentDuringOverwrite(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	const target = "/native-old-or-new.bin"
	payloads := make([]string, 64)
	allowed := make(map[string]struct{}, len(payloads))
	for i := range payloads {
		payloads[i] = fmt.Sprintf("generation-%02d:%s", i, strings.Repeat(string(rune('a'+i%26)), 4096+i*31))
		allowed[payloads[i]] = struct{}{}
	}
	if err := fs.WriteFile(t.Context(), target, strings.NewReader(payloads[0])); err != nil {
		t.Fatalf("WriteFile(initial) error: %v", err)
	}

	readCtx, cancelReads := context.WithCancel(context.Background())
	defer cancelReads()
	readErr := make(chan error, 1)
	reportReadErr := func(err error) {
		select {
		case readErr <- err:
		default:
		}
	}
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-readCtx.Done():
					return
				default:
				}
				data, err := os.ReadFile(fs.workspace.FullPath(target))
				if err != nil {
					reportReadErr(fmt.Errorf("native ReadFile() exposed a namespace gap: %w", err))
					return
				}
				if _, ok := allowed[string(data)]; !ok {
					reportReadErr(fmt.Errorf("native ReadFile() returned incomplete or unknown generation of %d bytes", len(data)))
					return
				}
			}
		}()
	}

	for i := 1; i < len(payloads); i++ {
		if err := fs.WriteFile(t.Context(), target, strings.NewReader(payloads[i])); err != nil {
			cancelReads()
			readers.Wait()
			t.Fatalf("WriteFile(generation %d) error: %v", i, err)
		}
		select {
		case err := <-readErr:
			cancelReads()
			readers.Wait()
			t.Fatal(err)
		default:
		}
	}
	cancelReads()
	readers.Wait()
	select {
	case err := <-readErr:
		t.Fatal(err)
	default:
	}
	if data, err := os.ReadFile(fs.workspace.FullPath(target)); err != nil || string(data) != payloads[len(payloads)-1] {
		t.Fatalf("final native ReadFile() = %d bytes, %v; want final generation", len(data), err)
	}
}
