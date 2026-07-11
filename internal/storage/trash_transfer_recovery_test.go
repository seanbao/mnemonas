package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

func completeDeleteParticipantForTest(context.Context, string, string, []byte) error {
	return nil
}

func completeRestoreParticipantForTest(context.Context, string, string, string, []byte) error {
	return nil
}

func validTrashTransferJournalRecordForTest(kind, decision string) trashTransferJournalRecord {
	record := trashTransferJournalRecord{
		Version:            trashTransferJournalVersion,
		Decision:           decision,
		Kind:               kind,
		OperationID:        strings.Repeat("a", 32),
		FilesRootIdentity:  strings.Repeat("f", 64),
		TrashRootIdentity:  strings.Repeat("0", 64),
		Item:               trashPurgeJournalItem{ID: "trash-transfer", OriginalPath: "/docs/report.txt", Size: 7, DeletedAtUnix: 1, ExpiresAtUnix: 2},
		WorkspaceStagePath: "/docs/.mnemonas-trash-transfer-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.stage",
		SourceManifest: []trashTransferManifestEntry{
			{Path: ".", Kind: "file", Mode: 0o640, Size: 7, ModTimeUnixNano: 3, Identity: strings.Repeat("b", 64), ContentHash: strings.Repeat("c", 64)},
		},
		ParticipantPayload: []byte(`{"shares":[],"favorites":[]}`),
	}
	record.Item.RestoreData = append([]byte(nil), record.ParticipantPayload...)
	if kind == trashTransferDeleteToTrash {
		record.TrashStagePath = ".transactions/transfer-" + record.OperationID + ".item"
	} else {
		record.DestinationPath = "/restored/report.txt"
		record.WorkspaceStagePath = "/restored/.mnemonas-trash-transfer-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.stage"
		record.TrashItemIdentity = strings.Repeat("e", 64)
	}
	if decision != trashTransferPrepared {
		if kind == trashTransferDeleteToTrash {
			record.TrashStageIdentity = strings.Repeat("e", 64)
		} else {
			record.WorkspaceStageIdentity = strings.Repeat("9", 64)
		}
	}
	if decision != trashTransferPrepared && decision != trashTransferCopying {
		record.ReplicaManifest = []trashTransferManifestEntry{
			{Path: ".", Kind: "file", Mode: 0o640, Size: 7, ModTimeUnixNano: 4, Identity: strings.Repeat("d", 64), ContentHash: strings.Repeat("c", 64)},
		}
	}
	return record
}

func TestValidateTrashTransferJournalRecord(t *testing.T) {
	for _, kind := range []string{trashTransferDeleteToTrash, trashTransferRestoreFromTrash} {
		for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
			record := validTrashTransferJournalRecordForTest(kind, decision)
			if err := validateTrashTransferJournalRecord(&record, decision); err != nil {
				t.Fatalf("validateTrashTransferJournalRecord(%s, %s) error: %v", kind, decision, err)
			}
		}
	}

	tests := []struct {
		name   string
		mutate func(*trashTransferJournalRecord)
	}{
		{name: "unknown kind", mutate: func(record *trashTransferJournalRecord) { record.Kind = "unknown" }},
		{name: "wrong decision", mutate: func(record *trashTransferJournalRecord) { record.Decision = trashTransferPrepared }},
		{name: "invalid operation ID", mutate: func(record *trashTransferJournalRecord) { record.OperationID = "../escape" }},
		{name: "invalid item path encoding", mutate: func(record *trashTransferJournalRecord) { record.Item.OriginalPath = string([]byte{0xff}) }},
		{name: "workspace stage outside parent for delete", mutate: func(record *trashTransferJournalRecord) {
			record.WorkspaceStagePath = "/other/.mnemonas-trash-transfer-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.stage"
		}},
		{name: "workspace stage basename mismatch", mutate: func(record *trashTransferJournalRecord) {
			record.WorkspaceStagePath = "/docs/.mnemonas-trash-transfer-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.stage"
		}},
		{name: "trash stage mismatch", mutate: func(record *trashTransferJournalRecord) {
			record.TrashStagePath = ".transactions/transfer-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.item"
		}},
		{name: "delete destination present", mutate: func(record *trashTransferJournalRecord) { record.DestinationPath = "/unexpected" }},
		{name: "manifest parent missing", mutate: func(record *trashTransferJournalRecord) {
			record.Item.Size = 1
			record.SourceManifest = []trashTransferManifestEntry{{Path: ".", Kind: "dir", Mode: 0o750, Identity: strings.Repeat("b", 64)}, {Path: "missing/file", Kind: "file", Mode: 0o640, Size: 1, Identity: strings.Repeat("c", 64), ContentHash: strings.Repeat("d", 64)}}
		}},
		{name: "manifest dot dot", mutate: func(record *trashTransferJournalRecord) { record.SourceManifest[0].Path = ".." }},
		{name: "manifest invalid encoding", mutate: func(record *trashTransferJournalRecord) { record.SourceManifest[0].Path = string([]byte{0xff}) }},
		{name: "manifest size mismatch", mutate: func(record *trashTransferJournalRecord) { record.SourceManifest[0].Size++ }},
		{name: "manifest invalid identity", mutate: func(record *trashTransferJournalRecord) { record.SourceManifest[0].Identity = "invalid" }},
		{name: "manifest invalid content hash", mutate: func(record *trashTransferJournalRecord) { record.SourceManifest[0].ContentHash = "invalid" }},
		{name: "prepared replica present", mutate: func(record *trashTransferJournalRecord) { record.Decision = trashTransferPrepared }},
		{name: "ready replica absent", mutate: func(record *trashTransferJournalRecord) { record.ReplicaManifest = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validTrashTransferJournalRecordForTest(trashTransferDeleteToTrash, trashTransferReady)
			test.mutate(&record)
			if err := validateTrashTransferJournalRecord(&record, trashTransferReady); err == nil {
				t.Fatal("validateTrashTransferJournalRecord() error = nil")
			}
		})
	}
}

func TestValidateTrashTransferJournalRecordCopyingRequiresOwnedContainerIdentity(t *testing.T) {
	deleteRecord := validTrashTransferJournalRecordForTest(trashTransferDeleteToTrash, trashTransferPrepared)
	deleteRecord.Decision = trashTransferCopying
	deleteRecord.TrashStageIdentity = strings.Repeat("e", 64)
	if err := validateTrashTransferJournalRecord(&deleteRecord, trashTransferCopying); err != nil {
		t.Fatalf("validateTrashTransferJournalRecord(delete copying) error: %v", err)
	}
	deleteRecord.TrashStageIdentity = ""
	if err := validateTrashTransferJournalRecord(&deleteRecord, trashTransferCopying); err == nil {
		t.Fatal("validateTrashTransferJournalRecord(delete copying without identity) error = nil")
	}

	restoreRecord := validTrashTransferJournalRecordForTest(trashTransferRestoreFromTrash, trashTransferPrepared)
	restoreRecord.Decision = trashTransferCopying
	restoreRecord.WorkspaceStageIdentity = strings.Repeat("9", 64)
	if err := validateTrashTransferJournalRecord(&restoreRecord, trashTransferCopying); err != nil {
		t.Fatalf("validateTrashTransferJournalRecord(restore copying) error: %v", err)
	}
	restoreRecord.WorkspaceStageIdentity = ""
	if err := validateTrashTransferJournalRecord(&restoreRecord, trashTransferCopying); err == nil {
		t.Fatal("validateTrashTransferJournalRecord(restore copying without identity) error = nil")
	}
}

func TestTrashTransferJournalNamesAndHash(t *testing.T) {
	operationID := strings.Repeat("a", 32)
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		name := trashTransferJournalRel(operationID, decision)
		gotID, gotDecision, ok := parseTrashTransferJournalName(strings.TrimPrefix(name, trashTransferJournalDir+"/"))
		if !ok || gotID != operationID || gotDecision != decision {
			t.Fatalf("parseTrashTransferJournalName(%q) = (%q, %q, %t)", name, gotID, gotDecision, ok)
		}
	}
	for _, name := range []string{
		"transfer-short.prepared.json",
		"transfer-" + operationID + ".unknown.json",
		"../transfer-" + operationID + ".prepared.json",
		"transfer-" + operationID + ".item",
	} {
		if _, _, ok := parseTrashTransferJournalName(name); ok {
			t.Fatalf("parseTrashTransferJournalName(%q) recognized invalid name", name)
		}
	}

	record := validTrashTransferJournalRecordForTest(trashTransferRestoreFromTrash, trashTransferCommitted)
	first, err := trashTransferJournalHash(&record)
	if err != nil || len(first) != 64 {
		t.Fatalf("trashTransferJournalHash() = %q, %v", first, err)
	}
	second, err := trashTransferJournalHash(&record)
	if err != nil || second != first {
		t.Fatalf("trashTransferJournalHash(second) = %q, %v, want %q", second, err, first)
	}
	record.Decision = trashTransferReady
	readyHash, err := trashTransferJournalHash(&record)
	if err != nil || readyHash != first {
		t.Fatalf("trashTransferJournalHash(ready) = %q, %v, want body hash %q", readyHash, err, first)
	}
	record.ParticipantPayload = append(record.ParticipantPayload, ' ')
	changed, err := trashTransferJournalHash(&record)
	if err != nil || changed == first {
		t.Fatalf("trashTransferJournalHash(changed) = %q, %v, want different hash", changed, err)
	}
}

func TestFileSystem_TrashTransferJournalRoundTrip(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	record := validTrashTransferJournalRecordForTest(trashTransferDeleteToTrash, trashTransferPrepared)

	if err := fixture.fs.ensureTrashTransferJournalDir(); err != nil {
		t.Fatalf("ensureTrashTransferJournalDir() error: %v", err)
	}
	published, err := fixture.fs.publishTrashTransferJournalRecord(&record)
	if err != nil || !published {
		t.Fatalf("publishTrashTransferJournalRecord() = (%t, %v)", published, err)
	}
	stored, err := fixture.fs.readTrashTransferJournalRecord(trashTransferJournalRel(record.OperationID, record.Decision), record.Decision)
	if err != nil {
		t.Fatalf("readTrashTransferJournalRecord() error: %v", err)
	}
	if !reflect.DeepEqual(stored, &record) {
		t.Fatalf("stored record = %#v, want %#v", stored, &record)
	}

	if published, err := fixture.fs.publishTrashTransferJournalRecord(&record); err == nil || published {
		t.Fatalf("duplicate publish = (%t, %v), want unpublished error", published, err)
	}

	info, err := os.Stat(filepath.Join(fixture.fs.trashRoot, trashTransferJournalDir))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("Trash transfer journal directory = %v, %v", info, err)
	}
	journalInfo, err := os.Stat(filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(trashTransferJournalRel(record.OperationID, record.Decision))))
	if err != nil || !journalInfo.Mode().IsRegular() || journalInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("Trash transfer journal file = %v, %v", journalInfo, err)
	}
}

func TestFileSystem_ReadTrashTransferJournalRejectsTrailingAndUnknownData(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	if err := fixture.fs.ensureTrashTransferJournalDir(); err != nil {
		t.Fatalf("ensureTrashTransferJournalDir() error: %v", err)
	}
	record := validTrashTransferJournalRecordForTest(trashTransferRestoreFromTrash, trashTransferCommitted)
	data, err := json.Marshal(&record)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "trailing", data: append(append([]byte(nil), data...), []byte("\n{}")...)},
		{name: "unknown", data: []byte(strings.Replace(string(data), `"version":1`, `"unknown":true,"version":1`, 1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			rel := trashTransferJournalRel(record.OperationID, record.Decision)
			if err := os.WriteFile(filepath.Join(fixture.fs.trashRoot, filepath.FromSlash(rel)), test.data, 0o600); err != nil {
				t.Fatalf("WriteFile() error: %v", err)
			}
			if _, err := fixture.fs.readTrashTransferJournalRecord(rel, record.Decision); err == nil {
				t.Fatal("readTrashTransferJournalRecord() error = nil")
			}
		})
	}

	if _, err := fixture.fs.readTrashTransferJournalRecord(trashTransferJournalRel(strings.Repeat("c", 32), record.Decision), record.Decision); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing journal error = %v, want os.ErrNotExist", err)
	}
}

func TestFileSystem_ScanTrashTransferTreeBindsRenameAndReplicaIdentity(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(fixture.cfg.FilesRoot, "docs"), 0o750); err != nil {
		t.Fatalf("MkdirAll(workspace) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.cfg.FilesRoot, "docs", "report.txt"), []byte("payload"), 0o640); err != nil {
		t.Fatalf("WriteFile(workspace) error: %v", err)
	}

	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	sourceManifest, _, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, filepath.Join("docs", "report.txt"), nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(source) error: %v", err)
	}
	stageRel := filepath.Join("docs", ".mnemonas-trash-transfer-"+strings.Repeat("a", 32)+".stage")
	if err := rootio.RenameNoFollow(fixture.fs.filesRootHandle, filepath.Join("docs", "report.txt"), stageRel); err != nil {
		t.Fatalf("RenameNoFollow(source, stage) error: %v", err)
	}
	if _, _, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, stageRel, sourceManifest, false); err != nil {
		t.Fatalf("scanTrashTransferTree(renamed source) error: %v", err)
	}

	if err := fixture.fs.ensureTrashTransferJournalDir(); err != nil {
		t.Fatalf("ensureTrashTransferJournalDir() error: %v", err)
	}
	trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
	replicaRel := filepath.FromSlash(trashTransferItemStageRel(strings.Repeat("a", 32)))
	if err := fixture.fs.copyFileBetweenRoots(workspaceRoot, stageRel, storageAbsolutePath(workspaceRoot, stageRel), trashRoot, replicaRel, storageAbsolutePath(trashRoot, replicaRel)); err != nil {
		t.Fatalf("copyFileBetweenRoots() error: %v", err)
	}
	replicaManifest, _, err := fixture.fs.scanTrashTransferTree(ctx, trashRoot, replicaRel, nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(replica) error: %v", err)
	}
	if !trashTransferManifestsHaveSameContent(sourceManifest, replicaManifest) {
		t.Fatalf("replica manifest = %#v, want source content %#v", replicaManifest, sourceManifest)
	}
	if replicaManifest[0].Identity == sourceManifest[0].Identity {
		t.Fatal("replica unexpectedly reused source persistent identity")
	}

	if err := os.WriteFile(storageAbsolutePath(trashRoot, replicaRel), []byte("changed"), 0o640); err != nil {
		t.Fatalf("WriteFile(mutated replica) error: %v", err)
	}
	if _, _, err := fixture.fs.scanTrashTransferTree(ctx, trashRoot, replicaRel, replicaManifest, false); err == nil {
		t.Fatal("scanTrashTransferTree(mutated replica) error = nil")
	}
}

func TestFileSystem_ScanTrashTransferTreeRejectsUnknownAndMissingEntries(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	ctx := context.Background()
	rootPath := filepath.Join(fixture.cfg.FilesRoot, "tree")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		t.Fatalf("MkdirAll(tree) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "a.txt"), []byte("a"), 0o640); err != nil {
		t.Fatalf("WriteFile(a.txt) error: %v", err)
	}
	workspaceRoot := &storagePathRoot{absRoot: fixture.fs.workspace.Root(), handle: fixture.fs.filesRootHandle}
	manifest, _, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, "tree", nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(capture) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "extra.txt"), []byte("extra"), 0o640); err != nil {
		t.Fatalf("WriteFile(extra.txt) error: %v", err)
	}
	if _, _, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, "tree", manifest, false); err == nil {
		t.Fatal("scanTrashTransferTree(extra entry) error = nil")
	}
	if err := os.Remove(filepath.Join(rootPath, "extra.txt")); err != nil {
		t.Fatalf("Remove(extra.txt) error: %v", err)
	}
	if err := os.Remove(filepath.Join(rootPath, "a.txt")); err != nil {
		t.Fatalf("Remove(a.txt) error: %v", err)
	}
	if _, _, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, "tree", manifest, false); err == nil {
		t.Fatal("scanTrashTransferTree(missing entry, strict) error = nil")
	}
	if _, present, err := fixture.fs.scanTrashTransferTree(ctx, workspaceRoot, "tree", manifest, true); err != nil || len(present) != 1 {
		t.Fatalf("scanTrashTransferTree(missing entry, partial) = (%d, %v), want root only", len(present), err)
	}
}

func TestFileSystem_PublishAndRemoveDeleteTrashTransferItem(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	ctx := context.Background()
	record := validTrashTransferJournalRecordForTest(trashTransferDeleteToTrash, trashTransferReady)
	stageRel := filepath.FromSlash(record.TrashStagePath)
	contentRel := filepath.Join(stageRel, "content")
	if err := os.MkdirAll(filepath.Dir(storageAbsolutePath(&storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}, contentRel)), 0o700); err != nil {
		t.Fatalf("MkdirAll(stage) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.fs.trashRoot, contentRel), []byte("payload"), 0o640); err != nil {
		t.Fatalf("WriteFile(stage content) error: %v", err)
	}
	trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
	manifest, _, err := fixture.fs.scanTrashTransferTree(ctx, trashRoot, contentRel, nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(stage content) error: %v", err)
	}
	record.ReplicaManifest = manifest
	record.SourceManifest = append([]trashTransferManifestEntry(nil), manifest...)
	stageInfo, err := fixture.fs.trashRootHandle.Lstat(stageRel)
	if err != nil {
		t.Fatalf("Lstat(stage) error: %v", err)
	}
	record.TrashStageIdentity = workspace.PersistentIdentityTokenForFileInfo(stageInfo)
	if err := validateTrashTransferJournalRecord(&record, trashTransferReady); err != nil {
		t.Fatalf("validateTrashTransferJournalRecord() error: %v", err)
	}
	if err := fixture.fs.publishDeleteTrashTransferItem(ctx, &record); err != nil {
		t.Fatalf("publishDeleteTrashTransferItem() error: %v", err)
	}
	if _, err := fixture.fs.trashRootHandle.Lstat(stageRel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged item after publish error = %v, want os.ErrNotExist", err)
	}
	if _, _, err := fixture.fs.scanDeleteTrashTransferItem(ctx, record.Item.ID, record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
		t.Fatalf("scanDeleteTrashTransferItem(canonical) error: %v", err)
	}
	if err := fixture.fs.removeDeleteTrashTransferItem(ctx, record.Item.ID, record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
		t.Fatalf("removeDeleteTrashTransferItem() error: %v", err)
	}
	if _, err := fixture.fs.trashRootHandle.Lstat(record.Item.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canonical item after removal error = %v, want os.ErrNotExist", err)
	}
}

func TestFileSystem_RemoveDeleteTrashTransferItemRejectsUnknownEntry(t *testing.T) {
	fixture := newTrashPurgeRecoveryTestFixture(t)
	ctx := context.Background()
	record := validTrashTransferJournalRecordForTest(trashTransferDeleteToTrash, trashTransferReady)
	itemRoot := filepath.Join(fixture.fs.trashRoot, record.Item.ID)
	if err := os.MkdirAll(itemRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(item) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemRoot, "content"), []byte("payload"), 0o640); err != nil {
		t.Fatalf("WriteFile(content) error: %v", err)
	}
	trashRoot := &storagePathRoot{absRoot: fixture.fs.trashRoot, handle: fixture.fs.trashRootHandle}
	manifest, _, err := fixture.fs.scanTrashTransferTree(ctx, trashRoot, filepath.Join(record.Item.ID, "content"), nil, false)
	if err != nil {
		t.Fatalf("scanTrashTransferTree(content) error: %v", err)
	}
	itemInfo, err := fixture.fs.trashRootHandle.Lstat(record.Item.ID)
	if err != nil {
		t.Fatalf("Lstat(item) error: %v", err)
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(itemInfo)
	if err := os.WriteFile(filepath.Join(itemRoot, "unknown"), []byte("do not remove"), 0o600); err != nil {
		t.Fatalf("WriteFile(unknown) error: %v", err)
	}
	if err := fixture.fs.removeDeleteTrashTransferItem(ctx, record.Item.ID, identity, manifest, false); err == nil {
		t.Fatal("removeDeleteTrashTransferItem() error = nil")
	}
	if data, err := os.ReadFile(filepath.Join(itemRoot, "unknown")); err != nil || string(data) != "do not remove" {
		t.Fatalf("unknown entry = %q, %v", data, err)
	}
}
