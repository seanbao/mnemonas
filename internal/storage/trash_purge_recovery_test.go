package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/versionstore"
)

type trashPurgeRecoveryTestFixture struct {
	t      *testing.T
	cfg    Config
	fs     *FileSystem
	client *dataplane.Client
}

func newTrashPurgeRecoveryTestFixture(t *testing.T) *trashPurgeRecoveryTestFixture {
	t.Helper()

	root := t.TempDir()
	fixture := &trashPurgeRecoveryTestFixture{
		t: t,
		cfg: Config{
			FilesRoot:          filepath.Join(root, "files"),
			InternalRoot:       filepath.Join(root, ".mnemonas"),
			TrashRoot:          filepath.Join(root, ".mnemonas", "trash"),
			MaxVersions:        10,
			MaxVersionAge:      30 * 24 * time.Hour,
			TrashRetentionDays: 30,
		},
	}
	fixture.open(t)
	t.Cleanup(func() {
		fixture.close(false)
	})
	return fixture
}

func (fixture *trashPurgeRecoveryTestFixture) open(t *testing.T) {
	t.Helper()

	if fixture.fs != nil || fixture.client != nil {
		t.Fatal("trash purge recovery fixture is already open")
	}
	fixture.client = dataplane.NewClient("unused")
	cfg := fixture.cfg
	cfg.Dataplane = fixture.client
	fs, err := New(&cfg)
	if err != nil {
		_ = fixture.client.Close()
		fixture.client = nil
		t.Fatalf("New() error: %v", err)
	}
	fixture.fs = fs
	fixture.fs.SetTrashParticipantHooks(TrashParticipantHooks{
		ValidatePurge:         func(context.Context, string, string, []byte) error { return nil },
		CompletePurge:         func(context.Context, string, string, []byte) error { return nil },
		RecoveryStateReliable: func() error { return nil },
	})
}

func (fixture *trashPurgeRecoveryTestFixture) close(fatal bool) {
	fixture.t.Helper()

	var closeErr error
	if fixture.fs != nil {
		closeErr = errors.Join(closeErr, fixture.fs.Close())
		fixture.fs = nil
	}
	if fixture.client != nil {
		closeErr = errors.Join(closeErr, fixture.client.Close())
		fixture.client = nil
	}
	if closeErr == nil {
		return
	}
	if fatal {
		fixture.t.Fatalf("close trash purge recovery fixture: %v", closeErr)
	}
	fixture.t.Errorf("close trash purge recovery fixture: %v", closeErr)
}

func (fixture *trashPurgeRecoveryTestFixture) reopen(t *testing.T) {
	t.Helper()
	fixture.close(true)
	fixture.open(t)
}

func seedTrashPurgeRecoveryItem(t *testing.T, fs *FileSystem, id string, isDir bool, files map[string]string) *versionstore.TrashItem {
	t.Helper()

	itemRoot := filepath.Join(fs.trashRoot, id)
	if err := os.MkdirAll(itemRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(Trash item) error: %v", err)
	}

	var size int64
	contentPath := filepath.Join(itemRoot, "content")
	if isDir {
		if err := os.MkdirAll(contentPath, 0o750); err != nil {
			t.Fatalf("MkdirAll(Trash directory content) error: %v", err)
		}
		for rel, contents := range files {
			target := filepath.Join(contentPath, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				t.Fatalf("MkdirAll(%s) error: %v", rel, err)
			}
			if err := os.WriteFile(target, []byte(contents), 0o640); err != nil {
				t.Fatalf("WriteFile(%s) error: %v", rel, err)
			}
			size += int64(len(contents))
		}
	} else {
		contents, ok := files["."]
		if !ok || len(files) != 1 {
			t.Fatal("regular Trash item fixture requires exactly one \".\" content entry")
		}
		if err := os.WriteFile(contentPath, []byte(contents), 0o640); err != nil {
			t.Fatalf("WriteFile(Trash file content) error: %v", err)
		}
		size = int64(len(contents))
	}

	deletedAt := time.Unix(1_700_000_000, 0)
	item := &versionstore.TrashItem{
		ID:           id,
		OriginalPath: "/original-" + id,
		Size:         size,
		DeletedAt:    deletedAt,
		ExpiresAt:    deletedAt.Add(30 * 24 * time.Hour),
		IsDir:        isDir,
		RestoreData:  []byte(`{"fixture":"trash-purge-recovery"}`),
	}
	if err := fs.versions.AddToTrash(context.Background(), item); err != nil {
		t.Fatalf("AddToTrash() error: %v", err)
	}
	return item
}

func prepareTrashPurgeRecoveryCheckpoint(t *testing.T, fs *FileSystem, item *versionstore.TrashItem) *trashPurgeOperation {
	t.Helper()

	operation, err := fs.prepareTrashPurge(context.Background(), item)
	if err != nil {
		t.Fatalf("prepareTrashPurge() error: %v", err)
	}
	return operation
}

func setTrashPurgeRecoveryOperationID(t *testing.T, fs *FileSystem, operation *trashPurgeOperation, operationID string) {
	t.Helper()
	if operation == nil || operation.record.Decision != trashPurgePrepared || !validTrashPurgeOperationID(operationID) {
		t.Fatal("setTrashPurgeRecoveryOperationID() requires a prepared operation and valid operation ID")
	}
	if err := fs.removeTrashPurgeJournalFile(trashPurgePreparedRel(operation.record.OperationID), false); err != nil {
		t.Fatalf("removeTrashPurgeJournalFile(old prepared operation) error: %v", err)
	}
	operation.record.OperationID = operationID
	published, err := fs.publishTrashPurgeJournalRecord(&operation.record)
	if err != nil || !published {
		t.Fatalf("publishTrashPurgeJournalRecord(fixed operation ID) = (%v, %v), want published", published, err)
	}
}

func stageTrashPurgeRecoveryCheckpoint(t *testing.T, fs *FileSystem, operation *trashPurgeOperation) string {
	t.Helper()

	canonicalPath := filepath.Join(fs.trashRoot, operation.record.Item.ID)
	stagePath := filepath.Join(fs.trashRoot, trashPurgeStageRel(operation.record.OperationID))
	if err := fs.movePath(canonicalPath, stagePath); err != nil {
		t.Fatalf("movePath(Trash item to purge stage) error: %v", err)
	}
	return stagePath
}

func commitTrashPurgeRecoveryCheckpoint(t *testing.T, fs *FileSystem, operation *trashPurgeOperation) {
	t.Helper()

	if err := fs.removeTrashMetadata(context.Background(), operation.record.Item.ID); err != nil {
		t.Fatalf("removeTrashMetadata() error: %v", err)
	}
	committed := operation.record
	committed.Decision = trashPurgeCommitted
	published, err := fs.publishTrashPurgeJournalRecord(&committed)
	if err != nil {
		t.Fatalf("publishTrashPurgeJournalRecord(committed) = (published %v, error %v)", published, err)
	}
	if !published {
		t.Fatal("committed Trash purge journal was not published")
	}
	operation.record = committed
}

func requireTrashPurgePathExists(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("Lstat(%s) error: %v", target, err)
	}
}

func requireTrashPurgePathAbsent(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%s) error = %v, want os.ErrNotExist", target, err)
	}
}

func requireTrashPurgeMetadata(t *testing.T, fs *FileSystem, id string, want bool) {
	t.Helper()
	_, err := fs.versions.GetTrashItem(context.Background(), id)
	if want && err != nil {
		t.Fatalf("GetTrashItem(%s) error: %v", id, err)
	}
	if !want && !errors.Is(err, versionstore.ErrNotFound) {
		t.Fatalf("GetTrashItem(%s) error = %v, want ErrNotFound", id, err)
	}
}

func requireSingleBlockedTrashPurge(t *testing.T, report TrashRecoveryReport, err error, operationID string) {
	t.Helper()
	if err == nil {
		t.Fatal("RecoverTrashDeletions() error = nil, want blocking error")
	}
	if report.RolledBack != 0 || report.RolledForward != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want no completed recovery", report)
	}
	if len(report.Blocked) != 1 || report.Blocked[0] != operationID {
		t.Fatalf("RecoverTrashDeletions() blocked = %v, want [%s]", report.Blocked, operationID)
	}
}

func requireBlockedTrashPurges(t *testing.T, report TrashRecoveryReport, err error, operationIDs ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("RecoverTrashDeletions() error = nil, want blocking error")
	}
	if !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RecoverTrashDeletions() error = %v, want ErrTrashRecoveryRequired", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want no completed recovery", report)
	}
	want := uniqueSortedStrings(append([]string(nil), operationIDs...))
	if strings.Join(report.Blocked, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("RecoverTrashDeletions() blocked = %v, want %v", report.Blocked, want)
	}
}

func setTrashPurgeRecoveryItemVersions(t *testing.T, fs *FileSystem, item *versionstore.TrashItem, hashes ...string) {
	t.Helper()
	if err := fs.versions.RemoveFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("RemoveFromTrash(%s) error: %v", item.ID, err)
	}
	item.HadVersions = true
	if err := fs.versions.AddToTrash(context.Background(), item); err != nil {
		t.Fatalf("AddToTrash(%s with versions) error: %v", item.ID, err)
	}
	for index, hash := range hashes {
		if err := fs.versions.AddVersion(context.Background(), item.OriginalPath, hash, int64(index+1), ""); err != nil {
			t.Fatalf("AddVersion(%s, %s) error: %v", item.OriginalPath, hash, err)
		}
	}
}

type sharedVersionTrashPurgeRecoverySetup struct {
	preparedItem       *versionstore.TrashItem
	committedItem      *versionstore.TrashItem
	preparedOperation  *trashPurgeOperation
	committedOperation *trashPurgeOperation
	preparedStagePath  string
	committedStagePath string
	versionPath        string
	versionHash        string
}

func setupSharedVersionTrashPurgeRecovery(
	t *testing.T,
	fixture *trashPurgeRecoveryTestFixture,
	prefix string,
	preparedOriginalPath string,
	preparedIsDir bool,
	committedOriginalPath string,
	versionPath string,
) sharedVersionTrashPurgeRecoverySetup {
	t.Helper()
	preparedFiles := map[string]string{".": "prepared content"}
	if preparedIsDir {
		preparedFiles = map[string]string{"child.txt": "prepared directory content"}
	}
	preparedItem := seedTrashPurgeRecoveryItem(t, fixture.fs, prefix+"-prepared", preparedIsDir, preparedFiles)
	committedItem := seedTrashPurgeRecoveryItem(t, fixture.fs, prefix+"-committed", false, map[string]string{".": "committed content"})
	for _, update := range []struct {
		item         *versionstore.TrashItem
		originalPath string
	}{
		{item: preparedItem, originalPath: preparedOriginalPath},
		{item: committedItem, originalPath: committedOriginalPath},
	} {
		if err := fixture.fs.versions.RemoveFromTrash(context.Background(), update.item.ID); err != nil {
			t.Fatalf("RemoveFromTrash(%s) error: %v", update.item.ID, err)
		}
		update.item.OriginalPath = update.originalPath
		update.item.HadVersions = true
		if err := fixture.fs.versions.AddToTrash(context.Background(), update.item); err != nil {
			t.Fatalf("AddToTrash(%s with shared versions) error: %v", update.item.ID, err)
		}
	}
	versionHash := strings.Repeat("a", 64)
	if err := fixture.fs.versions.AddVersion(context.Background(), versionPath, versionHash, 1, "shared recovery fixture"); err != nil {
		t.Fatalf("AddVersion(%s) error: %v", versionPath, err)
	}

	preparedOperation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, preparedItem)
	setTrashPurgeRecoveryOperationID(t, fixture.fs, preparedOperation, strings.Repeat("f", 32))
	committedOperation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedItem)
	setTrashPurgeRecoveryOperationID(t, fixture.fs, committedOperation, strings.Repeat("0", 32))
	preparedStagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, preparedOperation)
	if err := fixture.fs.versions.RemoveFromTrash(context.Background(), preparedItem.ID); err != nil {
		t.Fatalf("RemoveFromTrash(prepared checkpoint metadata) error: %v", err)
	}
	committedStagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedOperation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, committedOperation)

	return sharedVersionTrashPurgeRecoverySetup{
		preparedItem:       preparedItem,
		committedItem:      committedItem,
		preparedOperation:  preparedOperation,
		committedOperation: committedOperation,
		preparedStagePath:  preparedStagePath,
		committedStagePath: committedStagePath,
		versionPath:        versionPath,
		versionHash:        versionHash,
	}
}

func TestFileSystem_RecoverTrashDeletions_RollsBackPreparedJournalAfterReopen(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "prepared-reopen", false, map[string]string{".": "prepared content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
	preparedJournalPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID))

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one rollback", report)
	}
	restored, err := os.ReadFile(filepath.Join(canonicalPath, "content"))
	if err != nil {
		t.Fatalf("ReadFile(restored Trash content) error: %v", err)
	}
	if string(restored) != "prepared content" {
		t.Fatalf("restored Trash content = %q, want %q", restored, "prepared content")
	}
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathAbsent(t, preparedJournalPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_RecoverTrashDeletions_RestoresMissingMetadataForPreparedJournal(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "prepared-metadata-missing", false, map[string]string{".": "prepared content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
	if err := fixture.fs.versions.RemoveFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("RemoveFromTrash() error: %v", err)
	}
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one rollback", report)
	}
	requireTrashPurgePathExists(t, canonicalPath)
	requireTrashPurgePathAbsent(t, stagePath)
	restored, metadataErr := fixture.fs.versions.GetTrashItem(context.Background(), item.ID)
	if metadataErr != nil {
		t.Fatalf("GetTrashItem(restored) error: %v", metadataErr)
	}
	if !sameTrashPurgeItem(operation.record.Item, trashPurgeJournalItemFromStore(restored)) {
		t.Fatalf("restored Trash metadata = %+v, want journal item %+v", restored, operation.record.Item)
	}
}

func TestFileSystem_RecoverTrashDeletions_RollsForwardCommittedJournalAfterReopen(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "committed-reopen", false, map[string]string{".": "committed content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	preparedJournalPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID))
	committedJournalPath := filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID))

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one roll-forward", report)
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, item.ID))
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathAbsent(t, preparedJournalPath)
	requireTrashPurgePathAbsent(t, committedJournalPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
}

func TestFileSystem_RecoverTrashDeletions_RollsForwardPartiallyRemovedCommittedStage(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "committed-partial", true, map[string]string{
		"already-removed.txt": "removed before recovery",
		"retained/nested.txt": "removed by recovery",
	})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	if err := os.Remove(filepath.Join(stagePath, "content", "already-removed.txt")); err != nil {
		t.Fatalf("Remove(partially deleted Trash file) error: %v", err)
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one partial-stage roll-forward", report)
	}
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
}

func TestFileSystem_RecoverTrashDeletions_RetriesJournaledVersionHashesAfterMetadataCleanup(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "committed-version-retry", false, map[string]string{".": "versioned content"})
	hashes := []string{strings.Repeat("1", 64), strings.Repeat("2", 64)}
	setTrashPurgeRecoveryItemVersions(t, fixture.fs, item, hashes...)
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	if strings.Join(operation.record.VersionHashes, "\x00") != strings.Join(hashes, "\x00") {
		t.Fatalf("prepared VersionHashes = %v, want %v", operation.record.VersionHashes, hashes)
	}
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	if err := fixture.fs.versions.DeleteVersions(context.Background(), item.OriginalPath); err != nil {
		t.Fatalf("DeleteVersions() before recovery error: %v", err)
	}
	fixture.reopen(t)

	firstFailure := errors.New("delete second version object")
	firstCalls := make(map[string]int)
	fixture.fs.deleteVersionObject = func(_ context.Context, hash string) error {
		firstCalls[hash]++
		if hash == hashes[1] {
			return firstFailure
		}
		return nil
	}
	first, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireSingleBlockedTrashPurge(t, first, err, operation.record.OperationID)
	if !errors.Is(err, ErrTrashRecoveryRequired) || !errors.Is(err, firstFailure) {
		t.Fatalf("first RecoverTrashDeletions() error = %v, want recovery and object failures", err)
	}
	for _, hash := range hashes {
		if firstCalls[hash] != 1 {
			t.Fatalf("first recovery deleteVersionObject(%s) calls = %d, want 1", hash, firstCalls[hash])
		}
	}
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID)))
	versions, versionsErr := fixture.fs.versions.GetVersions(context.Background(), item.OriginalPath)
	if versionsErr != nil || len(versions) != 0 {
		t.Fatalf("GetVersions() after first recovery = %+v, %v; want metadata already removed", versions, versionsErr)
	}

	secondCalls := make(map[string]int)
	fixture.fs.deleteVersionObject = func(_ context.Context, hash string) error {
		secondCalls[hash]++
		if hash == hashes[0] {
			return versionstore.ErrNotFound
		}
		return nil
	}
	second, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("second RecoverTrashDeletions() error: %v", err)
	}
	if second.RolledForward != 1 || second.RolledBack != 0 || len(second.Blocked) != 0 {
		t.Fatalf("second RecoverTrashDeletions() report = %+v, want one roll-forward", second)
	}
	for _, hash := range hashes {
		if secondCalls[hash] != 1 {
			t.Fatalf("second recovery deleteVersionObject(%s) calls = %d, want 1", hash, secondCalls[hash])
		}
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful retry left Trash mutation gate blocked: %v", fixture.fs.trashMutationBlocked)
	}
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID)))
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID)))
}

func TestFileSystem_RecoverTrashDeletions_RollsBackAllPreparedOperationsBeforeCommittedVersionCleanup(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		preparedOriginalPath      string
		preparedIsDir             bool
		committedOriginalPath     string
		sharedVersionMetadataPath string
	}{
		{
			name:                      "same original path",
			preparedOriginalPath:      "/shared-recovery.txt",
			committedOriginalPath:     "/shared-recovery.txt",
			sharedVersionMetadataPath: "/shared-recovery.txt",
		},
		{
			name:                      "prepared ancestor",
			preparedOriginalPath:      "/shared-recovery-dir",
			preparedIsDir:             true,
			committedOriginalPath:     "/shared-recovery-dir/child.txt",
			sharedVersionMetadataPath: "/shared-recovery-dir/child.txt",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			setup := setupSharedVersionTrashPurgeRecovery(
				t,
				fixture,
				"shared-order",
				tc.preparedOriginalPath,
				tc.preparedIsDir,
				tc.committedOriginalPath,
				tc.sharedVersionMetadataPath,
			)
			if setup.committedOperation.record.OperationID >= setup.preparedOperation.record.OperationID {
				t.Fatalf(
					"fixed operation order = committed %s prepared %s, want committed to sort first",
					setup.committedOperation.record.OperationID,
					setup.preparedOperation.record.OperationID,
				)
			}
			fixture.reopen(t)
			deletedObjects := 0
			fixture.fs.deleteVersionObject = func(_ context.Context, hash string) error {
				deletedObjects++
				return nil
			}

			report, err := fixture.fs.RecoverTrashDeletions(context.Background())
			if err != nil {
				t.Fatalf("RecoverTrashDeletions() error: %v", err)
			}
			if report.RolledBack != 1 || report.RolledForward != 1 || len(report.Blocked) != 0 {
				t.Fatalf("RecoverTrashDeletions() report = %+v, want one rollback before one roll-forward", report)
			}
			requireTrashPurgeMetadata(t, fixture.fs, setup.preparedItem.ID, true)
			requireTrashPurgeMetadata(t, fixture.fs, setup.committedItem.ID, false)
			requireTrashPurgePathAbsent(t, setup.preparedStagePath)
			requireTrashPurgePathAbsent(t, setup.committedStagePath)
			versions, versionsErr := fixture.fs.versions.GetVersions(context.Background(), setup.versionPath)
			if versionsErr != nil || len(versions) != 1 || versions[0].Hash != setup.versionHash {
				t.Fatalf("shared versions after recovery = %+v, %v; want hash %s", versions, versionsErr, setup.versionHash)
			}
			if deletedObjects != 0 {
				t.Fatalf("deleteVersionObject() calls = %d, want none while prepared Trash item retains shared history", deletedObjects)
			}
		})
	}
}

func TestFileSystem_RecoverTrashDeletions_BlocksCommittedPhaseWhenPreparedRollbackFails(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	setup := setupSharedVersionTrashPurgeRecovery(
		t,
		fixture,
		"shared-block",
		"/shared-block.txt",
		false,
		"/shared-block.txt",
		"/shared-block.txt",
	)
	preparedCanonicalPath := filepath.Join(fixture.fs.trashRoot, setup.preparedItem.ID)
	if err := os.MkdirAll(preparedCanonicalPath, 0o700); err != nil {
		t.Fatalf("MkdirAll(conflicting prepared canonical path) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(preparedCanonicalPath, "content"), []byte("conflict"), 0o600); err != nil {
		t.Fatalf("WriteFile(conflicting prepared canonical content) error: %v", err)
	}
	fixture.reopen(t)
	deletedObjects := 0
	fixture.fs.deleteVersionObject = func(_ context.Context, hash string) error {
		deletedObjects++
		return nil
	}

	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireBlockedTrashPurges(
		t,
		report,
		err,
		setup.preparedOperation.record.OperationID,
		setup.committedOperation.record.OperationID,
	)
	requireTrashPurgePathExists(t, setup.preparedStagePath)
	requireTrashPurgePathExists(t, setup.committedStagePath)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(setup.committedOperation.record.OperationID)))
	requireTrashPurgeMetadata(t, fixture.fs, setup.preparedItem.ID, false)
	requireTrashPurgeMetadata(t, fixture.fs, setup.committedItem.ID, false)
	versions, versionsErr := fixture.fs.versions.GetVersions(context.Background(), setup.versionPath)
	if versionsErr != nil || len(versions) != 1 || versions[0].Hash != setup.versionHash {
		t.Fatalf("shared versions after blocked recovery = %+v, %v; want hash %s", versions, versionsErr, setup.versionHash)
	}
	if deletedObjects != 0 {
		t.Fatalf("deleteVersionObject() calls = %d, want no committed cleanup after prepared rollback failure", deletedObjects)
	}
}

func TestFileSystem_RecoverTrashDeletions_BlocksMismatchedPreparedAndCommittedRecords(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "mismatched-decisions", false, map[string]string{".": "preserved content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	committed := operation.record
	committed.Decision = trashPurgeCommitted
	committed.Item.RestoreData = []byte(`{"different":"committed-record"}`)
	published, err := fixture.fs.publishTrashPurgeJournalRecord(&committed)
	if err != nil || !published {
		t.Fatalf("publishTrashPurgeJournalRecord(mismatched committed) = (%v, %v), want (true, nil)", published, err)
	}

	fixture.reopen(t)
	report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
	requireBlockedTrashPurges(t, report, recoveryErr, operation.record.OperationID)
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID)))
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID)))
	data, readErr := os.ReadFile(filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	if readErr != nil || string(data) != "preserved content" {
		t.Fatalf("canonical Trash content = %q, %v; want preserved content", data, readErr)
	}
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_RecoverTrashDeletions_BlocksMultipleOperationsForSameTrashItem(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "duplicate-trash-id", false, map[string]string{".": "preserved content"})
	first := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	second := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	if first.record.OperationID == second.record.OperationID {
		t.Fatal("prepareTrashPurge() generated duplicate operation IDs")
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireBlockedTrashPurges(t, report, err, first.record.OperationID, second.record.OperationID)
	for _, operationID := range []string{first.record.OperationID, second.record.OperationID} {
		requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operationID)))
	}
	data, readErr := os.ReadFile(filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	if readErr != nil || string(data) != "preserved content" {
		t.Fatalf("canonical Trash content = %q, %v; want preserved content", data, readErr)
	}
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_RecoverTrashDeletions_BlocksOrphanRecognizedStage(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	if err := fixture.fs.ensureTrashPurgeJournalDir(); err != nil {
		t.Fatalf("ensureTrashPurgeJournalDir() error: %v", err)
	}
	operationID := strings.Repeat("c", 32)
	stagePath := filepath.Join(fixture.fs.trashRoot, trashPurgeStageRel(operationID))
	sentinelPath := filepath.Join(stagePath, "sentinel")
	if err := os.MkdirAll(stagePath, 0o700); err != nil {
		t.Fatalf("MkdirAll(orphan Trash purge stage) error: %v", err)
	}
	if err := os.WriteFile(sentinelPath, []byte("orphan content"), 0o600); err != nil {
		t.Fatalf("WriteFile(orphan Trash purge stage) error: %v", err)
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireBlockedTrashPurges(t, report, err, operationID)
	if len(report.UntrackedPaths) != 1 || report.UntrackedPaths[0] != stagePath {
		t.Fatalf("RecoverTrashDeletions() untracked = %v, want [%s]", report.UntrackedPaths, stagePath)
	}
	data, readErr := os.ReadFile(sentinelPath)
	if readErr != nil || string(data) != "orphan content" {
		t.Fatalf("orphan stage sentinel = %q, %v; want preserved content", data, readErr)
	}
}

func TestFileSystem_RecoverTrashDeletions_BlocksCrossedFilenameAndPayloadOperationIDs(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	firstItem := seedTrashPurgeRecoveryItem(t, fixture.fs, "crossed-first", false, map[string]string{".": "first content"})
	secondItem := seedTrashPurgeRecoveryItem(t, fixture.fs, "crossed-second", false, map[string]string{".": "second content"})
	first := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, firstItem)
	second := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, secondItem)

	firstPayload := first.record
	firstPayload.OperationID = second.record.OperationID
	secondPayload := second.record
	secondPayload.OperationID = first.record.OperationID
	for _, replacement := range []struct {
		path   string
		record trashPurgeJournalRecord
	}{
		{path: filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(first.record.OperationID)), record: firstPayload},
		{path: filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(second.record.OperationID)), record: secondPayload},
	} {
		encoded, err := json.Marshal(replacement.record)
		if err != nil {
			t.Fatalf("json.Marshal(crossed journal) error: %v", err)
		}
		if err := os.WriteFile(replacement.path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatalf("WriteFile(crossed journal) error: %v", err)
		}
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireBlockedTrashPurges(t, report, err, first.record.OperationID, second.record.OperationID)
	for _, item := range []*versionstore.TrashItem{firstItem, secondItem} {
		requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
		requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	}
}

func TestFileSystem_RecoverTrashDeletions_UsesGlobalScanBarrierBeforeMutatingValidOperation(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "scan-barrier-valid", false, map[string]string{".": "must remain staged"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
	preparedPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID))
	poisonedOperationID := strings.Repeat("d", 32)
	poisonedPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(poisonedOperationID))
	if err := os.WriteFile(poisonedPath, []byte("{not-json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(poisoned recognized journal) error: %v", err)
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err == nil || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("RecoverTrashDeletions() error = %v, want ErrTrashRecoveryRequired", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, scan failure must block all recovery mutations", report)
	}
	poisonedBlocked := false
	for _, operationID := range report.Blocked {
		if operationID == poisonedOperationID {
			poisonedBlocked = true
			break
		}
	}
	if !poisonedBlocked {
		t.Fatalf("RecoverTrashDeletions() blocked = %v, want poisoned operation %s to be reported", report.Blocked, poisonedOperationID)
	}
	requireTrashPurgePathAbsent(t, canonicalPath)
	requireTrashPurgePathExists(t, stagePath)
	requireTrashPurgePathExists(t, preparedPath)
	requireTrashPurgePathExists(t, poisonedPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_ExecuteTrashPurge_RollsBackWithNonCanceledContext(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "canceled-rollback", false, map[string]string{".": "rollback content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	ctx, cancel := context.WithCancel(context.Background())
	metadataFailure := errors.New("remove metadata after cancellation")
	fixture.fs.removeTrashMetadata = func(callCtx context.Context, id string) error {
		if id != item.ID {
			t.Fatalf("removeTrashMetadata() id = %q, want %q", id, item.ID)
		}
		cancel()
		if !errors.Is(callCtx.Err(), context.Canceled) {
			t.Fatalf("removeTrashMetadata() context error = %v, want context.Canceled", callCtx.Err())
		}
		return metadataFailure
	}

	committed, err := fixture.fs.executeTrashPurge(ctx, operation)
	if committed {
		t.Fatal("executeTrashPurge() committed = true, want rollback")
	}
	if !errors.Is(err, metadataFailure) {
		t.Fatalf("executeTrashPurge() error = %v, want metadata failure", err)
	}
	if errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("executeTrashPurge() error = %v, rollback should not require recovery", err)
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful rollback left Trash mutation gate blocked: %v", fixture.fs.trashMutationBlocked)
	}
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgeStageRel(operation.record.OperationID)))
	requireTrashPurgePathAbsent(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID)))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_ExecuteTrashPurge_BlocksMutationsWhenRollbackCannotBeVerified(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "runtime-rollback-blocked", false, map[string]string{".": "staged content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
	stagePath := filepath.Join(fixture.fs.trashRoot, trashPurgeStageRel(operation.record.OperationID))
	preparedPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID))
	committedPath := filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID))
	metadataFailure := errors.New("metadata mutation failed")
	fixture.fs.removeTrashMetadata = func(_ context.Context, id string) error {
		if id != item.ID {
			t.Fatalf("removeTrashMetadata() id = %q, want %q", id, item.ID)
		}
		if err := os.MkdirAll(canonicalPath, 0o700); err != nil {
			t.Fatalf("MkdirAll(conflicting canonical path) error: %v", err)
		}
		if err := os.WriteFile(filepath.Join(canonicalPath, "content"), []byte("conflicting content"), 0o600); err != nil {
			t.Fatalf("WriteFile(conflicting canonical content) error: %v", err)
		}
		return metadataFailure
	}

	committed, err := fixture.fs.executeTrashPurge(context.Background(), operation)
	if committed {
		t.Fatal("executeTrashPurge() committed = true, want unresolved rollback")
	}
	if !errors.Is(err, metadataFailure) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("executeTrashPurge() error = %v, want metadata and recovery failures", err)
	}
	var recoveryErr *TrashRecoveryRequiredError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("executeTrashPurge() error = %v, want TrashRecoveryRequiredError", err)
	}
	if recoveryErr.OperationID != operation.record.OperationID || recoveryErr.StagePath != stagePath {
		t.Fatalf("TrashRecoveryRequiredError = %+v, want operation %s and stage %s", recoveryErr, operation.record.OperationID, stagePath)
	}
	wantJournals := []string{preparedPath, committedPath}
	if strings.Join(recoveryErr.JournalPaths, "\x00") != strings.Join(wantJournals, "\x00") {
		t.Fatalf("TrashRecoveryRequiredError.JournalPaths = %v, want %v", recoveryErr.JournalPaths, wantJournals)
	}
	for _, want := range append([]string{operation.record.OperationID, stagePath}, wantJournals...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("executeTrashPurge() error %q does not identify recovery path %q", err, want)
		}
	}

	blockedErr := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
	if !errors.Is(blockedErr, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() after unresolved rollback error = %v, want ErrTrashRecoveryRequired", blockedErr)
	}
	requireTrashPurgePathExists(t, stagePath)
	requireTrashPurgePathExists(t, preparedPath)
	data, readErr := os.ReadFile(filepath.Join(canonicalPath, "content"))
	if readErr != nil || string(data) != "conflicting content" {
		t.Fatalf("conflicting canonical content = %q, %v; want preserved content", data, readErr)
	}
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
}

func TestFileSystem_ExecuteTrashPurge_BlocksUntilCommittedDecisionDirectoryIsDurable(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "committed-decision-sync", false, map[string]string{".": "committed content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	operationID := operation.record.OperationID
	canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
	stagePath := filepath.Join(fixture.fs.trashRoot, trashPurgeStageRel(operationID))
	preparedPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operationID))
	committedPath := filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operationID))

	originalSyncManagedStorageDir := syncManagedStorageDir
	failDecisionSync := true
	decisionSyncFailures := 0
	syncFailure := errors.New("committed decision directory sync failed")
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if failDecisionSync && root == fixture.fs.trashRootHandle && relName == trashPurgeJournalDir {
			if _, err := root.Lstat(trashPurgeCommittedRel(operationID)); err == nil {
				decisionSyncFailures++
				return syncFailure
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	committed, err := fixture.fs.executeTrashPurge(context.Background(), operation)
	if committed {
		t.Fatal("executeTrashPurge() committed = true, want unknown outcome until decision directory sync succeeds")
	}
	if !errors.Is(err, syncFailure) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("executeTrashPurge() error = %v, want sync failure and ErrTrashRecoveryRequired", err)
	}
	var recoveryRequiredErr *TrashRecoveryRequiredError
	if !errors.As(err, &recoveryRequiredErr) || recoveryRequiredErr.OperationID != operationID {
		t.Fatalf("executeTrashPurge() error = %v, want operation-specific TrashRecoveryRequiredError", err)
	}
	if operation.record.Decision != trashPurgeCommitted {
		t.Fatalf("operation decision = %q, want %q after committed journal rename", operation.record.Decision, trashPurgeCommitted)
	}
	if decisionSyncFailures != 1 {
		t.Fatalf("committed decision sync failures = %d, want 1 during execution", decisionSyncFailures)
	}
	requireTrashPurgePathAbsent(t, canonicalPath)
	requireTrashPurgePathExists(t, stagePath)
	requireTrashPurgePathExists(t, preparedPath)
	requireTrashPurgePathExists(t, committedPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)

	report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
	requireSingleBlockedTrashPurge(t, report, recoveryErr, operationID)
	if decisionSyncFailures != 2 {
		t.Fatalf("committed decision sync failures = %d, want recovery to retry the directory sync before mutation", decisionSyncFailures)
	}
	requireTrashPurgePathExists(t, stagePath)
	requireTrashPurgePathExists(t, preparedPath)
	requireTrashPurgePathExists(t, committedPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)

	failDecisionSync = false
	report, recoveryErr = fixture.fs.RecoverTrashDeletions(context.Background())
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashDeletions() after restored sync error: %v", recoveryErr)
	}
	if report.RolledForward != 1 || report.RolledBack != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one roll-forward", report)
	}
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathAbsent(t, preparedPath)
	requireTrashPurgePathAbsent(t, committedPath)
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful recovery left Trash mutation gate blocked: %v", fixture.fs.trashMutationBlocked)
	}
}

func TestFileSystem_ExecuteTrashPurge_RetainsPreparedJournalUntilCanonicalRollbackIsDurable(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "canonical-rollback-sync", false, map[string]string{".": "rollback content"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	operationID := operation.record.OperationID
	canonicalRel := filepath.FromSlash(item.ID)
	stageRel := trashPurgeStageRel(operationID)
	canonicalPath := filepath.Join(fixture.fs.trashRoot, canonicalRel)
	stagePath := filepath.Join(fixture.fs.trashRoot, stageRel)
	preparedPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operationID))

	originalSyncManagedStorageDir := syncManagedStorageDir
	stageSyncFailed := false
	canonicalSyncFailures := 0
	stageSyncFailure := errors.New("stage rename directory sync failed")
	canonicalSyncFailure := errors.New("canonical rollback directory sync failed")
	syncManagedStorageDir = func(root *os.Root, relName, absPath string) error {
		if root != fixture.fs.trashRootHandle {
			return originalSyncManagedStorageDir(root, relName, absPath)
		}
		if !stageSyncFailed && relName == trashPurgeJournalDir {
			_, canonicalErr := root.Lstat(canonicalRel)
			_, stageErr := root.Lstat(stageRel)
			if errors.Is(canonicalErr, os.ErrNotExist) && stageErr == nil {
				stageSyncFailed = true
				return stageSyncFailure
			}
		}
		if stageSyncFailed && relName == "." {
			_, canonicalErr := root.Lstat(canonicalRel)
			_, stageErr := root.Lstat(stageRel)
			if canonicalErr == nil && errors.Is(stageErr, os.ErrNotExist) {
				canonicalSyncFailures++
				return canonicalSyncFailure
			}
		}
		return originalSyncManagedStorageDir(root, relName, absPath)
	}
	t.Cleanup(func() {
		syncManagedStorageDir = originalSyncManagedStorageDir
	})

	committed, err := fixture.fs.executeTrashPurge(context.Background(), operation)
	if committed {
		t.Fatal("executeTrashPurge() committed = true, want rollback outcome")
	}
	if !errors.Is(err, stageSyncFailure) || !errors.Is(err, canonicalSyncFailure) || !errors.Is(err, ErrTrashRecoveryRequired) {
		t.Fatalf("executeTrashPurge() error = %v, want stage sync, canonical rollback sync, and recovery errors", err)
	}
	if !stageSyncFailed || canonicalSyncFailures != 2 {
		t.Fatalf("sync injections = stage:%v canonical:%d, want stage:true canonical:2", stageSyncFailed, canonicalSyncFailures)
	}
	requireTrashPurgePathExists(t, canonicalPath)
	requireTrashPurgePathAbsent(t, stagePath)
	requireTrashPurgePathExists(t, preparedPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	if blockedErr := fixture.fs.DeleteFromTrash(context.Background(), item.ID); !errors.Is(blockedErr, ErrTrashRecoveryRequired) {
		t.Fatalf("DeleteFromTrash() after unresolved canonical sync = %v, want ErrTrashRecoveryRequired", blockedErr)
	}

	syncManagedStorageDir = originalSyncManagedStorageDir
	report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
	if recoveryErr != nil {
		t.Fatalf("RecoverTrashDeletions() after restored sync error: %v", recoveryErr)
	}
	if report.RolledBack != 1 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want one rollback", report)
	}
	requireTrashPurgePathExists(t, canonicalPath)
	requireTrashPurgePathAbsent(t, preparedPath)
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("successful recovery left Trash mutation gate blocked: %v", fixture.fs.trashMutationBlocked)
	}
}

func TestFileSystem_DeleteFromTrash_RejectsInvalidUTF8ManifestPathBeforeMutation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("persistent Trash purge identity is supported only on Linux and macOS")
	}
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "invalid-utf8-manifest", true, map[string]string{"valid.txt": "valid"})
	invalidName := string([]byte{0xff})
	invalidContent := []byte("invalid name content")
	invalidPath := filepath.Join(fixture.fs.trashRoot, item.ID, "content", invalidName)
	if err := os.WriteFile(invalidPath, invalidContent, 0o640); err != nil {
		t.Fatalf("WriteFile(invalid UTF-8 Trash child) error: %v", err)
	}
	if err := fixture.fs.versions.RemoveFromTrash(context.Background(), item.ID); err != nil {
		t.Fatalf("RemoveFromTrash(fixture metadata) error: %v", err)
	}
	item.Size += int64(len(invalidContent))
	if err := fixture.fs.versions.AddToTrash(context.Background(), item); err != nil {
		t.Fatalf("AddToTrash(updated fixture metadata) error: %v", err)
	}

	err := fixture.fs.DeleteFromTrash(context.Background(), item.ID)
	if err == nil || !strings.Contains(err.Error(), "invalid Trash purge manifest path") {
		t.Fatalf("DeleteFromTrash() error = %v, want invalid manifest path rejection", err)
	}
	data, readErr := os.ReadFile(invalidPath)
	if readErr != nil || string(data) != string(invalidContent) {
		t.Fatalf("invalid UTF-8 Trash child = %q, %v; want unchanged content", data, readErr)
	}
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID, "content", "valid.txt"))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	journalEntries, journalErr := os.ReadDir(filepath.Join(fixture.fs.trashRoot, trashPurgeJournalDir))
	if journalErr != nil && !errors.Is(journalErr, os.ErrNotExist) {
		t.Fatalf("ReadDir(Trash purge journal) error: %v", journalErr)
	}
	if len(journalEntries) != 0 {
		t.Fatalf("Trash purge journal entries after invalid UTF-8 rejection = %v, want none", journalEntries)
	}
	if fixture.fs.trashMutationBlocked != nil {
		t.Fatalf("validation rejection left Trash mutation gate blocked: %v", fixture.fs.trashMutationBlocked)
	}
}

func TestFileSystem_PrepareTrashPurge_GloballySortsManifest(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "globally-sorted-manifest", true, map[string]string{
		"a/z.txt": "nested",
		"a.txt":   "sibling",
	})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	got := make([]string, 0, len(operation.record.Manifest))
	for _, entry := range operation.record.Manifest {
		got = append(got, entry.Path)
	}
	want := []string{".", "content", "content/a", "content/a.txt", "content/a/z.txt"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("Trash purge manifest paths = %v, want globally sorted %v", got, want)
	}
	if err := validateTrashPurgeJournalRecord(&operation.record, trashPurgePrepared); err != nil {
		t.Fatalf("validateTrashPurgeJournalRecord() error: %v", err)
	}
	if err := fixture.fs.rollbackPreparedTrashPurge(context.Background(), &operation.record); err != nil {
		t.Fatalf("rollbackPreparedTrashPurge() error: %v", err)
	}
}

func TestFileSystem_RecoverTrashDeletions_BlocksUnsafePreparedState(t *testing.T) {
	t.Run("stage replaced", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		item := seedTrashPurgeRecoveryItem(t, fixture.fs, "prepared-replaced", false, map[string]string{".": "original stage"})
		operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
		stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
		if err := os.RemoveAll(stagePath); err != nil {
			t.Fatalf("RemoveAll(original purge stage) error: %v", err)
		}
		if err := os.MkdirAll(stagePath, 0o700); err != nil {
			t.Fatalf("MkdirAll(replacement purge stage) error: %v", err)
		}
		if err := os.WriteFile(filepath.Join(stagePath, "content"), []byte("replacement!"), 0o640); err != nil {
			t.Fatalf("WriteFile(replacement purge stage) error: %v", err)
		}

		fixture.reopen(t)
		report, err := fixture.fs.RecoverTrashDeletions(context.Background())
		requireSingleBlockedTrashPurge(t, report, err, operation.record.OperationID)
		data, readErr := os.ReadFile(filepath.Join(stagePath, "content"))
		if readErr != nil || string(data) != "replacement!" {
			t.Fatalf("replacement purge stage = %q, %v; want preserved replacement", data, readErr)
		}
		requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID)))
		requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	})

	t.Run("canonical target occupied", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		item := seedTrashPurgeRecoveryItem(t, fixture.fs, "prepared-occupied", false, map[string]string{".": "staged original"})
		operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
		stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
		canonicalPath := filepath.Join(fixture.fs.trashRoot, item.ID)
		if err := os.MkdirAll(canonicalPath, 0o700); err != nil {
			t.Fatalf("MkdirAll(occupied canonical target) error: %v", err)
		}
		if err := os.WriteFile(filepath.Join(canonicalPath, "content"), []byte("new canonical content"), 0o640); err != nil {
			t.Fatalf("WriteFile(occupied canonical target) error: %v", err)
		}

		fixture.reopen(t)
		report, err := fixture.fs.RecoverTrashDeletions(context.Background())
		requireSingleBlockedTrashPurge(t, report, err, operation.record.OperationID)
		requireTrashPurgePathExists(t, stagePath)
		data, readErr := os.ReadFile(filepath.Join(canonicalPath, "content"))
		if readErr != nil || string(data) != "new canonical content" {
			t.Fatalf("occupied canonical content = %q, %v; want preserved target", data, readErr)
		}
		requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID)))
		requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
	})
}

func TestFileSystem_RecoverTrashDeletions_DoesNotDeleteCommittedStageWithUnmanifestedEntry(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	item := seedTrashPurgeRecoveryItem(t, fixture.fs, "committed-extra", true, map[string]string{"expected.txt": "expected"})
	operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
	stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
	extraPath := filepath.Join(stagePath, "unexpected.txt")
	if err := os.WriteFile(extraPath, []byte("must survive"), 0o600); err != nil {
		t.Fatalf("WriteFile(unmanifested stage entry) error: %v", err)
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	requireSingleBlockedTrashPurge(t, report, err, operation.record.OperationID)
	requireTrashPurgePathExists(t, filepath.Join(stagePath, "content", "expected.txt"))
	data, readErr := os.ReadFile(extraPath)
	if readErr != nil || string(data) != "must survive" {
		t.Fatalf("unmanifested stage entry = %q, %v; want preserved entry", data, readErr)
	}
	requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, trashPurgeCommittedRel(operation.record.OperationID)))
	requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
}

func TestFileSystem_RecoverTrashDeletions_IsIdempotent(t *testing.T) {
	for _, decision := range []string{trashPurgePrepared, trashPurgeCommitted} {
		t.Run(decision, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			item := seedTrashPurgeRecoveryItem(t, fixture.fs, "idempotent-"+decision, false, map[string]string{".": decision + " content"})
			operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
			stagePath := stageTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
			if decision == trashPurgeCommitted {
				commitTrashPurgeRecoveryCheckpoint(t, fixture.fs, operation)
			}
			fixture.reopen(t)

			first, err := fixture.fs.RecoverTrashDeletions(context.Background())
			if err != nil {
				t.Fatalf("first RecoverTrashDeletions() error: %v", err)
			}
			if decision == trashPurgePrepared && first.RolledBack != 1 {
				t.Fatalf("first RecoverTrashDeletions() report = %+v, want one rollback", first)
			}
			if decision == trashPurgeCommitted && first.RolledForward != 1 {
				t.Fatalf("first RecoverTrashDeletions() report = %+v, want one roll-forward", first)
			}
			second, err := fixture.fs.RecoverTrashDeletions(context.Background())
			if err != nil {
				t.Fatalf("second RecoverTrashDeletions() error: %v", err)
			}
			if second.RolledBack != 0 || second.RolledForward != 0 || len(second.Blocked) != 0 || len(second.UntrackedPaths) != 0 {
				t.Fatalf("second RecoverTrashDeletions() report = %+v, want no work", second)
			}
			if decision == trashPurgePrepared {
				requireTrashPurgePathExists(t, filepath.Join(fixture.fs.trashRoot, item.ID))
				requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
			} else {
				requireTrashPurgePathAbsent(t, stagePath)
				requireTrashPurgeMetadata(t, fixture.fs, item.ID, false)
			}
		})
	}
}

func TestFileSystem_RecoverTrashDeletions_ReportsUntrackedLegacyDeletingEntryWithoutDeleting(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	legacyPath := filepath.Join(fixture.fs.trashRoot, trashPurgeJournalDir, "legacy-trash-deadbeef")
	sentinelPath := filepath.Join(legacyPath, "sentinel")
	if err := os.MkdirAll(legacyPath, 0o700); err != nil {
		t.Fatalf("MkdirAll(legacy deleting entry) error: %v", err)
	}
	if err := os.WriteFile(sentinelPath, []byte("legacy content"), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy deleting sentinel) error: %v", err)
	}

	fixture.reopen(t)
	report, err := fixture.fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 0 || len(report.Blocked) != 0 {
		t.Fatalf("RecoverTrashDeletions() report = %+v, want only untracked entry", report)
	}
	if len(report.UntrackedPaths) != 1 || report.UntrackedPaths[0] != legacyPath {
		t.Fatalf("RecoverTrashDeletions() untracked = %v, want [%s]", report.UntrackedPaths, legacyPath)
	}
	data, readErr := os.ReadFile(sentinelPath)
	if readErr != nil || string(data) != "legacy content" {
		t.Fatalf("legacy deleting sentinel = %q, %v; want preserved content", data, readErr)
	}
}

func TestTrashPurgeJournalPathAndJSONValidation(t *testing.T) {
	operationID := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name string
		file string
		ok   bool
	}{
		{name: "prepared", file: "purge-" + operationID + ".prepared.json", ok: true},
		{name: "committed", file: "purge-" + operationID + ".committed.json", ok: true},
		{name: "stage is not a journal", file: "purge-" + operationID + ".item"},
		{name: "traversal", file: "../purge-" + operationID + ".prepared.json"},
		{name: "nested", file: "nested/purge-" + operationID + ".prepared.json"},
		{name: "backslash", file: `nested\purge-` + operationID + ".prepared.json"},
		{name: "non hex operation ID", file: "purge-" + strings.Repeat("g", 32) + ".prepared.json"},
		{name: "short operation ID", file: "purge-aaaa.prepared.json"},
	} {
		t.Run("name_"+tc.name, func(t *testing.T) {
			gotID, _, gotOK := parseTrashPurgeJournalName(tc.file)
			if gotOK != tc.ok {
				t.Fatalf("parseTrashPurgeJournalName(%q) ok = %v, want %v", tc.file, gotOK, tc.ok)
			}
			if tc.ok && gotID != operationID {
				t.Fatalf("parseTrashPurgeJournalName(%q) ID = %q, want %q", tc.file, gotID, operationID)
			}
		})
	}

	t.Run("rooted read rejects traversal", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		outsidePath := filepath.Join(filepath.Dir(fixture.fs.trashRoot), "outside-journal")
		if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
			t.Fatalf("WriteFile(outside journal) error: %v", err)
		}
		if _, err := fixture.fs.readTrashPurgeJournalRecord(filepath.Join("..", "outside-journal"), trashPurgePrepared); err == nil {
			t.Fatal("readTrashPurgeJournalRecord(traversal) error = nil")
		}
		data, err := os.ReadFile(outsidePath)
		if err != nil || string(data) != "outside" {
			t.Fatalf("outside journal = %q, %v; want unchanged", data, err)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(record trashPurgeJournalRecord, encoded []byte) []byte
	}{
		{
			name: "manifest traversal",
			mutate: func(record trashPurgeJournalRecord, _ []byte) []byte {
				record.Manifest = append([]trashPurgeManifestEntry(nil), record.Manifest...)
				record.Manifest[1].Path = "../outside"
				encoded, err := json.Marshal(record)
				if err != nil {
					t.Fatalf("json.Marshal(invalid manifest fixture) error: %v", err)
				}
				return encoded
			},
		},
		{
			name: "manifest parent directory",
			mutate: func(record trashPurgeJournalRecord, _ []byte) []byte {
				record.Manifest = append([]trashPurgeManifestEntry(nil), record.Manifest...)
				record.Manifest[1].Path = ".."
				encoded, err := json.Marshal(record)
				if err != nil {
					t.Fatalf("json.Marshal(parent directory manifest fixture) error: %v", err)
				}
				return encoded
			},
		},
		{
			name: "unknown JSON field",
			mutate: func(_ trashPurgeJournalRecord, encoded []byte) []byte {
				return append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...)
			},
		},
		{
			name: "trailing JSON value",
			mutate: func(_ trashPurgeJournalRecord, encoded []byte) []byte {
				return append(encoded, []byte("\n{}")...)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTrashPurgeRecoveryTestFixture(t)
			item := seedTrashPurgeRecoveryItem(t, fixture.fs, "invalid-journal", false, map[string]string{".": "safe content"})
			operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)
			encoded, err := json.Marshal(operation.record)
			if err != nil {
				t.Fatalf("json.Marshal(valid journal fixture) error: %v", err)
			}
			journalPath := filepath.Join(fixture.fs.trashRoot, trashPurgePreparedRel(operation.record.OperationID))
			if err := os.WriteFile(journalPath, tc.mutate(operation.record, encoded), 0o600); err != nil {
				t.Fatalf("WriteFile(invalid journal fixture) error: %v", err)
			}
			fixture.reopen(t)

			report, recoveryErr := fixture.fs.RecoverTrashDeletions(context.Background())
			requireSingleBlockedTrashPurge(t, report, recoveryErr, operation.record.OperationID)
			requireTrashPurgePathExists(t, journalPath)
			data, readErr := os.ReadFile(filepath.Join(fixture.fs.trashRoot, item.ID, "content"))
			if readErr != nil || string(data) != "safe content" {
				t.Fatalf("canonical Trash content = %q, %v; want preserved content", data, readErr)
			}
			requireTrashPurgeMetadata(t, fixture.fs, item.ID, true)
		})
	}

	t.Run("invalid UTF-8 semantic strings", func(t *testing.T) {
		fixture := newTrashPurgeRecoveryTestFixture(t)
		item := seedTrashPurgeRecoveryItem(t, fixture.fs, "invalid-utf8-record", false, map[string]string{".": "safe content"})
		operation := prepareTrashPurgeRecoveryCheckpoint(t, fixture.fs, item)

		invalidOriginalPath := operation.record
		invalidOriginalPath.Item.OriginalPath = "/" + string([]byte{0xff})
		if err := validateTrashPurgeJournalRecord(&invalidOriginalPath, trashPurgePrepared); err == nil {
			t.Fatal("validateTrashPurgeJournalRecord(invalid UTF-8 original path) error = nil")
		}

		invalidManifestPath := operation.record
		invalidManifestPath.Manifest = append([]trashPurgeManifestEntry(nil), operation.record.Manifest...)
		invalidManifestPath.Manifest[1].Path = string([]byte{0xff})
		if err := validateTrashPurgeJournalRecord(&invalidManifestPath, trashPurgePrepared); err == nil {
			t.Fatal("validateTrashPurgeJournalRecord(invalid UTF-8 manifest path) error = nil")
		}

		if err := fixture.fs.rollbackPreparedTrashPurge(context.Background(), &operation.record); err != nil {
			t.Fatalf("rollbackPreparedTrashPurge() error: %v", err)
		}
	})
}
