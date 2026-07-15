package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupStagingStopsBeforeDurableWriteTransactionEvidence(t *testing.T) {
	fs := setupStandaloneFileSystem(t)
	ctx := context.Background()
	targetName := "/journal-owned-stage.bin"

	target, err := fs.captureWriteTarget(ctx, targetName)
	if err != nil {
		t.Fatalf("captureWriteTarget() error: %v", err)
	}
	source, err := fs.stageWriteReader(
		ctx,
		strings.NewReader("durable staged content"),
		defaultMaxWriteSize,
		writeSourceStagePrefix,
	)
	if err != nil {
		t.Fatalf("stageWriteReader() error: %v", err)
	}

	releaseMutation, err := fs.beginMutation(ctx)
	if err != nil {
		t.Fatalf("beginMutation() error: %v", err)
	}
	prepared, prepareErr := fs.prepareWriteTransactionRuntime(
		ctx,
		targetName,
		source,
		writeFileTransactionOptions{},
		target,
		fs.versions,
	)
	if prepareErr != nil {
		releaseMutation()
		t.Fatalf("prepareWriteTransactionRuntime() error: %v", prepareErr)
	}
	result, publishErr := fs.writeTransactionJournal.PublishPrepared(
		prepared.operationID,
		prepared.plan,
	)
	releaseMutation()
	if publishErr != nil || !result.FinalObserved {
		t.Fatalf("PublishPrepared() result=%+v error=%v", result, publishErr)
	}

	source.retained = true
	if err := source.file.Close(); err != nil {
		t.Fatalf("Close(staged source) error: %v", err)
	}
	source.file = nil

	files, _, cleanupErr := fs.CleanupStaging(ctx)
	if !errors.Is(cleanupErr, ErrWriteRecoveryRequired) {
		t.Fatalf("CleanupStaging() error = %v, want ErrWriteRecoveryRequired", cleanupErr)
	}
	if files != 0 {
		t.Fatalf("CleanupStaging() removed %d entries, want zero", files)
	}
	if _, err := fs.internalRootHandle.Stat(source.rel); err != nil {
		t.Fatalf("journal-owned source stage was changed: %v", err)
	}
	expectedJournalPath := filepath.Join(fs.config.InternalRoot, writeTransactionJournalDir)
	var recoveryErr *WriteRecoveryRequiredError
	if !errors.As(cleanupErr, &recoveryErr) ||
		filepath.Clean(recoveryErr.StagePath) != filepath.Clean(expectedJournalPath) {
		t.Fatalf("CleanupStaging() recovery error = %+v, want journal path %q", recoveryErr, expectedJournalPath)
	}
	if err := fs.Mkdir(ctx, "/blocked-after-journal-cleanup"); !errors.Is(err, ErrWriteRecoveryRequired) {
		t.Fatalf("Mkdir() after blocked cleanup error = %v, want ErrWriteRecoveryRequired", err)
	}

	if _, err := os.Stat(filepath.Join(fs.config.InternalRoot, source.rel)); err != nil {
		t.Fatalf("journal-owned source stage is not inspectable: %v", err)
	}
}
