package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	trashPurgeJournalVersion = 1
	trashPurgeJournalDir     = ".deleting"
	trashPurgeJournalMaxSize = 256 << 20
	trashPurgePrepared       = "prepared"
	trashPurgeCommitted      = "committed"
)

// ErrTrashRecoveryRequired indicates that Trash mutations must remain blocked
// until journaled permanent deletions can be resolved safely.
var ErrTrashRecoveryRequired = errors.New("Trash recovery is required")

// TrashRecoveryRequiredError reports a journaled operation that could not be
// resolved safely. Trash mutations remain blocked until recovery succeeds.
type TrashRecoveryRequiredError struct {
	OperationID  string
	StagePath    string
	JournalPaths []string
	err          error
}

func (e *TrashRecoveryRequiredError) Error() string {
	if e == nil {
		return ErrTrashRecoveryRequired.Error()
	}
	message := ErrTrashRecoveryRequired.Error()
	if e.OperationID != "" {
		message += fmt.Sprintf(": operation %s", e.OperationID)
	}
	if e.StagePath != "" {
		message += fmt.Sprintf("; stage %s", e.StagePath)
	}
	if len(e.JournalPaths) != 0 {
		message += fmt.Sprintf("; journals %s", strings.Join(e.JournalPaths, ", "))
	}
	if e.err != nil {
		message += fmt.Sprintf("; cause: %v", e.err)
	}
	return message
}

func (e *TrashRecoveryRequiredError) Unwrap() error {
	if e == nil || e.err == nil {
		return ErrTrashRecoveryRequired
	}
	return errors.Join(ErrTrashRecoveryRequired, e.err)
}

type trashPurgeJournalItem struct {
	ID            string `json:"id"`
	OriginalPath  string `json:"original_path"`
	Size          int64  `json:"size"`
	DeletedAtUnix int64  `json:"deleted_at_unix"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
	IsDir         bool   `json:"is_dir"`
	HadVersions   bool   `json:"had_versions"`
	RestoreData   []byte `json:"restore_data"`
}

type trashPurgeManifestEntry struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Mode        uint32 `json:"mode"`
	Size        int64  `json:"size"`
	Identity    string `json:"identity"`
	ContentHash string `json:"content_hash,omitempty"`
}

type trashPurgeJournalRecord struct {
	Version       int                       `json:"version"`
	Decision      string                    `json:"decision"`
	OperationID   string                    `json:"operation_id"`
	Item          trashPurgeJournalItem     `json:"item"`
	Manifest      []trashPurgeManifestEntry `json:"manifest"`
	VersionHashes []string                  `json:"version_hashes"`
}

type trashPurgeOperation struct {
	record trashPurgeJournalRecord
}

// TrashRecoveryReport summarizes startup recovery of journaled Trash purges.
// UntrackedPaths are preserved for manual inspection and are never removed by
// filename pattern alone.
type TrashRecoveryReport struct {
	RolledBack     int
	RolledForward  int
	Blocked        []string
	UntrackedPaths []string
}

func trashPurgeJournalItemFromStore(item *versionstore.TrashItem) trashPurgeJournalItem {
	return trashPurgeJournalItem{
		ID:            item.ID,
		OriginalPath:  item.OriginalPath,
		Size:          item.Size,
		DeletedAtUnix: item.DeletedAt.Unix(),
		ExpiresAtUnix: item.ExpiresAt.Unix(),
		IsDir:         item.IsDir,
		HadVersions:   item.HadVersions,
		RestoreData:   append([]byte(nil), item.RestoreData...),
	}
}

func (item trashPurgeJournalItem) storeItem() *versionstore.TrashItem {
	return &versionstore.TrashItem{
		ID:           item.ID,
		OriginalPath: item.OriginalPath,
		Size:         item.Size,
		DeletedAt:    time.Unix(item.DeletedAtUnix, 0),
		ExpiresAt:    time.Unix(item.ExpiresAtUnix, 0),
		IsDir:        item.IsDir,
		HadVersions:  item.HadVersions,
		RestoreData:  append([]byte(nil), item.RestoreData...),
	}
}

func newTrashPurgeOperationID() (string, error) {
	first, err := generateID()
	if err != nil {
		return "", err
	}
	second, err := generateID()
	if err != nil {
		return "", err
	}
	return first + second, nil
}

func validTrashPurgeOperationID(operationID string) bool {
	if len(operationID) != 32 {
		return false
	}
	_, err := hex.DecodeString(operationID)
	return err == nil
}

func trashPurgePreparedRel(operationID string) string {
	return filepath.Join(trashPurgeJournalDir, "purge-"+operationID+".prepared.json")
}

func trashPurgeCommittedRel(operationID string) string {
	return filepath.Join(trashPurgeJournalDir, "purge-"+operationID+".committed.json")
}

func trashPurgeStageRel(operationID string) string {
	return filepath.Join(trashPurgeJournalDir, "purge-"+operationID+".item")
}

func (fs *FileSystem) checkTrashRootPathIdentity() error {
	if fs == nil || fs.trashRootHandle == nil {
		return errors.Join(ErrDeleteTargetChanged, errors.New("opened Trash root is unavailable"))
	}
	anchoredInfo, anchoredErr := fs.trashRootHandle.Lstat(".")
	nominalInfo, nominalErr := os.Lstat(fs.trashRoot)
	if anchoredErr != nil || nominalErr != nil || !nominalInfo.IsDir() || !os.SameFile(anchoredInfo, nominalInfo) {
		var nominalTypeErr error
		if nominalErr == nil && nominalInfo.Mode()&os.ModeSymlink != 0 {
			nominalTypeErr = errStoragePathSymlink
		}
		return errors.Join(
			ErrDeleteTargetChanged,
			nominalTypeErr,
			anchoredErr,
			nominalErr,
			fmt.Errorf("nominal Trash root %q no longer identifies the opened Trash root", fs.trashRoot),
		)
	}
	return nil
}

func (fs *FileSystem) trashRecoveryRequired(operationID string, cause error) *TrashRecoveryRequiredError {
	recoveryErr := &TrashRecoveryRequiredError{
		OperationID: operationID,
		err:         cause,
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		recoveryErr.err = errors.Join(
			recoveryErr.err,
			err,
			fmt.Errorf("nominal Trash root %q no longer identifies the opened Trash root; recovery residue paths are omitted", fs.trashRoot),
		)
		return recoveryErr
	}
	recoveryErr.StagePath = filepath.Join(fs.trashRoot, trashPurgeStageRel(operationID))
	recoveryErr.JournalPaths = []string{
		filepath.Join(fs.trashRoot, trashPurgePreparedRel(operationID)),
		filepath.Join(fs.trashRoot, trashPurgeCommittedRel(operationID)),
	}
	return recoveryErr
}

func (fs *FileSystem) blockTrashMutationsLocked(recoveryErr error) error {
	if recoveryErr == nil {
		return nil
	}
	fs.trashMutationBlocked = errors.Join(fs.trashMutationBlocked, recoveryErr)
	return recoveryErr
}

func (fs *FileSystem) checkTrashMutationAllowedLocked() error {
	if fs.trashMutationBlocked != nil {
		return fs.trashMutationBlocked
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return errors.Join(ErrTrashRecoveryRequired, err)
	}
	return nil
}

func parseTrashPurgeJournalName(name string) (operationID, decision string, ok bool) {
	if strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "purge-") {
		return "", "", false
	}
	for _, candidate := range []struct {
		suffix   string
		decision string
	}{
		{suffix: ".prepared.json", decision: trashPurgePrepared},
		{suffix: ".committed.json", decision: trashPurgeCommitted},
	} {
		if !strings.HasSuffix(name, candidate.suffix) {
			continue
		}
		operationID = strings.TrimSuffix(strings.TrimPrefix(name, "purge-"), candidate.suffix)
		return operationID, candidate.decision, validTrashPurgeOperationID(operationID)
	}
	return "", "", false
}

func parseTrashPurgeStageName(name string) (string, bool) {
	if strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "purge-") || !strings.HasSuffix(name, ".item") {
		return "", false
	}
	operationID := strings.TrimSuffix(strings.TrimPrefix(name, "purge-"), ".item")
	return operationID, validTrashPurgeOperationID(operationID)
}

func (fs *FileSystem) ensureTrashPurgeJournalDir() error {
	root := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	absDir := storageAbsolutePath(root, trashPurgeJournalDir)
	if err := ensureStorageManagedDir(root, absDir, 0700); err != nil {
		return fmt.Errorf("create Trash purge journal directory: %w", err)
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashPurgeJournalDir)
	if err != nil {
		return fmt.Errorf("open Trash purge journal directory: %w", mapStorageRootPathError(err))
	}
	defer dir.Close()
	if err := dir.Chmod(0700); err != nil {
		return fmt.Errorf("secure Trash purge journal directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync Trash purge journal directory: %w", err)
	}
	return nil
}

func (fs *FileSystem) prepareTrashPurge(ctx context.Context, item *versionstore.TrashItem) (*trashPurgeOperation, error) {
	if item == nil || !validTrashSelectionID(item.ID) {
		return nil, ErrNotFound
	}
	operationID, err := newTrashPurgeOperationID()
	if err != nil {
		return nil, fmt.Errorf("generate Trash purge operation ID: %w", err)
	}
	if err := fs.preflightTrashPurgeParticipant(ctx, operationID, item.OriginalPath, item.RestoreData); err != nil {
		return nil, fmt.Errorf("preflight Trash purge participant for %s: %w", item.ID, err)
	}
	itemRel := filepath.FromSlash(item.ID)
	manifest, _, err := fs.scanTrashPurgeTree(ctx, itemRel, nil, false)
	if err != nil {
		return nil, fmt.Errorf("capture Trash purge manifest for %s: %w", item.ID, err)
	}
	versionHashes, err := fs.captureTrashPurgeVersionHashes(ctx, item)
	if err != nil {
		return nil, fmt.Errorf("capture Trash purge version hashes for %s: %w", item.ID, err)
	}
	record := trashPurgeJournalRecord{
		Version:       trashPurgeJournalVersion,
		Decision:      trashPurgePrepared,
		OperationID:   operationID,
		Item:          trashPurgeJournalItemFromStore(item),
		Manifest:      manifest,
		VersionHashes: versionHashes,
	}
	if err := validateTrashPurgeJournalRecord(&record, trashPurgePrepared); err != nil {
		return nil, fmt.Errorf("validate Trash purge journal for %s: %w", item.ID, err)
	}
	if err := fs.ensureTrashPurgeJournalDir(); err != nil {
		return nil, err
	}
	published, err := fs.publishTrashPurgeJournalRecord(&record)
	if err != nil {
		if published {
			return nil, fs.blockTrashMutationsLocked(fs.trashRecoveryRequired(operationID, err))
		}
		return nil, fmt.Errorf("persist Trash purge journal for %s: %w", item.ID, err)
	}
	return &trashPurgeOperation{record: record}, nil
}

func (fs *FileSystem) captureTrashPurgeVersionHashes(ctx context.Context, item *versionstore.TrashItem) ([]string, error) {
	if item == nil || !item.HadVersions {
		return []string{}, nil
	}
	versionPaths := []string{item.OriginalPath}
	if item.IsDir {
		paths, err := fs.listVersionPaths(ctx)
		if err != nil {
			return nil, err
		}
		versionPaths = versionPaths[:0]
		for _, versionPath := range paths {
			if pathMatchesOrDescendant(item.OriginalPath, versionPath) {
				versionPaths = append(versionPaths, versionPath)
			}
		}
	}
	hashes := make([]string, 0)
	for _, versionPath := range versionPaths {
		versions, err := fs.versions.GetVersions(ctx, versionPath)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			hashes = append(hashes, version.Hash)
		}
	}
	return uniqueSortedStrings(hashes), nil
}

func (fs *FileSystem) publishTrashPurgeJournalRecord(record *trashPurgeJournalRecord) (bool, error) {
	if err := validateTrashPurgeJournalRecord(record, record.Decision); err != nil {
		return false, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if len(data) > trashPurgeJournalMaxSize {
		return false, errors.New("Trash purge journal exceeds size limit")
	}
	targetRel := trashPurgePreparedRel(record.OperationID)
	if record.Decision == trashPurgeCommitted {
		targetRel = trashPurgeCommittedRel(record.OperationID)
	}
	tempFile, tempRel, err := createStorageTempFile(fs.trashRootHandle, trashPurgeJournalDir, ".trash-purge-journal-")
	if err != nil {
		return false, err
	}
	published := false
	defer func() {
		if !published {
			_ = fs.trashRootHandle.Remove(tempRel)
		}
	}()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("write Trash purge journal: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("sync Trash purge journal: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return false, fmt.Errorf("close Trash purge journal: %w", err)
	}
	if err := rootio.RenameNoFollow(fs.trashRootHandle, tempRel, targetRel); err != nil {
		return false, fmt.Errorf("publish Trash purge journal: %w", mapStorageRootPathError(err))
	}
	published = true
	if err := syncManagedStorageDir(fs.trashRootHandle, trashPurgeJournalDir, filepath.Join(fs.trashRoot, trashPurgeJournalDir)); err != nil {
		return true, fmt.Errorf("sync published Trash purge journal: %w", err)
	}
	return true, nil
}

func (fs *FileSystem) scanTrashPurgeTree(ctx context.Context, rootRel string, expected []trashPurgeManifestEntry, allowMissing bool) ([]trashPurgeManifestEntry, map[string]os.FileInfo, error) {
	rootRel = filepath.Clean(rootRel)
	if rootRel == "." || filepath.IsAbs(rootRel) {
		return nil, nil, ErrDeleteTargetChanged
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return nil, nil, err
	}
	rootAbs := filepath.Join(fs.trashRoot, rootRel)
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(rootAbs); err != nil {
		return nil, nil, err
	}
	expectedByPath := make(map[string]trashPurgeManifestEntry, len(expected))
	for _, entry := range expected {
		expectedByPath[entry.Path] = entry
	}
	actual := make([]trashPurgeManifestEntry, 0, len(expected))
	identities := make(map[string]os.FileInfo, len(expected))

	var scan func(rel, suffix string) error
	scan = func(rel, suffix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := fs.trashRootHandle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrNotRegular
		}
		entry := trashPurgeManifestEntry{
			Path:     suffix,
			Kind:     "file",
			Mode:     uint32(storagePreservedMode(info.Mode())),
			Size:     info.Size(),
			Identity: workspace.PersistentIdentityTokenForFileInfo(info),
		}
		if entry.Identity == "" {
			return ErrDeleteIdentityUnavailable
		}
		if info.IsDir() {
			entry.Kind = "dir"
			entry.Size = 0
		}
		if expected != nil {
			want, ok := expectedByPath[suffix]
			if !ok || !trashPurgeEntryMetadataMatches(want, entry, allowMissing) {
				return ErrDeleteTargetChanged
			}
		}
		identity := deleteStageIdentity(info)
		if identity == "" {
			return ErrDeleteIdentityUnavailable
		}

		if info.IsDir() {
			dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, rel)
			if err != nil {
				return mapStorageRootPathError(err)
			}
			opened, err := dir.Stat()
			if err != nil || !sameDeleteStageEntry(info, identity, opened) {
				_ = dir.Close()
				return errors.Join(ErrDeleteTargetChanged, err)
			}
			children, err := dir.ReadDir(-1)
			if closeErr := dir.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
			actual = append(actual, entry)
			identities[suffix] = info
			for _, child := range children {
				childName, err := safeStorageReadDirFallbackChildName(child.Name())
				if err != nil {
					return err
				}
				childSuffix := filepath.ToSlash(childName)
				if suffix != "." {
					childSuffix = path.Join(suffix, filepath.ToSlash(childName))
				}
				if err := scan(filepath.Join(rel, childName), childSuffix); err != nil {
					return err
				}
			}
			current, err := fs.trashRootHandle.Lstat(rel)
			if err != nil || !sameDeleteStageEntry(info, identity, current) {
				return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
			}
			return nil
		}

		file, err := rootio.OpenRegularFileNoFollow(fs.trashRootHandle, rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, err := file.Stat()
		if err != nil || !sameDeleteStageEntry(info, identity, opened) {
			_ = file.Close()
			return errors.Join(ErrDeleteTargetChanged, err)
		}
		hash, err := hashOpenWorkspaceFileContext(ctx, file)
		if err != nil {
			_ = file.Close()
			return err
		}
		after, statErr := file.Stat()
		closeErr := file.Close()
		current, pathErr := fs.trashRootHandle.Lstat(rel)
		if statErr != nil || closeErr != nil || pathErr != nil || !sameDeleteStageEntry(info, identity, after) || !sameDeleteStageEntry(info, identity, current) {
			return errors.Join(ErrDeleteTargetChanged, statErr, closeErr, mapStorageRootPathError(pathErr))
		}
		entry.ContentHash = hash
		if expected != nil && expectedByPath[suffix].ContentHash != hash {
			return ErrDeleteTargetChanged
		}
		actual = append(actual, entry)
		identities[suffix] = info
		return nil
	}

	if err := scan(rootRel, "."); err != nil {
		return nil, nil, err
	}
	if expected != nil && !allowMissing && len(actual) != len(expected) {
		return nil, nil, ErrDeleteTargetChanged
	}
	if err := fs.captureDeleteMountBoundary(fs.trashRoot).checkHostTree(rootAbs); err != nil {
		return nil, nil, err
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return nil, nil, err
	}
	sort.Slice(actual, func(i, j int) bool { return actual[i].Path < actual[j].Path })
	return actual, identities, nil
}

func trashPurgeEntryMetadataMatches(expected, actual trashPurgeManifestEntry, allowPartial bool) bool {
	if expected.Path != actual.Path || expected.Kind != actual.Kind || expected.Size != actual.Size || expected.Identity != actual.Identity {
		return false
	}
	if expected.Mode == actual.Mode {
		return true
	}
	if !allowPartial || expected.Kind != "dir" {
		return false
	}
	expectedMode := os.FileMode(expected.Mode)
	return actual.Mode == uint32(storagePreservedMode(expectedMode)|0700)
}

func validateTrashPurgeJournalRecord(record *trashPurgeJournalRecord, decision string) error {
	if record == nil || record.Version != trashPurgeJournalVersion || record.Decision != decision || !validTrashPurgeOperationID(record.OperationID) {
		return errors.New("invalid Trash purge journal header")
	}
	if !validTrashSelectionID(record.Item.ID) || record.Item.Size < 0 || record.Item.DeletedAtUnix < 0 || record.Item.ExpiresAtUnix < 0 {
		return errors.New("invalid Trash purge journal item")
	}
	if !utf8.ValidString(record.Item.OriginalPath) {
		return errors.New("invalid Trash purge original path encoding")
	}
	originalPath, err := normalizeStorageWorkspacePath(record.Item.OriginalPath)
	if err != nil || originalPath != record.Item.OriginalPath || originalPath == "/" {
		return errors.New("invalid Trash purge original path")
	}
	if len(record.Manifest) == 0 || record.Manifest[0].Path != "." || record.Manifest[0].Kind != "dir" {
		return errors.New("invalid Trash purge manifest root")
	}
	seen := make(map[string]string, len(record.Manifest))
	var totalSize int64
	for i, entry := range record.Manifest {
		if i > 0 && record.Manifest[i-1].Path >= entry.Path {
			return errors.New("Trash purge manifest is not strictly sorted")
		}
		if !utf8.ValidString(entry.Path) || entry.Path == "" || entry.Path == ".." || strings.Contains(entry.Path, "\\") || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path || strings.HasPrefix(entry.Path, "../") {
			return errors.New("invalid Trash purge manifest path")
		}
		if entry.Kind != "file" && entry.Kind != "dir" {
			return errors.New("invalid Trash purge manifest kind")
		}
		mode := os.FileMode(entry.Mode)
		if uint32(storagePreservedMode(mode)) != entry.Mode {
			return errors.New("invalid Trash purge manifest mode")
		}
		if !validTrashPurgeContentHash(entry.Identity) {
			return errors.New("invalid Trash purge manifest identity")
		}
		if entry.Kind == "dir" {
			if entry.Size != 0 || entry.ContentHash != "" {
				return errors.New("invalid Trash purge directory manifest")
			}
		} else {
			if entry.Size < 0 || !validTrashPurgeContentHash(entry.ContentHash) {
				return errors.New("invalid Trash purge file manifest")
			}
			if entry.Size > record.Item.Size-totalSize {
				return errors.New("Trash purge manifest size overflow")
			}
			totalSize += entry.Size
		}
		if entry.Path != "." {
			parent := path.Dir(entry.Path)
			if parent == "." {
				parent = "."
			}
			if seen[parent] != "dir" {
				return errors.New("Trash purge manifest parent is missing")
			}
		}
		seen[entry.Path] = entry.Kind
	}
	if totalSize != record.Item.Size {
		return errors.New("Trash purge manifest size does not match item")
	}
	contentKind, ok := seen["content"]
	if !ok || (record.Item.IsDir && contentKind != "dir") || (!record.Item.IsDir && contentKind != "file") {
		return errors.New("Trash purge manifest content kind does not match item")
	}
	if !record.Item.HadVersions && len(record.VersionHashes) != 0 {
		return errors.New("Trash purge journal contains unexpected version hashes")
	}
	for i, hash := range record.VersionHashes {
		if !validTrashPurgeContentHash(hash) || (i > 0 && record.VersionHashes[i-1] >= hash) {
			return errors.New("invalid Trash purge version hash list")
		}
	}
	return nil
}

func validTrashPurgeContentHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func (fs *FileSystem) readTrashPurgeJournalRecord(rel, decision string) (*trashPurgeJournalRecord, error) {
	file, err := rootio.OpenRegularFileNoFollow(fs.trashRootHandle, rel)
	if err != nil {
		return nil, mapStorageRootPathError(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > trashPurgeJournalMaxSize {
		return nil, errors.New("Trash purge journal exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, trashPurgeJournalMaxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > trashPurgeJournalMaxSize {
		return nil, errors.New("Trash purge journal exceeds size limit")
	}
	after, err := file.Stat()
	if err != nil || !sameDeleteStageEntry(info, deleteStageIdentity(info), after) {
		return nil, errors.Join(ErrDeleteTargetChanged, err)
	}
	current, err := fs.trashRootHandle.Lstat(rel)
	if err != nil || !sameDeleteStageEntry(info, deleteStageIdentity(info), current) {
		return nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record trashPurgeJournalRecord
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Trash purge journal contains trailing data")
	}
	if err := validateTrashPurgeJournalRecord(&record, decision); err != nil {
		return nil, err
	}
	return &record, nil
}

func sameTrashPurgeOperation(left, right *trashPurgeJournalRecord) bool {
	if left == nil || right == nil {
		return false
	}
	leftCopy := *left
	rightCopy := *right
	leftCopy.Decision = ""
	rightCopy.Decision = ""
	leftJSON, leftErr := json.Marshal(leftCopy)
	rightJSON, rightErr := json.Marshal(rightCopy)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (fs *FileSystem) executeTrashPurge(ctx context.Context, operation *trashPurgeOperation) (bool, error) {
	if operation == nil {
		return false, errors.New("Trash purge operation is missing")
	}
	record := &operation.record
	item := record.Item.storeItem()
	trashItemPath := path.Join(fs.trashRoot, item.ID)
	stagePath := filepath.Join(fs.trashRoot, trashPurgeStageRel(record.OperationID))
	rollback := func(cause error) (bool, error) {
		rollbackErr := fs.rollbackPreparedTrashPurge(context.WithoutCancel(ctx), record)
		if rollbackErr != nil {
			recoveryErr := fs.trashRecoveryRequired(record.OperationID, rollbackErr)
			fs.blockTrashMutationsLocked(recoveryErr)
			return false, errors.Join(cause, recoveryErr)
		}
		return false, cause
	}

	if err := fs.movePath(trashItemPath, stagePath); err != nil {
		return rollback(fmt.Errorf("stage Trash item %s for permanent deletion: %w", item.ID, err))
	}
	if _, _, err := fs.scanTrashPurgeTree(ctx, trashPurgeStageRel(record.OperationID), record.Manifest, false); err != nil {
		return rollback(fmt.Errorf("verify staged Trash item %s: %w", item.ID, err))
	}
	if err := fs.removeTrashMetadata(ctx, item.ID); err != nil {
		return rollback(fmt.Errorf("remove Trash metadata for %s: %w", item.ID, err))
	}

	committed := *record
	committed.Decision = trashPurgeCommitted
	published, err := fs.publishTrashPurgeJournalRecord(&committed)
	if err != nil {
		if !published {
			return rollback(fmt.Errorf("persist committed Trash purge decision for %s: %w", item.ID, err))
		}
		operation.record = committed
		recoveryErr := fs.trashRecoveryRequired(record.OperationID, fmt.Errorf("persist committed Trash purge decision for %s: %w", item.ID, err))
		fs.blockTrashMutationsLocked(recoveryErr)
		return false, recoveryErr
	}
	operation.record = committed

	if _, err := fs.removeTrashPathDurably(stagePath); err != nil {
		durabilityErr := &trashDeleteDurabilityError{err: fmt.Errorf("remove committed Trash content for %s: %w", item.ID, err)}
		if errors.Is(err, ErrDeleteTargetChanged) {
			recoveryErr := fs.trashRecoveryRequired(record.OperationID, err)
			fs.blockTrashMutationsLocked(recoveryErr)
			return true, errors.Join(durabilityErr, recoveryErr)
		}
		return true, durabilityErr
	}
	return true, nil
}

func (fs *FileSystem) finishTrashPurge(ctx context.Context, operation *trashPurgeOperation) error {
	if operation == nil || operation.record.Decision != trashPurgeCommitted {
		return errors.New("committed Trash purge operation is missing")
	}
	recoveryCtx := context.WithoutCancel(ctx)
	item := operation.record.Item.storeItem()
	if err := fs.cleanupTrashPurgeVersions(recoveryCtx, item, operation.record.VersionHashes); err != nil {
		durabilityErr := &trashDeleteDurabilityError{err: fmt.Errorf("cleanup version state after Trash purge for %s: %w", item.ID, err)}
		recoveryErr := fs.trashRecoveryRequired(operation.record.OperationID, durabilityErr)
		fs.blockTrashMutationsLocked(recoveryErr)
		return recoveryErr
	}
	participantWarning, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.completeTrashPurgeParticipant(
			recoveryCtx,
			operation.record.OperationID,
			item.OriginalPath,
			item.RestoreData,
		)
	})
	if participantErr != nil {
		recoveryErr := fs.trashRecoveryRequired(
			operation.record.OperationID,
			fmt.Errorf("complete Trash purge participant for %s: %w", item.ID, participantErr),
		)
		fs.blockTrashMutationsLocked(recoveryErr)
		return recoveryErr
	}
	var completionWarning error
	if participantWarning != nil {
		completionWarning = fmt.Errorf("complete Trash purge participant for %s: %w", item.ID, participantWarning)
	}
	if err := fs.completeTrashPurgeJournal(operation.record.OperationID); err != nil {
		retentionErr := fs.retainCommittedTrashPurgeJournal(&operation.record)
		durabilityErr := &trashDeleteDurabilityError{err: errors.Join(
			completionWarning,
			fmt.Errorf("complete Trash purge journal for %s: %w", item.ID, err),
			retentionErr,
		)}
		recoveryErr := fs.trashRecoveryRequired(operation.record.OperationID, durabilityErr)
		fs.blockTrashMutationsLocked(recoveryErr)
		return recoveryErr
	}
	return wrapVisibleMutationWarning(completionWarning)
}

func (fs *FileSystem) cleanupTrashPurgeVersions(ctx context.Context, item *versionstore.TrashItem, versionHashes []string) error {
	if item == nil || !item.HadVersions {
		return nil
	}
	sharedMetadata, err := fs.hasOtherTrashItemWithOriginalPath(ctx, item.OriginalPath, item.ID)
	if err != nil {
		return err
	}
	if sharedMetadata {
		return nil
	}
	livePathExists, err := fs.workspacePathExists(ctx, item.OriginalPath)
	if err != nil {
		return err
	}
	if livePathExists {
		return nil
	}
	metadataErr := fs.cleanupDeletedTrashVersionMetadata(ctx, item)
	objectErr := fs.deleteUnreferencedVersionObjects(ctx, versionHashes)
	if objectErr != nil {
		objectErr = fmt.Errorf("failed to delete version objects for trash item: %w", objectErr)
	}
	return errors.Join(metadataErr, objectErr)
}

func (fs *FileSystem) rollbackPreparedTrashPurge(ctx context.Context, record *trashPurgeJournalRecord) error {
	if err := validateTrashPurgeJournalRecord(record, trashPurgePrepared); err != nil {
		return err
	}
	committedRel := trashPurgeCommittedRel(record.OperationID)
	if _, err := fs.trashRootHandle.Lstat(committedRel); err == nil {
		return errors.New("committed Trash purge decision cannot be rolled back")
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	stored, metadataErr := fs.versions.GetTrashItem(ctx, record.Item.ID)
	if metadataErr == nil {
		if !sameTrashPurgeItem(record.Item, trashPurgeJournalItemFromStore(stored)) {
			return errors.New("existing Trash metadata does not match purge journal")
		}
	} else if !errors.Is(metadataErr, versionstore.ErrNotFound) {
		return metadataErr
	}

	canonicalRel := filepath.FromSlash(record.Item.ID)
	stageRel := trashPurgeStageRel(record.OperationID)
	canonicalInfo, canonicalErr := fs.trashRootHandle.Lstat(canonicalRel)
	stageInfo, stageErr := fs.trashRootHandle.Lstat(stageRel)
	canonicalExists := canonicalErr == nil
	stageExists := stageErr == nil
	if canonicalErr != nil && !errors.Is(canonicalErr, os.ErrNotExist) {
		return mapStorageRootPathError(canonicalErr)
	}
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return mapStorageRootPathError(stageErr)
	}
	if canonicalExists && stageExists {
		return errors.New("both canonical and staged Trash purge content exist")
	}
	if !canonicalExists && !stageExists {
		return errors.New("neither canonical nor staged Trash purge content exists")
	}
	if canonicalExists {
		if canonicalInfo.Mode()&os.ModeSymlink != 0 {
			return ErrNotRegular
		}
		if _, _, err := fs.scanTrashPurgeTree(ctx, canonicalRel, record.Manifest, false); err != nil {
			return fmt.Errorf("verify canonical Trash item during purge rollback: %w", err)
		}
	} else {
		if stageInfo.Mode()&os.ModeSymlink != 0 {
			return ErrNotRegular
		}
		if _, _, err := fs.scanTrashPurgeTree(ctx, stageRel, record.Manifest, false); err != nil {
			return fmt.Errorf("verify staged Trash item during purge rollback: %w", err)
		}
		stagePath := filepath.Join(fs.trashRoot, stageRel)
		canonicalPath := filepath.Join(fs.trashRoot, canonicalRel)
		if err := fs.movePath(stagePath, canonicalPath); err != nil {
			return fmt.Errorf("restore staged Trash item during purge rollback: %w", err)
		}
		if _, _, err := fs.scanTrashPurgeTree(ctx, canonicalRel, record.Manifest, false); err != nil {
			return fmt.Errorf("verify restored Trash item during purge rollback: %w", err)
		}
	}
	canonicalParentRel := filepath.Dir(canonicalRel)
	canonicalRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	if err := syncManagedStorageDir(fs.trashRootHandle, canonicalParentRel, storageAbsolutePath(canonicalRoot, canonicalParentRel)); err != nil {
		return fmt.Errorf("sync canonical Trash item during purge rollback: %w", err)
	}

	if errors.Is(metadataErr, versionstore.ErrNotFound) {
		if err := fs.addTrashMetadata(ctx, record.Item.storeItem()); err != nil {
			return fmt.Errorf("restore Trash metadata during purge rollback: %w", err)
		}
	}

	if err := fs.removeTrashPurgeJournalFile(trashPurgePreparedRel(record.OperationID), true); err != nil {
		return fmt.Errorf("remove prepared Trash purge journal: %w", err)
	}
	return nil
}

func sameTrashPurgeItem(left, right trashPurgeJournalItem) bool {
	return left.ID == right.ID &&
		left.OriginalPath == right.OriginalPath &&
		left.Size == right.Size &&
		left.DeletedAtUnix == right.DeletedAtUnix &&
		left.ExpiresAtUnix == right.ExpiresAtUnix &&
		left.IsDir == right.IsDir &&
		left.HadVersions == right.HadVersions &&
		bytes.Equal(left.RestoreData, right.RestoreData)
}

func (fs *FileSystem) recoverCommittedTrashPurge(ctx context.Context, record *trashPurgeJournalRecord) error {
	if err := validateTrashPurgeJournalRecord(record, trashPurgeCommitted); err != nil {
		return err
	}
	canonicalRel := filepath.FromSlash(record.Item.ID)
	if _, err := fs.trashRootHandle.Lstat(canonicalRel); err == nil {
		return errors.New("canonical Trash item exists for committed purge")
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}

	stored, err := fs.versions.GetTrashItem(ctx, record.Item.ID)
	if err == nil {
		if !sameTrashPurgeItem(record.Item, trashPurgeJournalItemFromStore(stored)) {
			return errors.New("existing Trash metadata does not match committed purge journal")
		}
		if err := fs.removeTrashMetadata(ctx, record.Item.ID); err != nil {
			return fmt.Errorf("remove Trash metadata during purge recovery: %w", err)
		}
	} else if !errors.Is(err, versionstore.ErrNotFound) {
		return err
	}

	stageRel := trashPurgeStageRel(record.OperationID)
	stagePath := filepath.Join(fs.trashRoot, stageRel)
	if _, err := fs.trashRootHandle.Lstat(stageRel); err == nil {
		if _, err := fs.removeTrashPathDurably(stagePath); err != nil {
			return fmt.Errorf("remove staged Trash content during purge recovery: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	if err := fs.cleanupTrashPurgeVersions(ctx, record.Item.storeItem(), record.VersionHashes); err != nil {
		return fmt.Errorf("cleanup version metadata during Trash purge recovery: %w", err)
	}
	_, participantErr := deliverTrashParticipantWithDurabilityRetry(func() error {
		return fs.completeTrashPurgeParticipant(
			context.WithoutCancel(ctx),
			record.OperationID,
			record.Item.OriginalPath,
			record.Item.RestoreData,
		)
	})
	if participantErr != nil {
		return fmt.Errorf("complete Trash purge participant during recovery: %w", participantErr)
	}
	if err := fs.completeTrashPurgeJournal(record.OperationID); err != nil {
		return errors.Join(err, fs.retainCommittedTrashPurgeJournal(record))
	}
	return nil
}

func (fs *FileSystem) removeCommittedTrashPurgeStage(target string) error {
	absTarget, err := normalizeStorageHostPath(target)
	if err != nil {
		return err
	}
	rel, ok := storageRelativePath(fs.trashRoot, absTarget)
	if !ok {
		return fmt.Errorf("Trash purge stage %q is outside Trash root", target)
	}
	base := filepath.Base(rel)
	if filepath.Dir(rel) != trashPurgeJournalDir || !strings.HasPrefix(base, "purge-") || !strings.HasSuffix(base, ".item") {
		return errors.New("invalid committed Trash purge stage path")
	}
	operationID := strings.TrimSuffix(strings.TrimPrefix(base, "purge-"), ".item")
	if !validTrashPurgeOperationID(operationID) {
		return errors.New("invalid committed Trash purge operation ID")
	}
	record, err := fs.readTrashPurgeJournalRecord(trashPurgeCommittedRel(operationID), trashPurgeCommitted)
	if err != nil {
		return fmt.Errorf("read committed Trash purge journal: %w", err)
	}
	_, identities, err := fs.scanTrashPurgeTree(context.Background(), rel, record.Manifest, true)
	if err != nil {
		return fmt.Errorf("verify committed Trash purge stage: %w", err)
	}
	journalDir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashPurgeJournalDir)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer journalDir.Close()
	verify := func(entryPath string, info os.FileInfo) error {
		suffix := strings.TrimPrefix(filepath.ToSlash(entryPath), filepath.ToSlash(base)+"/")
		if entryPath == base {
			suffix = "."
		}
		expected, ok := identities[suffix]
		if !ok || !sameDeleteStageEntry(expected, deleteStageIdentity(expected), info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(journalDir, base, verify); err != nil {
		return mapStorageRootPathError(err)
	}
	return nil
}

func (fs *FileSystem) removeTrashPurgeJournalFile(rel string, allowMissing bool) error {
	info, err := fs.trashRootHandle.Lstat(rel)
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapStorageRootPathError(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegular
	}
	journalDir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashPurgeJournalDir)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer journalDir.Close()
	base := filepath.Base(rel)
	identity := deleteStageIdentity(info)
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(journalDir, base, func(entryPath string, current os.FileInfo) error {
		if entryPath != base || !sameDeleteStageEntry(info, identity, current) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(fs.trashRootHandle, trashPurgeJournalDir, filepath.Join(fs.trashRoot, trashPurgeJournalDir))
}

func (fs *FileSystem) completeTrashPurgeJournal(operationID string) error {
	if err := fs.removeTrashPurgeJournalFile(trashPurgePreparedRel(operationID), true); err != nil {
		return err
	}
	return fs.removeTrashPurgeJournalFile(trashPurgeCommittedRel(operationID), true)
}

func (fs *FileSystem) retainCommittedTrashPurgeJournal(record *trashPurgeJournalRecord) error {
	if err := validateTrashPurgeJournalRecord(record, trashPurgeCommitted); err != nil {
		return fmt.Errorf("retain committed Trash purge journal: %w", err)
	}
	committedRel := trashPurgeCommittedRel(record.OperationID)
	existing, err := fs.readTrashPurgeJournalRecord(committedRel, trashPurgeCommitted)
	if err == nil {
		if !sameTrashPurgeOperation(existing, record) {
			return errors.New("retain committed Trash purge journal: existing record does not match purge operation")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retain committed Trash purge journal: %w", err)
	}
	published, publishErr := fs.publishTrashPurgeJournalRecord(record)
	if !published {
		return fmt.Errorf("retain committed Trash purge journal: %w", publishErr)
	}
	if publishErr != nil {
		return fmt.Errorf("sync retained committed Trash purge journal: %w", publishErr)
	}
	return nil
}

type trashPurgeRecoveryRecords struct {
	prepared  *trashPurgeJournalRecord
	committed *trashPurgeJournalRecord
}

// RecoverTrashDeletions resolves journaled permanent Trash deletion operations.
// Prepared operations roll back, committed operations roll forward, and any
// unverifiable state is retained and returned as a blocking error.
func (fs *FileSystem) RecoverTrashDeletions(ctx context.Context) (TrashRecoveryReport, error) {
	var report TrashRecoveryReport
	release := fs.beginRecoveryMutation()
	defer release()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, err)
		return report, fs.trashMutationBlocked
	}
	if _, err := fs.trashRootHandle.Lstat(trashPurgeJournalDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if fs.trashMutationBlocked != nil {
				return report, fs.trashMutationBlocked
			}
			return report, nil
		}
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, mapStorageRootPathError(err))
		return report, fs.trashMutationBlocked
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashPurgeJournalDir)
	if err != nil {
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, mapStorageRootPathError(err))
		return report, fs.trashMutationBlocked
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil || closeErr != nil {
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, err, closeErr)
		return report, fs.trashMutationBlocked
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	records := make(map[string]*trashPurgeRecoveryRecords)
	stages := make(map[string]string)
	var scanErr error
	for _, entry := range entries {
		name, err := safeStorageReadDirFallbackChildName(entry.Name())
		if err != nil {
			residuePath := filepath.Join(fs.trashRoot, trashPurgeJournalDir, entry.Name())
			report.UntrackedPaths = append(report.UntrackedPaths, residuePath)
			scanErr = errors.Join(scanErr, fmt.Errorf("inspect Trash purge residue %q: %w", residuePath, err))
			continue
		}
		operationID, decision, recognized := parseTrashPurgeJournalName(name)
		if !recognized {
			if stageOperationID, stageRecognized := parseTrashPurgeStageName(name); stageRecognized {
				stages[stageOperationID] = filepath.Join(fs.trashRoot, trashPurgeJournalDir, name)
				continue
			}
			report.UntrackedPaths = append(report.UntrackedPaths, filepath.Join(fs.trashRoot, trashPurgeJournalDir, name))
			continue
		}
		record, err := fs.readTrashPurgeJournalRecord(filepath.Join(trashPurgeJournalDir, name), decision)
		if err != nil || record.OperationID != operationID {
			report.Blocked = append(report.Blocked, operationID)
			if err == nil {
				if validTrashPurgeOperationID(record.OperationID) {
					report.Blocked = append(report.Blocked, record.OperationID)
				}
				err = errors.New("Trash purge journal operation ID does not match its filename")
			}
			scanErr = errors.Join(scanErr, fmt.Errorf("read Trash purge operation %s: %w", operationID, err))
			continue
		}
		operationRecords := records[operationID]
		if operationRecords == nil {
			operationRecords = &trashPurgeRecoveryRecords{}
			records[operationID] = operationRecords
		}
		if decision == trashPurgePrepared {
			operationRecords.prepared = record
		} else {
			operationRecords.committed = record
		}
	}
	for operationID, stagePath := range stages {
		if records[operationID] == nil {
			report.UntrackedPaths = append(report.UntrackedPaths, stagePath)
			report.Blocked = append(report.Blocked, operationID)
			scanErr = errors.Join(scanErr, fmt.Errorf("Trash purge stage %s has no journal", operationID))
		}
	}

	operationIDs := make([]string, 0, len(records))
	operationsByTrashID := make(map[string]map[string]struct{}, len(records))
	for operationID, operationRecords := range records {
		operationIDs = append(operationIDs, operationID)
		if operationRecords.prepared != nil && operationRecords.committed != nil && !sameTrashPurgeOperation(operationRecords.prepared, operationRecords.committed) {
			report.Blocked = append(report.Blocked, operationID)
			scanErr = errors.Join(scanErr, fmt.Errorf("Trash purge operation %s has mismatched journal decisions", operationID))
		}
		for _, record := range []*trashPurgeJournalRecord{operationRecords.prepared, operationRecords.committed} {
			if record == nil {
				continue
			}
			operations := operationsByTrashID[record.Item.ID]
			if operations == nil {
				operations = make(map[string]struct{})
				operationsByTrashID[record.Item.ID] = operations
			}
			operations[operationID] = struct{}{}
		}
	}
	for trashID, operations := range operationsByTrashID {
		if len(operations) < 2 {
			continue
		}
		for operationID := range operations {
			report.Blocked = append(report.Blocked, operationID)
		}
		scanErr = errors.Join(scanErr, fmt.Errorf("Trash item %s is referenced by multiple purge operations", trashID))
	}
	sort.Strings(operationIDs)
	if scanErr != nil {
		for _, operationID := range operationIDs {
			report.Blocked = append(report.Blocked, operationID)
		}
		report.Blocked = uniqueSortedStrings(report.Blocked)
		report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, scanErr)
		return report, fs.trashMutationBlocked
	}
	if len(operationIDs) != 0 {
		if err := syncManagedStorageDir(fs.trashRootHandle, trashPurgeJournalDir, filepath.Join(fs.trashRoot, trashPurgeJournalDir)); err != nil {
			recoveryErr := fmt.Errorf("sync Trash purge recovery decisions: %w", err)
			report.Blocked = uniqueSortedStrings(append(report.Blocked, operationIDs...))
			report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
			fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, recoveryErr)
			return report, fs.trashMutationBlocked
		}
	}
	preparedIDs := make([]string, 0, len(operationIDs))
	committedIDs := make([]string, 0, len(operationIDs))
	for _, operationID := range operationIDs {
		operationRecords := records[operationID]
		if operationRecords.committed != nil {
			committedIDs = append(committedIDs, operationID)
		} else if operationRecords.prepared != nil {
			preparedIDs = append(preparedIDs, operationID)
		}
	}
	if err := fs.preflightTrashPurgeParticipantRecovery(ctx, records, committedIDs); err != nil {
		report.Blocked = uniqueSortedStrings(append(report.Blocked, operationIDs...))
		report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
		fs.trashMutationBlocked = errors.Join(
			ErrTrashRecoveryRequired,
			fmt.Errorf("preflight Trash purge participants: %w", err),
		)
		return report, fs.trashMutationBlocked
	}

	var preparedRecoveryErr error
	for index, operationID := range preparedIDs {
		if err := ctx.Err(); err != nil {
			report.Blocked = append(report.Blocked, preparedIDs[index:]...)
			preparedRecoveryErr = errors.Join(preparedRecoveryErr, err)
			break
		}
		operationRecords := records[operationID]
		if err := fs.rollbackPreparedTrashPurge(ctx, operationRecords.prepared); err != nil {
			report.Blocked = append(report.Blocked, operationID)
			preparedRecoveryErr = errors.Join(preparedRecoveryErr, fmt.Errorf("roll back Trash purge operation %s: %w", operationID, err))
			continue
		}
		report.RolledBack++
	}
	if preparedRecoveryErr != nil {
		report.Blocked = uniqueSortedStrings(append(report.Blocked, committedIDs...))
		report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, preparedRecoveryErr)
		return report, fs.trashMutationBlocked
	}

	var recoveryErr error
	for index, operationID := range committedIDs {
		if err := ctx.Err(); err != nil {
			report.Blocked = append(report.Blocked, committedIDs[index:]...)
			recoveryErr = errors.Join(recoveryErr, err)
			break
		}
		operationRecords := records[operationID]
		if err := fs.recoverCommittedTrashPurge(ctx, operationRecords.committed); err != nil {
			report.Blocked = append(report.Blocked, operationID)
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("roll forward Trash purge operation %s: %w", operationID, err))
			continue
		}
		report.RolledForward++
	}
	report.Blocked = uniqueSortedStrings(report.Blocked)
	report.UntrackedPaths = uniqueSortedStrings(report.UntrackedPaths)
	if recoveryErr != nil {
		fs.trashMutationBlocked = errors.Join(ErrTrashRecoveryRequired, recoveryErr)
		return report, fs.trashMutationBlocked
	}
	fs.trashMutationBlocked = nil
	return report, nil
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
