package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
)

func setupWriteMetadataStore(t *testing.T) *Store {
	t.Helper()
	client := dataplane.NewClient("unused")
	store, err := New(Config{
		DBPath:    filepath.Join(t.TempDir(), "write-metadata.db"),
		Dataplane: client,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() {
		writeMetadataTestHook = nil
		writeMetadataCommitTransaction = commitWriteMetadataTransaction
		writeMetadataRollbackTransaction = rollbackWriteMetadataTransaction
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error: %v", err)
		}
		_ = client.Close()
	})
	return store
}

func TestWriteMetadataPlanCommitRollbackAndIdempotency(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/docs/report.txt"
	oldHash := strings.Repeat("a", 64)
	newHash := strings.Repeat("b", 64)
	beforeTime := time.Unix(1_700_000_100, 0)
	if err := store.UpdateFileIndex(ctx, path, 11, beforeTime, oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}

	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        22,
			ModTimeUnix: 1_700_000_200,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          11,
			CreatedAtUnix: 1_700_000_150,
			Comment:       "before overwrite",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if plan.IndexBefore == nil ||
		plan.IndexBefore.Path != path ||
		plan.IndexBefore.Size != 11 ||
		plan.IndexBefore.ModTimeUnix != beforeTime.Unix() ||
		plan.IndexBefore.ContentHash != oldHash {
		t.Fatalf("IndexBefore = %+v, want exact original row", plan.IndexBefore)
	}
	if plan.VersionBefore != nil || plan.VersionAfter == nil {
		t.Fatalf("version plan = before:%+v after:%+v, want planned insert", plan.VersionBefore, plan.VersionAfter)
	}
	if err := ValidateWriteMetadataPlan(plan); err != nil {
		t.Fatalf("ValidateWriteMetadataPlan() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)

	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateAfter)
	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata(idempotent) error: %v", err)
	}
	if err := store.EnsureWriteMetadataCommitted(ctx, plan); err != nil {
		t.Fatalf("EnsureWriteMetadataCommitted(idempotent) error: %v", err)
	}
	size, modTime, hash, err := store.GetFileIndex(ctx, path)
	if err != nil || size != 22 || modTime.Unix() != 1_700_000_200 || hash != newHash {
		t.Fatalf("committed index = (%d, %v, %q, %v)", size, modTime, hash, err)
	}
	version, err := store.GetVersion(ctx, path, oldHash)
	if err != nil {
		t.Fatalf("GetVersion(committed) error: %v", err)
	}
	if version.ID <= 0 ||
		version.Size != 11 ||
		version.CreatedAt.Unix() != 1_700_000_150 ||
		version.Comment != "before overwrite" {
		t.Fatalf("committed version = %+v", version)
	}

	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata(idempotent) error: %v", err)
	}
	size, modTime, hash, err = store.GetFileIndex(ctx, path)
	if err != nil || size != 11 || modTime.Unix() != beforeTime.Unix() || hash != oldHash {
		t.Fatalf("rolled-back index = (%d, %v, %q, %v)", size, modTime, hash, err)
	}
	if _, err := store.GetVersion(ctx, path, oldHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetVersion(after rollback) error = %v, want ErrNotFound", err)
	}
}

func TestWriteMetadataPlanCreatesAndRollsBackNewIndex(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	plan, err := store.CaptureWriteMetadataPlan(ctx, FileIndexRecord{
		Path:        "/new.txt",
		Size:        7,
		ModTimeUnix: 1_700_000_300,
		ContentHash: strings.Repeat("c", 64),
	}, nil)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if plan.IndexBefore != nil {
		t.Fatalf("IndexBefore = %+v, want nil", plan.IndexBefore)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateAfter)
	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if _, _, _, err := store.GetFileIndex(ctx, "/new.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetFileIndex(after rollback) error = %v, want ErrNotFound", err)
	}
}

func TestWriteMetadataPlanPreservesExistingVersionExactly(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/docs/existing.txt"
	versionHash := strings.Repeat("d", 64)
	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
		path,
		versionHash,
		41,
		int64(1_700_000_400),
		"existing",
	); err != nil {
		t.Fatalf("insert existing version: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        50,
			ModTimeUnix: 1_700_000_500,
			ContentHash: strings.Repeat("e", 64),
		},
		&VersionRecord{
			Path:          path,
			Hash:          versionHash,
			Size:          41,
			CreatedAtUnix: 1_700_000_999,
			Comment:       "must not replace",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if plan.VersionBefore == nil || plan.VersionAfter == nil ||
		*plan.VersionBefore != *plan.VersionAfter ||
		plan.VersionAfter.ID <= 0 ||
		plan.VersionAfter.Size != 41 ||
		plan.VersionAfter.CreatedAtUnix != 1_700_000_400 ||
		plan.VersionAfter.Comment != "existing" {
		t.Fatalf("captured existing version plan = before:%+v after:%+v", plan.VersionBefore, plan.VersionAfter)
	}
	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata() error: %v", err)
	}
	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata() error: %v", err)
	}
	version, err := store.GetVersion(ctx, path, versionHash)
	if err != nil {
		t.Fatalf("GetVersion(preserved) error: %v", err)
	}
	if version.ID != plan.VersionBefore.ID ||
		version.Size != 41 ||
		version.CreatedAt.Unix() != 1_700_000_400 ||
		version.Comment != "existing" {
		t.Fatalf("preserved version = %+v, plan before = %+v", version, plan.VersionBefore)
	}
}

func TestCaptureWriteMetadataPlanRejectsExistingVersionSizeConflict(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/docs/conflicting-version.txt"
	versionHash := strings.Repeat("d", 64)
	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
		path,
		versionHash,
		41,
		int64(1_700_000_400),
		"existing",
	); err != nil {
		t.Fatalf("insert existing version: %v", err)
	}

	_, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        50,
			ModTimeUnix: 1_700_000_500,
			ContentHash: strings.Repeat("e", 64),
		},
		&VersionRecord{
			Path:          path,
			Hash:          versionHash,
			Size:          42,
			CreatedAtUnix: 1_700_000_999,
			Comment:       "must not replace",
		},
	)
	if !errors.Is(err, ErrWriteMetadataConflict) {
		t.Fatalf("CaptureWriteMetadataPlan() error = %v, want ErrWriteMetadataConflict", err)
	}
}

func TestWriteMetadataPlanPreservesNullableLegacyFieldsExactly(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/nullable.txt"
	versionHash := strings.Repeat("f", 64)
	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO files (path, size, mod_time, content_hash) VALUES (?, ?, ?, NULL)`,
		path,
		12,
		int64(1_700_000_510),
	); err != nil {
		t.Fatalf("insert nullable index row: %v", err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, NULL)`,
		path,
		versionHash,
		12,
		int64(1_700_000_520),
	); err != nil {
		t.Fatalf("insert nullable version row: %v", err)
	}

	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        13,
			ModTimeUnix: 1_700_000_530,
			ContentHash: strings.Repeat("e", 64),
		},
		&VersionRecord{
			Path:          path,
			Hash:          versionHash,
			Size:          12,
			CreatedAtUnix: 1_700_000_999,
			Comment:       "ignored",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if plan.IndexBefore == nil ||
		!plan.IndexBefore.ContentHashIsNull ||
		plan.IndexBefore.ContentHash != "" {
		t.Fatalf("nullable IndexBefore = %+v", plan.IndexBefore)
	}
	if plan.VersionBefore == nil ||
		!plan.VersionBefore.CommentIsNull ||
		plan.VersionBefore.Comment != "" ||
		*plan.VersionBefore != *plan.VersionAfter {
		t.Fatalf("nullable version plan = before:%+v after:%+v", plan.VersionBefore, plan.VersionAfter)
	}
	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata() error: %v", err)
	}
	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata() error: %v", err)
	}
	var restoredHash *string
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT content_hash FROM files WHERE path = ?`,
		path,
	).Scan(&restoredHash); err != nil {
		t.Fatalf("read restored nullable index hash: %v", err)
	}
	if restoredHash != nil {
		t.Fatalf("restored nullable index hash = %q, want NULL", *restoredHash)
	}
	var restoredComment *string
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT comment FROM versions WHERE path = ? AND hash = ?`,
		path,
		versionHash,
	).Scan(&restoredComment); err != nil {
		t.Fatalf("read restored nullable version comment: %v", err)
	}
	if restoredComment != nil {
		t.Fatalf("restored nullable version comment = %q, want NULL", *restoredComment)
	}
}

func TestWriteMetadataPlanRejectsNullableVersionDrift(t *testing.T) {
	tests := []struct {
		name          string
		initial       any
		drifted       any
		initialIsNull bool
	}{
		{
			name:          "null to empty string",
			initial:       nil,
			drifted:       "",
			initialIsNull: true,
		},
		{
			name:          "empty string to null",
			initial:       "",
			drifted:       nil,
			initialIsNull: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := setupWriteMetadataStore(t)
			ctx := context.Background()
			path := "/nullable-drift.txt"
			oldHash := strings.Repeat("a", 64)
			newHash := strings.Repeat("b", 64)
			if err := store.UpdateFileIndex(ctx, path, 12, time.Unix(1_700_000_510, 0), oldHash); err != nil {
				t.Fatalf("UpdateFileIndex(before) error: %v", err)
			}
			if _, err := store.db.ExecContext(
				ctx,
				`INSERT INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
				path,
				oldHash,
				12,
				int64(1_700_000_520),
				test.initial,
			); err != nil {
				t.Fatalf("insert version: %v", err)
			}
			plan, err := store.CaptureWriteMetadataPlan(
				ctx,
				FileIndexRecord{
					Path:        path,
					Size:        13,
					ModTimeUnix: 1_700_000_530,
					ContentHash: newHash,
				},
				&VersionRecord{
					Path:          path,
					Hash:          oldHash,
					Size:          12,
					CreatedAtUnix: 1_700_000_999,
					Comment:       "ignored",
				},
			)
			if err != nil {
				t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
			}
			if plan.VersionBefore == nil || plan.VersionBefore.CommentIsNull != test.initialIsNull {
				t.Fatalf("VersionBefore = %+v, initial null = %t", plan.VersionBefore, test.initialIsNull)
			}
			if _, err := store.db.ExecContext(
				ctx,
				`UPDATE versions SET comment = ? WHERE path = ? AND hash = ?`,
				test.drifted,
				path,
				oldHash,
			); err != nil {
				t.Fatalf("drift version comment: %v", err)
			}
			assertWriteMetadataState(t, store, plan, WriteMetadataStateConflict)
			for name, operation := range map[string]func(context.Context, WriteMetadataPlan) error{
				"commit":   store.CommitWriteMetadata,
				"rollback": store.RollbackWriteMetadata,
				"ensure":   store.EnsureWriteMetadataCommitted,
			} {
				if err := operation(ctx, plan); !errors.Is(err, ErrWriteMetadataConflict) {
					t.Fatalf("%s drifted state error = %v, want ErrWriteMetadataConflict", name, err)
				}
			}
		})
	}
}

func TestWriteMetadataPlanRejectsMixedOrDriftedState(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/mixed.txt"
	oldHash := strings.Repeat("1", 64)
	newHash := strings.Repeat("2", 64)
	if err := store.UpdateFileIndex(ctx, path, 10, time.Unix(1_700_000_600, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        20,
			ModTimeUnix: 1_700_000_700,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          10,
			CreatedAtUnix: 1_700_000_650,
			Comment:       "planned",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO versions (path, hash, size, created_at, comment) VALUES (?, ?, ?, ?, ?)`,
		path,
		oldHash,
		10,
		int64(1_700_000_650),
		"planned",
	); err != nil {
		t.Fatalf("insert mixed after-version state: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateConflict)
	for name, operation := range map[string]func(context.Context, WriteMetadataPlan) error{
		"commit":   store.CommitWriteMetadata,
		"rollback": store.RollbackWriteMetadata,
		"ensure":   store.EnsureWriteMetadataCommitted,
	} {
		if err := operation(ctx, plan); !errors.Is(err, ErrWriteMetadataConflict) {
			t.Fatalf("%s mixed state error = %v, want ErrWriteMetadataConflict", name, err)
		}
	}
	size, _, hash, err := store.GetFileIndex(ctx, path)
	if err != nil || size != 10 || hash != oldHash {
		t.Fatalf("mixed-state index changed = (%d, %q, %v)", size, hash, err)
	}
	version, err := store.GetVersion(ctx, path, oldHash)
	if err != nil || version.Comment != "planned" {
		t.Fatalf("mixed-state version changed = (%+v, %v)", version, err)
	}
}

func TestWriteMetadataPlanRejectsAfterIndexWithBeforeVersion(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/mixed-index.txt"
	oldHash := strings.Repeat("1", 64)
	newHash := strings.Repeat("2", 64)
	if err := store.UpdateFileIndex(ctx, path, 10, time.Unix(1_700_000_600, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        20,
			ModTimeUnix: 1_700_000_700,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          10,
			CreatedAtUnix: 1_700_000_650,
			Comment:       "planned",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	if err := store.UpdateFileIndex(
		ctx,
		path,
		plan.IndexAfter.Size,
		time.Unix(plan.IndexAfter.ModTimeUnix, 0),
		plan.IndexAfter.ContentHash,
	); err != nil {
		t.Fatalf("UpdateFileIndex(after) error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateConflict)
	for name, operation := range map[string]func(context.Context, WriteMetadataPlan) error{
		"commit":   store.CommitWriteMetadata,
		"rollback": store.RollbackWriteMetadata,
		"ensure":   store.EnsureWriteMetadataCommitted,
	} {
		if err := operation(ctx, plan); !errors.Is(err, ErrWriteMetadataConflict) {
			t.Fatalf("%s mixed state error = %v, want ErrWriteMetadataConflict", name, err)
		}
	}
}

func TestWriteMetadataPlanClassifiesIdenticalStatesAsBoth(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/both.txt"
	hash := strings.Repeat("2", 64)
	record := FileIndexRecord{
		Path:        path,
		Size:        20,
		ModTimeUnix: 1_700_000_700,
		ContentHash: hash,
	}
	if err := store.UpdateFileIndex(
		ctx,
		path,
		record.Size,
		time.Unix(record.ModTimeUnix, 0),
		record.ContentHash,
	); err != nil {
		t.Fatalf("UpdateFileIndex() error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(ctx, record, nil)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBoth)
	for name, operation := range map[string]func(context.Context, WriteMetadataPlan) error{
		"commit":   store.CommitWriteMetadata,
		"rollback": store.RollbackWriteMetadata,
		"ensure":   store.EnsureWriteMetadataCommitted,
	} {
		if err := operation(ctx, plan); err != nil {
			t.Fatalf("%s identical state error: %v", name, err)
		}
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBoth)
}

func TestClassifyWriteMetadataSnapshotRejectsEveryFieldDrift(t *testing.T) {
	indexAfter := FileIndexRecord{
		Path:        "/field-drift.txt",
		Size:        20,
		ModTimeUnix: 1_700_000_700,
		ContentHash: strings.Repeat("2", 64),
	}
	version := VersionRecord{
		ID:            17,
		Path:          indexAfter.Path,
		Hash:          strings.Repeat("1", 64),
		Size:          10,
		CreatedAtUnix: 1_700_000_650,
		Comment:       "planned",
	}
	plan := WriteMetadataPlan{
		IndexAfter:    indexAfter,
		VersionBefore: &version,
		VersionAfter:  &version,
	}
	tests := []struct {
		name   string
		mutate func(*writeMetadataSnapshot)
	}{
		{"index path", func(snapshot *writeMetadataSnapshot) { snapshot.index.Path = "/other.txt" }},
		{"index size", func(snapshot *writeMetadataSnapshot) { snapshot.index.Size++ }},
		{"index mod time", func(snapshot *writeMetadataSnapshot) { snapshot.index.ModTimeUnix++ }},
		{"index hash", func(snapshot *writeMetadataSnapshot) { snapshot.index.ContentHash = strings.Repeat("3", 64) }},
		{"index hash nullness", func(snapshot *writeMetadataSnapshot) {
			snapshot.index.ContentHash = ""
			snapshot.index.ContentHashIsNull = true
		}},
		{"version id", func(snapshot *writeMetadataSnapshot) { snapshot.version.ID++ }},
		{"version path", func(snapshot *writeMetadataSnapshot) { snapshot.version.Path = "/other.txt" }},
		{"version hash", func(snapshot *writeMetadataSnapshot) { snapshot.version.Hash = strings.Repeat("4", 64) }},
		{"version size", func(snapshot *writeMetadataSnapshot) { snapshot.version.Size++ }},
		{"version created at", func(snapshot *writeMetadataSnapshot) { snapshot.version.CreatedAtUnix++ }},
		{"version comment", func(snapshot *writeMetadataSnapshot) { snapshot.version.Comment = "other" }},
		{"version comment nullness", func(snapshot *writeMetadataSnapshot) {
			snapshot.version.Comment = ""
			snapshot.version.CommentIsNull = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := indexAfter
			currentVersion := version
			snapshot := writeMetadataSnapshot{
				index:   &index,
				version: &currentVersion,
			}
			test.mutate(&snapshot)
			if state := classifyWriteMetadataSnapshot(plan, snapshot); state != WriteMetadataStateConflict {
				t.Fatalf("classifyWriteMetadataSnapshot() = %q, want %q", state, WriteMetadataStateConflict)
			}
		})
	}
}

func TestEqualVersionRecordPlannedInsertRequiresPositiveSQLiteID(t *testing.T) {
	expected := &VersionRecord{
		Path:          "/planned-id.txt",
		Hash:          strings.Repeat("1", 64),
		Size:          10,
		CreatedAtUnix: 1_700_000_650,
		Comment:       "planned",
	}
	for _, test := range []struct {
		name string
		id   int64
		want bool
	}{
		{name: "positive autoincrement id", id: 1, want: true},
		{name: "zero database id", id: 0, want: false},
		{name: "negative database id", id: -1, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := *expected
			actual.ID = test.id
			if got := equalVersionRecord(expected, &actual); got != test.want {
				t.Fatalf("equalVersionRecord(ID=%d) = %t, want %t", test.id, got, test.want)
			}
		})
	}
}

func TestWriteMetadataTransactionRollsBackInjectedCommitAndRollbackFailures(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	path := "/fault.txt"
	oldHash := strings.Repeat("3", 64)
	newHash := strings.Repeat("4", 64)
	if err := store.UpdateFileIndex(ctx, path, 30, time.Unix(1_700_000_800, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        40,
			ModTimeUnix: 1_700_000_900,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          30,
			CreatedAtUnix: 1_700_000_850,
			Comment:       "fault",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	injectedCommit := errors.New("injected commit mutation failure")
	writeMetadataTestHook = func(stage string) error {
		if stage == "after-version-commit-mutation" {
			return injectedCommit
		}
		return nil
	}
	if err := store.CommitWriteMetadata(ctx, plan); !errors.Is(err, injectedCommit) {
		t.Fatalf("CommitWriteMetadata(injected) error = %v, want injected error", err)
	}
	writeMetadataTestHook = nil
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)

	if err := store.CommitWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("CommitWriteMetadata(retry) error: %v", err)
	}
	injectedRollback := errors.New("injected rollback mutation failure")
	writeMetadataTestHook = func(stage string) error {
		if stage == "after-version-rollback-mutation" {
			return injectedRollback
		}
		return nil
	}
	if err := store.RollbackWriteMetadata(ctx, plan); !errors.Is(err, injectedRollback) {
		t.Fatalf("RollbackWriteMetadata(injected) error = %v, want injected error", err)
	}
	writeMetadataTestHook = nil
	assertWriteMetadataState(t, store, plan, WriteMetadataStateAfter)
	if err := store.RollbackWriteMetadata(ctx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata(retry) error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
}

func TestWriteMetadataAmbiguousCommitCanBeDetachedRolledBack(t *testing.T) {
	store := setupWriteMetadataStore(t)
	store.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	path := "/ambiguous-commit.txt"
	oldHash := strings.Repeat("3", 64)
	newHash := strings.Repeat("4", 64)
	if err := store.UpdateFileIndex(ctx, path, 30, time.Unix(1_700_000_800, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        40,
			ModTimeUnix: 1_700_000_900,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          30,
			CreatedAtUnix: 1_700_000_850,
			Comment:       "ambiguous",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	injected := errors.New("commit result lost after sqlite commit")
	writeMetadataCommitTransaction = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return injected
	}
	err = store.CommitWriteMetadata(ctx, plan)
	writeMetadataCommitTransaction = commitWriteMetadataTransaction
	cancel()
	if !errors.Is(err, ErrWriteMetadataOutcomeUnknown) || !errors.Is(err, injected) {
		t.Fatalf("CommitWriteMetadata() error = %v, want unknown outcome and injected error", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateAfter)

	recoveryCtx := context.WithoutCancel(ctx)
	if err := store.RollbackWriteMetadata(recoveryCtx, plan); err != nil {
		t.Fatalf("RollbackWriteMetadata(detached) error: %v", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if err := store.db.PingContext(context.Background()); err != nil {
		t.Fatalf("database connection is not reusable: %v", err)
	}
}

func TestWriteMetadataRollbackCleanupFailureIsOutcomeUnknown(t *testing.T) {
	store := setupWriteMetadataStore(t)
	store.db.SetMaxOpenConns(1)
	ctx := context.Background()
	path := "/ambiguous-rollback.txt"
	oldHash := strings.Repeat("3", 64)
	newHash := strings.Repeat("4", 64)
	if err := store.UpdateFileIndex(ctx, path, 30, time.Unix(1_700_000_800, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        40,
			ModTimeUnix: 1_700_000_900,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          30,
			CreatedAtUnix: 1_700_000_850,
			Comment:       "ambiguous",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	actionFailure := errors.New("injected action failure")
	cleanupFailure := errors.New("rollback result lost after sqlite rollback")
	writeMetadataTestHook = func(stage string) error {
		if stage == "after-version-commit-mutation" {
			return actionFailure
		}
		return nil
	}
	writeMetadataRollbackTransaction = func(tx *sql.Tx) error {
		if err := tx.Rollback(); err != nil {
			return err
		}
		return cleanupFailure
	}
	err = store.CommitWriteMetadata(ctx, plan)
	writeMetadataTestHook = nil
	writeMetadataRollbackTransaction = rollbackWriteMetadataTransaction
	if !errors.Is(err, actionFailure) ||
		!errors.Is(err, cleanupFailure) ||
		!errors.Is(err, ErrWriteMetadataOutcomeUnknown) {
		t.Fatalf("CommitWriteMetadata() error = %v, want action, cleanup, and unknown-outcome errors", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if err := store.db.PingContext(ctx); err != nil {
		t.Fatalf("database connection is not reusable: %v", err)
	}
}

func TestWriteMetadataContextCancellationUsesAutomaticRollback(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	path := "/canceled-during-action.txt"
	oldHash := strings.Repeat("3", 64)
	newHash := strings.Repeat("4", 64)
	if err := store.UpdateFileIndex(ctx, path, 30, time.Unix(1_700_000_800, 0), oldHash); err != nil {
		t.Fatalf("UpdateFileIndex(before) error: %v", err)
	}
	plan, err := store.CaptureWriteMetadataPlan(
		ctx,
		FileIndexRecord{
			Path:        path,
			Size:        40,
			ModTimeUnix: 1_700_000_900,
			ContentHash: newHash,
		},
		&VersionRecord{
			Path:          path,
			Hash:          oldHash,
			Size:          30,
			CreatedAtUnix: 1_700_000_850,
			Comment:       "canceled",
		},
	)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	writeMetadataTestHook = func(stage string) error {
		if stage != "after-version-commit-mutation" {
			return nil
		}
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	err = store.CommitWriteMetadata(ctx, plan)
	writeMetadataTestHook = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitWriteMetadata() error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrWriteMetadataOutcomeUnknown) {
		t.Fatalf("CommitWriteMetadata() error = %v, automatic rollback must be a known before-state", err)
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateBefore)
	if err := store.db.PingContext(context.Background()); err != nil {
		t.Fatalf("database connection is not reusable: %v", err)
	}
}

func TestWriteMetadataPlanConcurrentCommitIsIdempotent(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	plan, err := store.CaptureWriteMetadataPlan(ctx, FileIndexRecord{
		Path:        "/concurrent.txt",
		Size:        1,
		ModTimeUnix: 1_700_001_000,
		ContentHash: strings.Repeat("5", 64),
	}, nil)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	for range workers {
		go func() {
			ready.Done()
			<-start
			errs <- store.CommitWriteMetadata(ctx, plan)
		}()
	}
	ready.Wait()
	close(start)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent CommitWriteMetadata() error: %v", err)
		}
	}
	assertWriteMetadataState(t, store, plan, WriteMetadataStateAfter)
}

func TestWriteMetadataPlanConcurrentCommitAndRollbackRemainCoherent(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx := context.Background()
	plan, err := store.CaptureWriteMetadataPlan(ctx, FileIndexRecord{
		Path:        "/concurrent-reconcile.txt",
		Size:        1,
		ModTimeUnix: 1_700_001_000,
		ContentHash: strings.Repeat("5", 64),
	}, nil)
	if err != nil {
		t.Fatalf("CaptureWriteMetadataPlan() error: %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	for worker := range workers {
		operation := store.CommitWriteMetadata
		if worker%2 != 0 {
			operation = store.RollbackWriteMetadata
		}
		go func() {
			ready.Done()
			<-start
			errs <- operation(ctx, plan)
		}()
	}
	ready.Wait()
	close(start)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent metadata reconciliation error: %v", err)
		}
	}
	state, err := store.InspectWriteMetadata(ctx, plan)
	if err != nil {
		t.Fatalf("InspectWriteMetadata() error: %v", err)
	}
	if state != WriteMetadataStateBefore && state != WriteMetadataStateAfter {
		t.Fatalf("InspectWriteMetadata() = %q, want coherent before or after state", state)
	}
}

func TestWriteMetadataPlanRejectsInvalidInputAndCanceledContext(t *testing.T) {
	store := setupWriteMetadataStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CaptureWriteMetadataPlan(ctx, FileIndexRecord{
		Path:        "/canceled.txt",
		Size:        1,
		ModTimeUnix: 1,
		ContentHash: strings.Repeat("6", 64),
	}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureWriteMetadataPlan(canceled) error = %v, want context.Canceled", err)
	}

	tests := []struct {
		name string
		plan WriteMetadataPlan
	}{
		{
			name: "invalid hash",
			plan: WriteMetadataPlan{IndexAfter: FileIndexRecord{
				Path:        "/bad.txt",
				Size:        1,
				ModTimeUnix: 1,
				ContentHash: "bad",
			}},
		},
		{
			name: "unnormalized path",
			plan: WriteMetadataPlan{IndexAfter: FileIndexRecord{
				Path:        "/docs/../bad.txt",
				Size:        1,
				ModTimeUnix: 1,
				ContentHash: strings.Repeat("7", 64),
			}},
		},
		{
			name: "version removal",
			plan: WriteMetadataPlan{
				IndexAfter: FileIndexRecord{
					Path:        "/bad.txt",
					Size:        1,
					ModTimeUnix: 1,
					ContentHash: strings.Repeat("8", 64),
				},
				VersionBefore: &VersionRecord{
					ID:            1,
					Path:          "/bad.txt",
					Hash:          strings.Repeat("9", 64),
					Size:          1,
					CreatedAtUnix: 1,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateWriteMetadataPlan(test.plan); !errors.Is(err, ErrInvalidWriteMetadataPlan) {
				t.Fatalf("ValidateWriteMetadataPlan() error = %v, want ErrInvalidWriteMetadataPlan", err)
			}
			if err := store.CommitWriteMetadata(context.Background(), test.plan); !errors.Is(err, ErrInvalidWriteMetadataPlan) {
				t.Fatalf("CommitWriteMetadata() error = %v, want ErrInvalidWriteMetadataPlan", err)
			}
		})
	}
}

func assertWriteMetadataState(
	t *testing.T,
	store *Store,
	plan WriteMetadataPlan,
	want WriteMetadataState,
) {
	t.Helper()
	got, err := store.InspectWriteMetadata(context.Background(), plan)
	if err != nil {
		t.Fatalf("InspectWriteMetadata() error: %v", err)
	}
	if got != want {
		t.Fatalf("InspectWriteMetadata() = %q, want %q", got, want)
	}
}
