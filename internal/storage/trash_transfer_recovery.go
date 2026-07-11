package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"unicode/utf8"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/versionstore"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	trashTransferJournalVersion = 1
	trashTransferJournalDir     = ".transactions"

	trashTransferDeleteToTrash    = "delete_to_trash"
	trashTransferRestoreFromTrash = "restore_from_trash"

	trashTransferPrepared  = "prepared"
	trashTransferCopying   = "copying"
	trashTransferReady     = "ready"
	trashTransferCommitted = "committed"
	trashTransferCompleted = "completed"
)

// TrashTransferRecoveryRequiredError reports a live Trash transfer that must
// be reconciled before further mutations are safe.
type TrashTransferRecoveryRequiredError struct {
	OperationID     string
	JournalPaths    []string
	InspectionPaths []string
	err             error
}

func (e *TrashTransferRecoveryRequiredError) Error() string {
	if e == nil {
		return ErrTrashRecoveryRequired.Error()
	}
	message := ErrTrashRecoveryRequired.Error()
	if e.OperationID != "" {
		message += ": transfer " + e.OperationID
	}
	if len(e.InspectionPaths) != 0 {
		message += "; inspect: " + strings.Join(e.InspectionPaths, ", ")
	}
	if e.err != nil {
		message += "; cause: " + e.err.Error()
	}
	return message
}

func (e *TrashTransferRecoveryRequiredError) Unwrap() error {
	if e == nil || e.err == nil {
		return ErrTrashRecoveryRequired
	}
	return errors.Join(ErrTrashRecoveryRequired, e.err)
}

type trashTransferManifestEntry struct {
	Path            string `json:"path"`
	Kind            string `json:"kind"`
	Mode            uint32 `json:"mode"`
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"mod_time_unix_nano"`
	Identity        string `json:"identity"`
	ContentHash     string `json:"content_hash,omitempty"`
}

type trashTransferWorkspaceParentDir struct {
	Path     string `json:"path"`
	Identity string `json:"identity,omitempty"`
}

type trashTransferJournalRecord struct {
	Version                int                               `json:"version"`
	Decision               string                            `json:"decision"`
	Kind                   string                            `json:"kind"`
	OperationID            string                            `json:"operation_id"`
	FilesRootIdentity      string                            `json:"files_root_identity"`
	TrashRootIdentity      string                            `json:"trash_root_identity"`
	Item                   trashPurgeJournalItem             `json:"item"`
	DestinationPath        string                            `json:"destination_path,omitempty"`
	WorkspaceStagePath     string                            `json:"workspace_stage_path"`
	WorkspaceStageIdentity string                            `json:"workspace_stage_identity,omitempty"`
	WorkspaceParentDirs    []trashTransferWorkspaceParentDir `json:"workspace_parent_dirs,omitempty"`
	TrashStagePath         string                            `json:"trash_stage_path,omitempty"`
	TrashStageIdentity     string                            `json:"trash_stage_identity,omitempty"`
	TrashItemIdentity      string                            `json:"trash_item_identity,omitempty"`
	SourceManifest         []trashTransferManifestEntry      `json:"source_manifest"`
	ReplicaManifest        []trashTransferManifestEntry      `json:"replica_manifest,omitempty"`
	ParticipantPayload     []byte                            `json:"participant_payload"`
}

func (fs *FileSystem) trashParticipantHooksSnapshot() TrashParticipantHooks {
	fs.hookMu.RLock()
	defer fs.hookMu.RUnlock()
	return fs.trashParticipantHooks
}

func (fs *FileSystem) prepareTrashDeleteParticipant(ctx context.Context, operationID, sourcePath string) ([]byte, error) {
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.PrepareDelete == nil {
		return []byte{}, nil
	}
	payload, err := hooks.PrepareDelete(ctx, operationID, sourcePath)
	if err != nil {
		return nil, err
	}
	return append([]byte{}, payload...), nil
}

func (fs *FileSystem) applyTrashDeleteParticipant(ctx context.Context, operationID, sourcePath string, payload []byte, committed bool) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.ApplyDelete == nil {
		return errors.New("delete-to-Trash participant is unavailable")
	}
	return hooks.ApplyDelete(ctx, operationID, sourcePath, append([]byte(nil), payload...), committed)
}

func (fs *FileSystem) rollbackTrashDeleteParticipant(ctx context.Context, operationID, sourcePath string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.RollbackDelete == nil {
		return errors.New("delete-to-Trash participant rollback is unavailable")
	}
	return hooks.RollbackDelete(ctx, operationID, sourcePath, append([]byte(nil), payload...))
}

func (fs *FileSystem) completeTrashDeleteParticipant(ctx context.Context, operationID, sourcePath string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.CompleteDelete == nil {
		return errors.New("delete-to-Trash participant completion is unavailable")
	}
	return hooks.CompleteDelete(ctx, operationID, sourcePath, append([]byte(nil), payload...))
}

func (fs *FileSystem) applyTrashRestoreParticipant(ctx context.Context, operationID, originalPath, destinationPath string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.ApplyRestore == nil {
		return errors.New("restore-from-Trash participant is unavailable")
	}
	return hooks.ApplyRestore(ctx, operationID, originalPath, destinationPath, append([]byte(nil), payload...))
}

func (fs *FileSystem) completeTrashRestoreParticipant(ctx context.Context, operationID, originalPath, destinationPath string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.CompleteRestore == nil {
		return errors.New("restore-from-Trash participant completion is unavailable")
	}
	return hooks.CompleteRestore(ctx, operationID, originalPath, destinationPath, append([]byte(nil), payload...))
}

func isTrashParticipantPersistenceWarning(err error) bool {
	return workspace.IsVisibleMutationWarning(err) || isVisibleMutationWarning(err)
}

func deliverTrashParticipantWithDurabilityRetry(deliver func() error) (warningErr, barrierErr error) {
	firstErr := deliver()
	if firstErr == nil {
		return nil, nil
	}
	if !isTrashParticipantPersistenceWarning(firstErr) {
		return nil, firstErr
	}

	retryErr := deliver()
	if retryErr == nil {
		return firstErr, nil
	}
	if !isTrashParticipantPersistenceWarning(retryErr) {
		return firstErr, errors.Join(firstErr, retryErr)
	}
	warnings := errors.Join(firstErr, retryErr)
	return warnings, errors.Join(errors.New("Trash participant persistence remained uncertain after one retry"), warnings)
}

func (fs *FileSystem) trashTransferRecoveryRequired(record *trashTransferJournalRecord, cause error) *TrashTransferRecoveryRequiredError {
	recoveryErr := &TrashTransferRecoveryRequiredError{err: cause}
	if record == nil {
		return recoveryErr
	}
	recoveryErr.OperationID = record.OperationID
	if err := fs.checkTrashRootPathIdentity(); err == nil {
		for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
			recoveryErr.JournalPaths = append(recoveryErr.JournalPaths, filepath.Join(fs.trashRoot, filepath.FromSlash(trashTransferJournalRel(record.OperationID, decision))))
		}
		if record.TrashStagePath != "" {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, filepath.Join(fs.trashRoot, filepath.FromSlash(record.TrashStagePath)))
		}
		if record.Kind == trashTransferDeleteToTrash {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, filepath.Join(fs.trashRoot, record.Item.ID))
		}
	} else {
		recoveryErr.err = errors.Join(recoveryErr.err, err)
	}
	if workspaceRoot := fs.workspace.Root(); workspaceRoot != "" {
		if record.WorkspaceStagePath != "" {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, fs.workspace.FullPath(record.WorkspaceStagePath))
		}
		if record.Kind == trashTransferDeleteToTrash {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, fs.workspace.FullPath(record.Item.OriginalPath))
		} else if record.DestinationPath != "" {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, fs.workspace.FullPath(record.DestinationPath))
		}
	}
	var residual *DeleteStageResidualError
	if errors.As(cause, &residual) {
		if residual.StagePath != "" {
			recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, residual.StagePath)
		}
		recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, residual.InspectionPaths...)
	}
	var witnessRecovery *deleteWitnessRecoveryError
	if errors.As(cause, &witnessRecovery) {
		recoveryErr.InspectionPaths = append(recoveryErr.InspectionPaths, witnessRecovery.paths...)
	}
	recoveryErr.InspectionPaths = uniqueSortedStrings(recoveryErr.InspectionPaths)
	return recoveryErr
}

func (fs *FileSystem) blockTrashTransferLocked(record *trashTransferJournalRecord, cause error) error {
	recoveryErr := fs.trashTransferRecoveryRequired(record, cause)
	fs.trashMutationBlocked = errors.Join(fs.trashMutationBlocked, recoveryErr)
	return recoveryErr
}

func trashTransferJournalRel(operationID, decision string) string {
	return path.Join(trashTransferJournalDir, "transfer-"+operationID+"."+decision+".json")
}

func trashTransferItemStageRel(operationID string) string {
	return path.Join(trashTransferJournalDir, "transfer-"+operationID+".item")
}

func trashTransferWorkspaceStagePath(parentPath, operationID string) string {
	return path.Join(parentPath, ".mnemonas-trash-transfer-"+operationID+".stage")
}

func parseTrashTransferJournalName(name string) (operationID, decision string, ok bool) {
	if strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "transfer-") || !strings.HasSuffix(name, ".json") {
		return "", "", false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, "transfer-"), ".json")
	separator := strings.LastIndexByte(base, '.')
	if separator <= 0 || separator == len(base)-1 {
		return "", "", false
	}
	operationID = base[:separator]
	decision = base[separator+1:]
	if !validTrashPurgeOperationID(operationID) || !validTrashTransferDecision(decision) {
		return "", "", false
	}
	return operationID, decision, true
}

func validTrashTransferDecision(decision string) bool {
	return decision == trashTransferPrepared || decision == trashTransferCopying || decision == trashTransferReady || decision == trashTransferCommitted || decision == trashTransferCompleted
}

func validateTrashTransferJournalRecord(record *trashTransferJournalRecord, decision string) error {
	if record == nil || record.Version != trashTransferJournalVersion || record.Decision != decision || !validTrashTransferDecision(decision) || !validTrashPurgeOperationID(record.OperationID) {
		return errors.New("invalid Trash transfer journal header")
	}
	if record.Kind != trashTransferDeleteToTrash && record.Kind != trashTransferRestoreFromTrash {
		return errors.New("invalid Trash transfer kind")
	}
	if !validTrashPurgeContentHash(record.FilesRootIdentity) || !validTrashPurgeContentHash(record.TrashRootIdentity) {
		return errors.New("invalid Trash transfer root identity")
	}
	if !validTrashSelectionID(record.Item.ID) || record.Item.Size < 0 || record.Item.DeletedAtUnix < 0 || record.Item.ExpiresAtUnix < 0 {
		return errors.New("invalid Trash transfer item")
	}
	if !utf8.ValidString(record.Item.OriginalPath) {
		return errors.New("invalid Trash transfer original path encoding")
	}
	if !bytes.Equal(record.Item.RestoreData, record.ParticipantPayload) {
		return errors.New("Trash transfer participant payload does not match restore data")
	}
	originalPath, err := normalizeStorageWorkspacePath(record.Item.OriginalPath)
	if err != nil || originalPath != record.Item.OriginalPath || originalPath == "/" {
		return errors.New("invalid Trash transfer original path")
	}
	if !utf8.ValidString(record.WorkspaceStagePath) {
		return errors.New("invalid Trash transfer workspace stage path encoding")
	}
	stagePath, err := normalizeStorageWorkspacePath(record.WorkspaceStagePath)
	if err != nil || stagePath != record.WorkspaceStagePath || stagePath == "/" {
		return errors.New("invalid Trash transfer workspace stage path")
	}

	expectedStageParent := path.Dir(record.Item.OriginalPath)
	if record.Kind == trashTransferDeleteToTrash {
		if record.DestinationPath != "" || record.WorkspaceStageIdentity != "" || len(record.WorkspaceParentDirs) != 0 || record.TrashStagePath != trashTransferItemStageRel(record.OperationID) || record.TrashItemIdentity != "" {
			return errors.New("invalid delete-to-Trash transfer paths")
		}
	} else {
		if record.TrashStagePath != "" || record.TrashStageIdentity != "" || !validTrashPurgeContentHash(record.TrashItemIdentity) || !utf8.ValidString(record.DestinationPath) {
			return errors.New("invalid restore-from-Trash transfer paths")
		}
		destinationPath, destinationErr := normalizeStorageWorkspacePath(record.DestinationPath)
		if destinationErr != nil || destinationPath != record.DestinationPath || destinationPath == "/" {
			return errors.New("invalid Trash transfer destination path")
		}
		expectedStageParent = path.Dir(record.DestinationPath)
	}
	if record.WorkspaceStagePath != trashTransferWorkspaceStagePath(expectedStageParent, record.OperationID) {
		return errors.New("invalid Trash transfer workspace stage name")
	}
	if record.Kind == trashTransferRestoreFromTrash {
		if err := validateTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs, expectedStageParent, decision); err != nil {
			return err
		}
	}

	if err := validateTrashTransferManifest(record.SourceManifest, record.Item.Size, record.Item.IsDir); err != nil {
		return err
	}
	if decision == trashTransferPrepared {
		if len(record.ReplicaManifest) != 0 {
			return errors.New("prepared Trash transfer contains a replica manifest")
		}
		if record.Kind == trashTransferDeleteToTrash && record.TrashStageIdentity != "" && !validTrashPurgeContentHash(record.TrashStageIdentity) {
			return errors.New("invalid delete-to-Trash prepared ownership identity")
		}
		if record.Kind == trashTransferRestoreFromTrash && record.WorkspaceStageIdentity != "" {
			if !validTrashPurgeContentHash(record.WorkspaceStageIdentity) {
				return errors.New("invalid restore-from-Trash prepared ownership identity")
			}
			for _, dir := range record.WorkspaceParentDirs {
				if dir.Identity == "" {
					return errors.New("restore-from-Trash prepared stage has an unowned parent directory")
				}
			}
		}
		return nil
	}
	if decision == trashTransferCopying {
		if len(record.ReplicaManifest) != 0 {
			return errors.New("copying Trash transfer contains a replica manifest")
		}
		if record.Kind == trashTransferDeleteToTrash && !validTrashPurgeContentHash(record.TrashStageIdentity) {
			return errors.New("invalid delete-to-Trash copying stage identity")
		}
		if record.Kind == trashTransferRestoreFromTrash && !validTrashPurgeContentHash(record.WorkspaceStageIdentity) {
			return errors.New("invalid restore-from-Trash copying stage identity")
		}
		return nil
	}
	if err := validateTrashTransferManifest(record.ReplicaManifest, record.Item.Size, record.Item.IsDir); err != nil {
		return err
	}
	if record.Kind == trashTransferDeleteToTrash && !validTrashPurgeContentHash(record.TrashStageIdentity) {
		return errors.New("invalid delete-to-Trash stage identity")
	}
	if record.Kind == trashTransferRestoreFromTrash && !validTrashPurgeContentHash(record.WorkspaceStageIdentity) {
		return errors.New("invalid restore-from-Trash stage identity")
	}
	if !trashTransferManifestsHaveSameContent(record.SourceManifest, record.ReplicaManifest) {
		return errors.New("Trash transfer replica does not match source content")
	}
	return nil
}

func validateTrashTransferWorkspaceParentDirs(dirs []trashTransferWorkspaceParentDir, expectedParent, decision string) error {
	if len(dirs) == 0 {
		return nil
	}
	if expectedParent == "/" || dirs[0].Path != expectedParent {
		return errors.New("invalid Trash transfer workspace parent directory plan")
	}
	foundPreparedIdentity := false
	for index, dir := range dirs {
		cleaned, err := normalizeStorageWorkspacePath(dir.Path)
		if err != nil || cleaned != dir.Path || cleaned == "/" {
			return errors.New("invalid Trash transfer workspace parent directory path")
		}
		if index > 0 && dir.Path != path.Dir(dirs[index-1].Path) {
			return errors.New("Trash transfer workspace parent directories are not a contiguous deepest-first chain")
		}
		if decision == trashTransferPrepared {
			if dir.Identity == "" {
				if foundPreparedIdentity {
					return errors.New("prepared Trash transfer workspace parent ownership is not a contiguous shallow chain")
				}
			} else {
				if !validTrashPurgeContentHash(dir.Identity) {
					return errors.New("invalid prepared Trash transfer workspace parent directory identity")
				}
				foundPreparedIdentity = true
			}
		} else if !validTrashPurgeContentHash(dir.Identity) {
			return errors.New("invalid Trash transfer workspace parent directory identity")
		}
	}
	return nil
}

func validateTrashTransferManifest(manifest []trashTransferManifestEntry, expectedSize int64, expectedDir bool) error {
	if len(manifest) == 0 || manifest[0].Path != "." {
		return errors.New("invalid Trash transfer manifest root")
	}
	seen := make(map[string]string, len(manifest))
	var totalSize int64
	for index, entry := range manifest {
		if index > 0 && manifest[index-1].Path >= entry.Path {
			return errors.New("Trash transfer manifest is not strictly sorted")
		}
		if !utf8.ValidString(entry.Path) || entry.Path == "" || entry.Path == ".." || strings.Contains(entry.Path, "\\") || path.IsAbs(entry.Path) || path.Clean(entry.Path) != entry.Path || strings.HasPrefix(entry.Path, "../") {
			return errors.New("invalid Trash transfer manifest path")
		}
		if entry.Kind != "file" && entry.Kind != "dir" {
			return errors.New("invalid Trash transfer manifest kind")
		}
		mode := os.FileMode(entry.Mode)
		if uint32(storagePreservedMode(mode)) != entry.Mode {
			return errors.New("invalid Trash transfer manifest mode")
		}
		if !validTrashPurgeContentHash(entry.Identity) {
			return errors.New("invalid Trash transfer manifest identity")
		}
		if entry.Kind == "dir" {
			if entry.Size != 0 || entry.ContentHash != "" {
				return errors.New("invalid Trash transfer directory manifest")
			}
		} else {
			if entry.Size < 0 || !validTrashPurgeContentHash(entry.ContentHash) || entry.Size > expectedSize-totalSize {
				return errors.New("invalid Trash transfer file manifest")
			}
			totalSize += entry.Size
		}
		if entry.Path != "." {
			parent := path.Dir(entry.Path)
			if seen[parent] != "dir" {
				return errors.New("Trash transfer manifest parent is missing")
			}
		}
		seen[entry.Path] = entry.Kind
	}
	if totalSize != expectedSize {
		return errors.New("Trash transfer manifest size does not match item")
	}
	rootKind := seen["."]
	if expectedDir && rootKind != "dir" || !expectedDir && rootKind != "file" {
		return errors.New("Trash transfer manifest root kind does not match item")
	}
	return nil
}

func trashTransferManifestsHaveSameContent(source, replica []trashTransferManifestEntry) bool {
	if len(source) != len(replica) {
		return false
	}
	for index := range source {
		left := source[index]
		right := replica[index]
		if left.Path != right.Path || left.Kind != right.Kind || left.Mode != right.Mode || left.Size != right.Size || left.ContentHash != right.ContentHash {
			return false
		}
	}
	return true
}

func trashTransferJournalHash(record *trashTransferJournalRecord) (string, error) {
	if record == nil {
		return "", errors.New("Trash transfer journal is missing")
	}
	body := *record
	body.Decision = ""
	data, err := json.Marshal(&body)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (fs *FileSystem) ensureTrashTransferJournalDir() error {
	if err := fs.checkTrashRootPathIdentity(); err != nil {
		return err
	}
	root := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	absDir := storageAbsolutePath(root, trashTransferJournalDir)
	if err := ensureStorageManagedDir(root, absDir, 0o700); err != nil {
		return fmt.Errorf("create Trash transfer journal directory: %w", err)
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
	if err != nil {
		return fmt.Errorf("open Trash transfer journal directory: %w", mapStorageRootPathError(err))
	}
	defer dir.Close()
	if err := dir.Chmod(0o700); err != nil {
		return fmt.Errorf("secure Trash transfer journal directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync Trash transfer journal directory: %w", err)
	}
	return fs.checkTrashRootPathIdentity()
}

func (fs *FileSystem) publishTrashTransferJournalRecord(record *trashTransferJournalRecord) (bool, error) {
	if err := validateTrashTransferJournalRecord(record, record.Decision); err != nil {
		return false, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if len(data) > trashPurgeJournalMaxSize {
		return false, errors.New("Trash transfer journal exceeds size limit")
	}
	if err := fs.ensureTrashTransferJournalDir(); err != nil {
		return false, err
	}
	targetRel := filepath.FromSlash(trashTransferJournalRel(record.OperationID, record.Decision))
	tempFile, tempRel, err := createStorageTempFile(fs.trashRootHandle, trashTransferJournalDir, ".trash-transfer-journal-")
	if err != nil {
		return false, err
	}
	published := false
	defer func() {
		if !published {
			_ = fs.trashRootHandle.Remove(tempRel)
		}
	}()
	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("set Trash transfer journal permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("write Trash transfer journal: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("sync Trash transfer journal: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return false, fmt.Errorf("close Trash transfer journal: %w", err)
	}
	if err := rootio.RenameNoFollow(fs.trashRootHandle, tempRel, targetRel); err != nil {
		return false, fmt.Errorf("publish Trash transfer journal: %w", mapStorageRootPathError(err))
	}
	published = true
	if err := syncManagedStorageDir(fs.trashRootHandle, trashTransferJournalDir, filepath.Join(fs.trashRoot, trashTransferJournalDir)); err != nil {
		return true, fmt.Errorf("sync published Trash transfer journal: %w", err)
	}
	return true, nil
}

func (fs *FileSystem) readTrashTransferJournalRecord(rel, decision string) (*trashTransferJournalRecord, error) {
	file, err := rootio.OpenRegularFileNoFollow(fs.trashRootHandle, filepath.FromSlash(rel))
	if err != nil {
		return nil, mapStorageRootPathError(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > trashPurgeJournalMaxSize {
		return nil, errors.New("Trash transfer journal exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, trashPurgeJournalMaxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > trashPurgeJournalMaxSize {
		return nil, errors.New("Trash transfer journal exceeds size limit")
	}
	after, err := file.Stat()
	if err != nil || !sameDeleteStageEntry(info, deleteStageIdentity(info), after) {
		return nil, errors.Join(ErrDeleteTargetChanged, err)
	}
	current, err := fs.trashRootHandle.Lstat(filepath.FromSlash(rel))
	if err != nil || !sameDeleteStageEntry(info, deleteStageIdentity(info), current) {
		return nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record trashTransferJournalRecord
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Trash transfer journal contains trailing data")
	}
	if err := validateTrashTransferJournalRecord(&record, decision); err != nil {
		return nil, err
	}
	return &record, nil
}

func (fs *FileSystem) removeTrashTransferJournalFile(rel string, allowMissing bool) error {
	rel = filepath.FromSlash(rel)
	if filepath.Dir(rel) != trashTransferJournalDir {
		return errors.New("invalid Trash transfer journal path")
	}
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
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer dir.Close()
	base := filepath.Base(rel)
	identity := deleteStageIdentity(info)
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(dir, base, func(entryPath string, current os.FileInfo) error {
		if entryPath != base || !sameDeleteStageEntry(info, identity, current) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(fs.trashRootHandle, trashTransferJournalDir, filepath.Join(fs.trashRoot, trashTransferJournalDir))
}

func (fs *FileSystem) removeTrashTransferJournalPublishTemp(name string, expectedDir os.FileInfo, expectedDirIdentity string) error {
	if !isTrashTransferJournalPublishTempName(name) {
		return errors.New("invalid Trash transfer journal publish temp name")
	}
	currentDir, err := fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil || !sameDeleteStageEntry(expectedDir, expectedDirIdentity, currentDir) || !currentDir.IsDir() || currentDir.Mode()&os.ModeSymlink != 0 {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	rel := filepath.Join(trashTransferJournalDir, name)
	info, err := fs.trashRootHandle.Lstat(rel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegular
	}
	if info.Size() < 0 || info.Size() > trashPurgeJournalMaxSize {
		return errors.New("Trash transfer journal publish temp exceeds size limit")
	}
	identity := deleteStageIdentity(info)
	if identity == "" {
		return ErrDeleteIdentityUnavailable
	}

	file, err := rootio.OpenRegularFileNoFollow(fs.trashRootHandle, rel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	opened, statErr := file.Stat()
	closeErr := file.Close()
	current, pathErr := fs.trashRootHandle.Lstat(rel)
	if statErr != nil || closeErr != nil || pathErr != nil ||
		!sameDeleteStageEntry(info, identity, opened) || !sameStorageFileObject(info, opened) ||
		!sameDeleteStageEntry(info, identity, current) || !sameStorageFileObject(info, current) {
		return errors.Join(ErrDeleteTargetChanged, statErr, closeErr, mapStorageRootPathError(pathErr))
	}

	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer dir.Close()
	openedDir, err := dir.Stat()
	if err != nil || !sameDeleteStageEntry(expectedDir, expectedDirIdentity, openedDir) {
		return errors.Join(ErrDeleteTargetChanged, err)
	}
	if err := beforeTrashTransferJournalTempRemoval(filepath.Join(fs.trashRoot, rel)); err != nil {
		return err
	}
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(dir, name, func(entryPath string, actual os.FileInfo) error {
		if entryPath != name || !sameDeleteStageEntry(info, identity, actual) || !sameStorageFileObject(info, actual) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	if err := syncTrashTransferJournalTempCleanupDir(dir); err != nil {
		return err
	}
	currentDir, err = fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil || !currentDir.IsDir() || currentDir.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedDir, currentDir) {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	return nil
}

type trashTransferTerminalCleanupWarningError struct {
	err error
}

func (e *trashTransferTerminalCleanupWarningError) Error() string {
	if e == nil || e.err == nil {
		return "Trash transfer terminal cleanup durability is uncertain"
	}
	return e.err.Error()
}

func (e *trashTransferTerminalCleanupWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func isTrashTransferTerminalCleanupWarning(err error) bool {
	var warningErr *trashTransferTerminalCleanupWarningError
	return errors.As(err, &warningErr)
}

func (fs *FileSystem) completeTrashTransferJournal(record *trashTransferJournalRecord) error {
	if record == nil {
		return ErrDeleteTargetChanged
	}
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		if err := fs.removeTrashTransferJournalFile(trashTransferJournalRel(record.OperationID, decision), true); err != nil {
			cleanupErr := fs.classifyTrashTransferTerminalCleanup(record, err)
			if record.Decision == trashTransferCompleted {
				cleanupErr = errors.Join(cleanupErr, fs.retainCompletedTrashTransferJournal(record))
			}
			return cleanupErr
		}
	}
	cleanupErr := fs.classifyTrashTransferTerminalCleanup(record, fs.removeEmptyTrashTransferJournalDir())
	if cleanupErr != nil && record.Decision == trashTransferCompleted {
		cleanupErr = errors.Join(cleanupErr, fs.retainCompletedTrashTransferJournal(record))
	}
	return cleanupErr
}

func (fs *FileSystem) retainCompletedTrashTransferJournal(record *trashTransferJournalRecord) error {
	if record == nil || record.Decision != trashTransferCompleted {
		return errors.New("retain completed Trash transfer journal: invalid completed record")
	}
	if err := validateTrashTransferJournalRecord(record, trashTransferCompleted); err != nil {
		return fmt.Errorf("retain completed Trash transfer journal: %w", err)
	}
	completedRel := trashTransferJournalRel(record.OperationID, trashTransferCompleted)
	existing, err := fs.readTrashTransferJournalRecord(completedRel, trashTransferCompleted)
	if err == nil {
		if !trashTransferCheckpointBodiesEqual(existing, record) {
			return errors.New("retain completed Trash transfer journal: existing record does not match transfer operation")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retain completed Trash transfer journal: %w", err)
	}
	published, publishErr := fs.publishTrashTransferJournalRecord(record)
	if !published {
		return fmt.Errorf("retain completed Trash transfer journal: %w", publishErr)
	}
	if publishErr != nil {
		return fmt.Errorf("sync retained completed Trash transfer journal: %w", publishErr)
	}
	return nil
}

func (fs *FileSystem) rollbackTrashTransferJournal(record *trashTransferJournalRecord) error {
	if record == nil {
		return ErrDeleteTargetChanged
	}
	// Remove the higher rollback checkpoint first. If cleanup is interrupted,
	// the remaining prepared checkpoint is independently recoverable.
	for _, decision := range []string{trashTransferReady, trashTransferCopying, trashTransferPrepared} {
		if err := fs.removeTrashTransferJournalFile(trashTransferJournalRel(record.OperationID, decision), true); err != nil {
			return errors.Join(
				fs.classifyTrashTransferTerminalCleanup(record, err),
				fs.retainPreparedTrashTransferJournal(record),
			)
		}
	}
	cleanupErr := fs.classifyTrashTransferTerminalCleanup(record, fs.removeEmptyTrashTransferJournalDir())
	if cleanupErr != nil {
		cleanupErr = errors.Join(cleanupErr, fs.retainPreparedTrashTransferJournal(record))
	}
	return cleanupErr
}

func (fs *FileSystem) retainPreparedTrashTransferJournal(record *trashTransferJournalRecord) error {
	prepared, err := trashTransferPreparedOwnershipRecord(record)
	if err != nil {
		return fmt.Errorf("retain prepared Trash transfer journal: %w", err)
	}
	preparedRel := trashTransferJournalRel(prepared.OperationID, trashTransferPrepared)
	existing, err := fs.readTrashTransferJournalRecord(preparedRel, trashTransferPrepared)
	if err == nil {
		if !trashTransferCheckpointBodiesEqual(existing, prepared) {
			return errors.New("retain prepared Trash transfer journal: existing record does not match transfer operation")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retain prepared Trash transfer journal: %w", err)
	}
	published, publishErr := fs.publishTrashTransferJournalRecord(prepared)
	if !published {
		return fmt.Errorf("retain prepared Trash transfer journal: %w", publishErr)
	}
	if publishErr != nil {
		return fmt.Errorf("sync retained prepared Trash transfer journal: %w", publishErr)
	}
	return nil
}

func (fs *FileSystem) classifyTrashTransferTerminalCleanup(record *trashTransferJournalRecord, cleanupErr error) error {
	if cleanupErr == nil {
		return nil
	}
	if err := fs.verifyTrashTransferTerminalArtifactsAbsent(record); err != nil {
		return errors.Join(cleanupErr, err)
	}
	return &trashTransferTerminalCleanupWarningError{err: cleanupErr}
}

func (fs *FileSystem) verifyTrashTransferTerminalArtifactsAbsent(record *trashTransferJournalRecord) error {
	if record == nil || validateTrashTransferJournalRecord(record, record.Decision) != nil {
		return errors.Join(ErrDeleteTargetChanged, errors.New("cannot verify terminal cleanup for an invalid Trash transfer record"))
	}
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}

	requireMissing := func(root *os.Root, rel, description string) error {
		if _, err := root.Lstat(rel); err == nil {
			return fmt.Errorf("Trash transfer terminal cleanup retained %s", description)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("verify absence of %s: %w", description, mapStorageRootPathError(err))
		}
		return nil
	}
	for _, decision := range []string{trashTransferPrepared, trashTransferCopying, trashTransferReady, trashTransferCommitted, trashTransferCompleted} {
		if err := requireMissing(
			fs.trashRootHandle,
			filepath.FromSlash(trashTransferJournalRel(record.OperationID, decision)),
			decision+" journal",
		); err != nil {
			return err
		}
	}
	if err := requireMissing(
		fs.trashRootHandle,
		filepath.FromSlash(trashTransferItemStageRel(record.OperationID)),
		"private Trash stage",
	); err != nil {
		return err
	}
	if err := requireMissing(
		fs.filesRootHandle,
		storageWorkspaceRelativeName(record.WorkspaceStagePath),
		"private workspace stage",
	); err != nil {
		return err
	}
	if record.Kind == trashTransferRestoreFromTrash {
		if record.Decision == trashTransferCommitted || record.Decision == trashTransferCompleted {
			if err := fs.verifyTrashTransferWorkspaceParentDirs(record.WorkspaceParentDirs); err != nil {
				return err
			}
			workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
			if _, _, err := fs.scanTrashTransferTree(context.Background(), workspaceRoot, storageWorkspaceRelativeName(record.DestinationPath), record.ReplicaManifest, false); err != nil {
				return fmt.Errorf("verify terminal restore-from-Trash destination: %w", err)
			}
			if err := requireMissing(fs.trashRootHandle, filepath.FromSlash(record.Item.ID), "committed restore-from-Trash source item"); err != nil {
				return err
			}
		} else {
			for _, dir := range record.WorkspaceParentDirs {
				if err := requireMissing(fs.filesRootHandle, storageWorkspaceRelativeName(dir.Path), "owned workspace parent directory"); err != nil {
					return err
				}
			}
			if err := requireMissing(fs.filesRootHandle, storageWorkspaceRelativeName(record.DestinationPath), "rolled-back restore-from-Trash destination"); err != nil {
				return err
			}
			if _, _, err := fs.scanDeleteTrashTransferItem(context.Background(), filepath.FromSlash(record.Item.ID), record.TrashItemIdentity, record.SourceManifest, false); err != nil {
				return fmt.Errorf("verify terminal restore-from-Trash source item: %w", err)
			}
		}
	} else if record.Decision == trashTransferCommitted || record.Decision == trashTransferCompleted {
		if _, _, err := fs.scanDeleteTrashTransferItem(context.Background(), filepath.FromSlash(record.Item.ID), record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
			return fmt.Errorf("verify terminal delete-to-Trash item: %w", err)
		}
		if err := requireMissing(fs.filesRootHandle, storageWorkspaceRelativeName(record.Item.OriginalPath), "committed delete-to-Trash source"); err != nil {
			return err
		}
	} else {
		if err := requireMissing(fs.trashRootHandle, filepath.FromSlash(record.Item.ID), "rolled-back delete-to-Trash item"); err != nil {
			return err
		}
		workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
		if _, _, err := fs.scanTrashTransferTree(context.Background(), workspaceRoot, storageWorkspaceRelativeName(record.Item.OriginalPath), record.SourceManifest, false); err != nil {
			return fmt.Errorf("verify terminal rolled-back delete-to-Trash source: %w", err)
		}
	}

	return fs.verifyTrashTransferRootIdentities(record)
}

func (fs *FileSystem) removeEmptyTrashTransferJournalDir() error {
	info, err := fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapStorageRootPathError(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegular
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, trashTransferJournalDir)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	opened, statErr := dir.Stat()
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if statErr != nil || readErr != nil || closeErr != nil || !os.SameFile(info, opened) {
		return errors.Join(ErrDeleteTargetChanged, statErr, readErr, closeErr)
	}
	if len(entries) != 0 {
		return nil
	}
	current, err := fs.trashRootHandle.Lstat(trashTransferJournalDir)
	if err != nil || !os.SameFile(info, current) || workspace.PersistentIdentityTokenForFileInfo(info) != workspace.PersistentIdentityTokenForFileInfo(current) {
		return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	if err := fs.trashRootHandle.Remove(trashTransferJournalDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(fs.trashRootHandle, ".", fs.trashRoot)
}

func (fs *FileSystem) checkTrashTransferRootIdentity(root *storagePathRoot) error {
	if root == nil || root.handle == nil || root.absRoot == "" {
		return errors.Join(ErrDeleteTargetChanged, errors.New("opened storage root is unavailable"))
	}
	anchoredInfo, anchoredErr := root.handle.Lstat(".")
	nominalInfo, nominalErr := os.Lstat(root.absRoot)
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
			fmt.Errorf("nominal storage root %q no longer identifies the opened storage root", root.absRoot),
		)
	}
	return nil
}

func (fs *FileSystem) captureTrashTransferRootIdentities() (string, string, error) {
	filesRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	filesIdentity, err := fs.captureTrashTransferRootIdentity(filesRoot)
	if err != nil {
		return "", "", err
	}
	trashIdentity, err := fs.captureTrashTransferRootIdentity(trashRoot)
	if err != nil {
		return "", "", err
	}
	return filesIdentity, trashIdentity, nil
}

func (fs *FileSystem) captureTrashTransferRootIdentity(root *storagePathRoot) (string, error) {
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return "", err
	}
	info, err := root.handle.Lstat(".")
	if err != nil {
		return "", mapStorageRootPathError(err)
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(info)
	if identity == "" {
		return "", ErrDeleteIdentityUnavailable
	}
	return identity, nil
}

func (fs *FileSystem) verifyTrashTransferRootIdentities(record *trashTransferJournalRecord) error {
	if record == nil {
		return ErrDeleteTargetChanged
	}
	filesIdentity, trashIdentity, err := fs.captureTrashTransferRootIdentities()
	if err != nil {
		return err
	}
	if filesIdentity != record.FilesRootIdentity || trashIdentity != record.TrashRootIdentity {
		return errors.New("Trash transfer storage root identity changed")
	}
	return nil
}

func (fs *FileSystem) scanTrashTransferTree(ctx context.Context, root *storagePathRoot, rootRel string, expected []trashTransferManifestEntry, allowMissing bool) ([]trashTransferManifestEntry, map[string]os.FileInfo, error) {
	rootRel = filepath.Clean(rootRel)
	if rootRel == "." || filepath.IsAbs(rootRel) {
		return nil, nil, ErrDeleteTargetChanged
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, nil, err
	}
	rootAbs := storageAbsolutePath(root, rootRel)
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(rootAbs); err != nil {
		return nil, nil, err
	}
	expectedByPath := make(map[string]trashTransferManifestEntry, len(expected))
	for _, entry := range expected {
		expectedByPath[entry.Path] = entry
	}
	actual := make([]trashTransferManifestEntry, 0, len(expected))
	identities := make(map[string]os.FileInfo, len(expected))

	var scan func(string, string) error
	scan = func(rel, suffix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.handle.Lstat(rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrNotRegular
		}
		entry := trashTransferManifestEntry{
			Path:            suffix,
			Kind:            "file",
			Mode:            uint32(storagePreservedMode(info.Mode())),
			Size:            info.Size(),
			ModTimeUnixNano: info.ModTime().UnixNano(),
			Identity:        workspace.PersistentIdentityTokenForFileInfo(info),
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
			if !ok || !trashTransferEntryMatches(want, entry, allowMissing) {
				return ErrDeleteTargetChanged
			}
		}
		identity := deleteStageIdentity(info)
		if identity == "" {
			return ErrDeleteIdentityUnavailable
		}

		if info.IsDir() {
			dir, err := rootio.OpenDirNoFollow(root.handle, rel)
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
			current, err := root.handle.Lstat(rel)
			if err != nil || !sameDeleteStageEntry(info, identity, current) {
				return errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
			}
			return nil
		}

		file, err := rootio.OpenRegularFileNoFollow(root.handle, rel)
		if err != nil {
			return mapStorageRootPathError(err)
		}
		opened, err := file.Stat()
		if err != nil || !sameDeleteStageEntry(info, identity, opened) {
			_ = file.Close()
			return errors.Join(ErrDeleteTargetChanged, err)
		}
		hashFile := fs.hashTrashTransferFile
		if hashFile == nil {
			hashFile = func(ctx context.Context, _ *storagePathRoot, _ string, file *os.File) (string, error) {
				return hashOpenWorkspaceFileContext(ctx, file)
			}
		}
		hash, hashErr := hashFile(ctx, root, rel, file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		current, pathErr := root.handle.Lstat(rel)
		if hashErr != nil || statErr != nil || closeErr != nil || pathErr != nil || !sameDeleteStageEntry(info, identity, after) || !sameDeleteStageEntry(info, identity, current) {
			return errors.Join(ErrDeleteTargetChanged, hashErr, statErr, closeErr, mapStorageRootPathError(pathErr))
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
	if err := fs.captureDeleteMountBoundary(root.absRoot).checkHostTree(rootAbs); err != nil {
		return nil, nil, err
	}
	if err := fs.checkTrashTransferRootIdentity(root); err != nil {
		return nil, nil, err
	}
	sort.Slice(actual, func(i, j int) bool { return actual[i].Path < actual[j].Path })
	return actual, identities, nil
}

func trashTransferEntryMatches(expected, actual trashTransferManifestEntry, allowPartial bool) bool {
	if expected.Path != actual.Path || expected.Kind != actual.Kind || expected.Size != actual.Size || expected.Identity != actual.Identity {
		return false
	}
	if (!allowPartial || expected.Kind != "dir") && expected.ModTimeUnixNano != actual.ModTimeUnixNano {
		return false
	}
	if expected.Mode == actual.Mode {
		return true
	}
	if !allowPartial || expected.Kind != "dir" {
		return false
	}
	expectedMode := os.FileMode(expected.Mode)
	return actual.Mode == uint32(storagePreservedMode(expectedMode)|0o700)
}

func (fs *FileSystem) scanDeleteTrashTransferItem(ctx context.Context, itemRel, expectedIdentity string, expected []trashTransferManifestEntry, allowMissing bool) (string, map[string]os.FileInfo, error) {
	itemRel = filepath.Clean(itemRel)
	if itemRel == "." || filepath.IsAbs(itemRel) {
		return "", nil, ErrDeleteTargetChanged
	}
	trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	if err := fs.checkTrashTransferRootIdentity(trashRoot); err != nil {
		return "", nil, err
	}
	itemInfo, err := fs.trashRootHandle.Lstat(itemRel)
	if err != nil {
		return "", nil, mapStorageRootPathError(err)
	}
	if !itemInfo.IsDir() || itemInfo.Mode()&os.ModeSymlink != 0 {
		return "", nil, ErrNotRegular
	}
	identity := workspace.PersistentIdentityTokenForFileInfo(itemInfo)
	if identity == "" {
		return "", nil, ErrDeleteIdentityUnavailable
	}
	if expectedIdentity != "" && identity != expectedIdentity {
		return "", nil, ErrDeleteTargetChanged
	}
	dir, err := rootio.OpenDirNoFollow(fs.trashRootHandle, itemRel)
	if err != nil {
		return "", nil, mapStorageRootPathError(err)
	}
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(itemInfo, opened) {
		_ = dir.Close()
		return "", nil, errors.Join(ErrDeleteTargetChanged, err)
	}
	children, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil {
		return "", nil, errors.Join(readErr, closeErr)
	}
	if allowMissing && len(children) == 0 {
		return identity, map[string]os.FileInfo{".": itemInfo}, nil
	}
	if len(children) != 1 || children[0].Name() != "content" {
		return "", nil, ErrDeleteTargetChanged
	}
	_, contentIdentities, err := fs.scanTrashTransferTree(ctx, trashRoot, filepath.Join(itemRel, "content"), expected, allowMissing)
	if err != nil {
		return "", nil, err
	}
	current, err := fs.trashRootHandle.Lstat(itemRel)
	if err != nil || workspace.PersistentIdentityTokenForFileInfo(current) != identity || !os.SameFile(itemInfo, current) {
		return "", nil, errors.Join(ErrDeleteTargetChanged, mapStorageRootPathError(err))
	}
	identities := make(map[string]os.FileInfo, len(contentIdentities)+1)
	identities["."] = itemInfo
	for suffix, info := range contentIdentities {
		entryPath := "content"
		if suffix != "." {
			entryPath = path.Join(entryPath, filepath.ToSlash(suffix))
		}
		identities[entryPath] = info
	}
	return identity, identities, nil
}

func (fs *FileSystem) publishDeleteTrashTransferItem(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferDeleteToTrash || record.Decision != trashTransferReady {
		return ErrDeleteTargetChanged
	}
	stageRel := filepath.FromSlash(record.TrashStagePath)
	targetRel := filepath.FromSlash(record.Item.ID)
	if _, _, err := fs.scanDeleteTrashTransferItem(ctx, stageRel, record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
		return err
	}
	if _, err := fs.trashRootHandle.Lstat(targetRel); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapStorageRootPathError(err)
	}
	if err := rootio.RenameNoFollow(fs.trashRootHandle, stageRel, targetRel); err != nil {
		return mapStorageCreateTargetError(mapStorageRootPathError(err))
	}
	trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
	if err := syncStorageManagedRenameDirs(trashRoot, stageRel, trashRoot, targetRel); err != nil {
		return fmt.Errorf("sync published delete-to-Trash item: %w", err)
	}
	_, _, err := fs.scanDeleteTrashTransferItem(ctx, targetRel, record.TrashStageIdentity, record.ReplicaManifest, false)
	return err
}

func (fs *FileSystem) removeDeleteTrashTransferItem(ctx context.Context, itemRel, expectedIdentity string, expected []trashTransferManifestEntry, allowMissing bool) error {
	return fs.removeDeleteTrashTransferItemAfterPreflight(ctx, itemRel, expectedIdentity, expected, allowMissing, nil)
}

func (fs *FileSystem) removeDeleteTrashTransferItemAfterPreflight(
	ctx context.Context,
	itemRel string,
	expectedIdentity string,
	expected []trashTransferManifestEntry,
	allowMissing bool,
	beforeRemove func() error,
) error {
	itemRel = filepath.Clean(itemRel)
	if _, err := fs.trashRootHandle.Lstat(itemRel); err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			if beforeRemove != nil {
				return beforeRemove()
			}
			return nil
		}
		return mapStorageRootPathError(err)
	}
	_, identities, err := fs.scanDeleteTrashTransferItem(ctx, itemRel, expectedIdentity, expected, allowMissing)
	if err != nil {
		return err
	}
	parentRel := filepath.Dir(itemRel)
	parent, err := rootio.OpenDirNoFollow(fs.trashRootHandle, parentRel)
	if err != nil {
		return mapStorageRootPathError(err)
	}
	defer parent.Close()
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return err
		}
	}
	base := filepath.Base(itemRel)
	if err := rootio.RemoveAllFromDirNoFollowCheckedInPlace(parent, base, func(entryPath string, info os.FileInfo) error {
		suffix := "."
		if entryPath != base {
			suffix = filepath.ToSlash(strings.TrimPrefix(entryPath, base+string(filepath.Separator)))
		}
		expectedInfo, ok := identities[suffix]
		if !ok || !os.SameFile(expectedInfo, info) || workspace.PersistentIdentityTokenForFileInfo(expectedInfo) != workspace.PersistentIdentityTokenForFileInfo(info) {
			return rootio.ErrEntryChanged
		}
		return nil
	}); err != nil {
		return mapStorageRootPathError(err)
	}
	return syncManagedStorageDir(fs.trashRootHandle, parentRel, filepath.Join(fs.trashRoot, parentRel))
}

func trashTransferOperationKind(record *trashTransferJournalRecord) string {
	if record != nil && record.Kind == trashTransferRestoreFromTrash {
		return versionstore.TrashOperationKindRestoreFromTrash
	}
	return versionstore.TrashOperationKindDeleteToTrash
}

func trashTransferOperationForRecord(record *trashTransferJournalRecord) (*versionstore.TrashOperation, error) {
	hash, err := trashTransferJournalHash(record)
	if err != nil {
		return nil, err
	}
	return &versionstore.TrashOperation{
		ID:                 record.OperationID,
		Kind:               trashTransferOperationKind(record),
		TrashID:            record.Item.ID,
		JournalHash:        hash,
		ParticipantPayload: append([]byte{}, record.ParticipantPayload...),
	}, nil
}

func trashTransferOperationMatches(record *trashTransferJournalRecord, operation *versionstore.TrashOperation) bool {
	if record == nil || operation == nil {
		return false
	}
	expected, err := trashTransferOperationForRecord(record)
	if err != nil {
		return false
	}
	return expected.ID == operation.ID &&
		expected.Kind == operation.Kind &&
		expected.TrashID == operation.TrashID &&
		expected.JournalHash == operation.JournalHash &&
		bytes.Equal(expected.ParticipantPayload, operation.ParticipantPayload)
}

func (fs *FileSystem) restorePreparedDeleteTransferSource(ctx context.Context, record *trashTransferJournalRecord) error {
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	originalRel := storageWorkspaceRelativeName(record.Item.OriginalPath)
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	originalInfo, originalErr := fs.filesRootHandle.Lstat(originalRel)
	stageInfo, stageErr := fs.filesRootHandle.Lstat(stageRel)
	originalExists := originalErr == nil
	stageExists := stageErr == nil
	if originalErr != nil && !errors.Is(originalErr, os.ErrNotExist) {
		return mapStorageRootPathError(originalErr)
	}
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return mapStorageRootPathError(stageErr)
	}
	if originalExists && stageExists {
		return errors.New("both original and staged delete-to-Trash source exist")
	}
	if !originalExists && !stageExists {
		return errors.New("neither original nor staged delete-to-Trash source exists")
	}
	if originalExists {
		if originalInfo.Mode()&os.ModeSymlink != 0 {
			return ErrNotRegular
		}
		_, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, originalRel, record.SourceManifest, false)
		return err
	}
	if stageInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegular
	}
	if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, stageRel, record.SourceManifest, false); err != nil {
		return err
	}
	if err := rootio.RenameNoFollow(fs.filesRootHandle, stageRel, originalRel); err != nil {
		return mapStorageRootPathError(err)
	}
	if err := syncStorageManagedRenameDirs(workspaceRoot, stageRel, workspaceRoot, originalRel); err != nil {
		return fmt.Errorf("sync restored delete-to-Trash source: %w", err)
	}
	_, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, originalRel, record.SourceManifest, false)
	return err
}

func (fs *FileSystem) preflightPreparedDeleteTrashTransferRollback(ctx context.Context, record *trashTransferJournalRecord) error {
	if err := fs.verifyTrashTransferRootIdentities(record); err != nil {
		return err
	}
	workspaceRoot := &storagePathRoot{absRoot: fs.workspace.Root(), handle: fs.filesRootHandle}
	originalRel := storageWorkspaceRelativeName(record.Item.OriginalPath)
	stageRel := storageWorkspaceRelativeName(record.WorkspaceStagePath)
	originalInfo, originalErr := fs.filesRootHandle.Lstat(originalRel)
	stageInfo, stageErr := fs.filesRootHandle.Lstat(stageRel)
	originalExists := originalErr == nil
	stageExists := stageErr == nil
	if originalErr != nil && !errors.Is(originalErr, os.ErrNotExist) {
		return mapStorageRootPathError(originalErr)
	}
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return mapStorageRootPathError(stageErr)
	}
	if originalExists == stageExists {
		if originalExists {
			return errors.New("both original and staged delete-to-Trash source exist")
		}
		return errors.New("neither original nor staged delete-to-Trash source exists")
	}
	sourceRel := originalRel
	sourceInfo := originalInfo
	if stageExists {
		sourceRel = stageRel
		sourceInfo = stageInfo
	}
	if sourceInfo == nil || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegular
	}
	if _, _, err := fs.scanTrashTransferTree(ctx, workspaceRoot, sourceRel, record.SourceManifest, false); err != nil {
		return err
	}

	trashStageRel := filepath.FromSlash(record.TrashStagePath)
	canonicalRel := filepath.FromSlash(record.Item.ID)
	_, trashStageErr := fs.trashRootHandle.Lstat(trashStageRel)
	_, canonicalErr := fs.trashRootHandle.Lstat(canonicalRel)
	trashStageExists := trashStageErr == nil
	canonicalExists := canonicalErr == nil
	if trashStageErr != nil && !errors.Is(trashStageErr, os.ErrNotExist) {
		return mapStorageRootPathError(trashStageErr)
	}
	if canonicalErr != nil && !errors.Is(canonicalErr, os.ErrNotExist) {
		return mapStorageRootPathError(canonicalErr)
	}
	if trashStageExists && canonicalExists {
		return errors.New("both staged and canonical delete-to-Trash replicas exist")
	}
	switch record.Decision {
	case trashTransferPrepared:
		if canonicalExists {
			return errors.New("prepared delete-to-Trash transfer has a canonical replica")
		}
		if trashStageExists {
			trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
			if _, _, err := fs.inspectPreparedTrashTransferOwnedContainer(
				trashRoot,
				trashStageRel,
				record,
				trashTransferOwnershipRoleTrashContainer,
				record.TrashStageIdentity,
			); err != nil {
				return err
			}
		}
	case trashTransferCopying:
		if canonicalExists {
			return errors.New("copying delete-to-Trash transfer has a canonical replica")
		}
		if trashStageExists {
			trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
			if _, err := fs.scanTrashTransferOwnedContainerPartialWithOwnership(
				ctx,
				trashRoot,
				trashStageRel,
				record.TrashStageIdentity,
				record.SourceManifest,
				record,
				trashTransferOwnershipRoleTrashContainer,
			); err != nil {
				return err
			}
		}
	case trashTransferReady:
		if trashStageExists || canonicalExists {
			replicaRel := trashStageRel
			if canonicalExists {
				replicaRel = canonicalRel
			}
			if _, _, err := fs.scanDeleteTrashTransferItem(ctx, replicaRel, record.TrashStageIdentity, record.ReplicaManifest, false); err != nil {
				return err
			}
		}
	default:
		return ErrDeleteTargetChanged
	}

	if len(record.ParticipantPayload) != 0 {
		hooks := fs.trashParticipantHooksSnapshot()
		if hooks.RollbackDelete == nil {
			return errors.New("delete-to-Trash participant rollback is unavailable")
		}
	}
	return nil
}

func (fs *FileSystem) removePreparedDeleteTransferReplica(ctx context.Context, record *trashTransferJournalRecord) error {
	stageRel := filepath.FromSlash(record.TrashStagePath)
	canonicalRel := filepath.FromSlash(record.Item.ID)
	_, stageErr := fs.trashRootHandle.Lstat(stageRel)
	_, canonicalErr := fs.trashRootHandle.Lstat(canonicalRel)
	stageExists := stageErr == nil
	canonicalExists := canonicalErr == nil
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return mapStorageRootPathError(stageErr)
	}
	if canonicalErr != nil && !errors.Is(canonicalErr, os.ErrNotExist) {
		return mapStorageRootPathError(canonicalErr)
	}
	if stageExists && canonicalExists {
		return errors.New("both staged and canonical delete-to-Trash replicas exist")
	}
	if !stageExists && !canonicalExists {
		return nil
	}
	switch record.Decision {
	case trashTransferPrepared:
		if canonicalExists {
			return errors.New("prepared delete-to-Trash transfer has a canonical replica")
		}
		trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
		return fs.removeTrashTransferOwnedContainerPartialWithOwnership(
			ctx,
			trashRoot,
			stageRel,
			record.TrashStageIdentity,
			record.SourceManifest,
			record,
			trashTransferOwnershipRoleTrashContainer,
		)
	case trashTransferCopying:
		if canonicalExists {
			return errors.New("copying delete-to-Trash transfer has a canonical replica")
		}
		trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
		return fs.removeTrashTransferOwnedContainerPartialWithOwnership(
			ctx,
			trashRoot,
			stageRel,
			record.TrashStageIdentity,
			record.SourceManifest,
			record,
			trashTransferOwnershipRoleTrashContainer,
		)
	case trashTransferReady:
		if stageExists {
			return fs.removeDeleteTrashTransferItem(ctx, stageRel, record.TrashStageIdentity, record.ReplicaManifest, false)
		}
		return fs.removeDeleteTrashTransferItem(ctx, canonicalRel, record.TrashStageIdentity, record.ReplicaManifest, false)
	default:
		return ErrDeleteTargetChanged
	}
}

func (fs *FileSystem) rollbackPreparedDeleteTrashTransfer(ctx context.Context, record *trashTransferJournalRecord) error {
	if record == nil || record.Kind != trashTransferDeleteToTrash || record.Decision == trashTransferCommitted || record.Decision == trashTransferCompleted {
		return ErrDeleteTargetChanged
	}
	operations, err := fs.versions.ListTrashOperations(ctx)
	if err != nil {
		return err
	}
	for index := range operations {
		operation := &operations[index]
		if operation.ID != record.OperationID && operation.TrashID != record.Item.ID {
			continue
		}
		if trashTransferOperationMatches(record, operation) {
			return errors.New("committed delete-to-Trash operation cannot be rolled back")
		}
		return versionstore.ErrTrashOperationConflict
	}
	// Classify every owned path before making the first rollback mutation. A
	// partial replica without a durable manifest must remain untouched.
	if err := fs.preflightPreparedDeleteTrashTransferRollback(ctx, record); err != nil {
		return err
	}
	if record.Decision == trashTransferPrepared {
		trashStageRel := filepath.FromSlash(record.TrashStagePath)
		if _, err := fs.trashRootHandle.Lstat(trashStageRel); err == nil {
			trashRoot := &storagePathRoot{absRoot: fs.trashRoot, handle: fs.trashRootHandle}
			identity, _, err := fs.inspectPreparedTrashTransferOwnedContainer(
				trashRoot,
				trashStageRel,
				record,
				trashTransferOwnershipRoleTrashContainer,
				record.TrashStageIdentity,
			)
			if err != nil {
				return err
			}
			record.TrashStageIdentity = identity
		} else if !errors.Is(err, os.ErrNotExist) {
			return mapStorageRootPathError(err)
		}
	}
	if err := fs.restorePreparedDeleteTransferSource(ctx, record); err != nil {
		return fmt.Errorf("restore prepared delete-to-Trash source: %w", err)
	}
	if err := fs.rollbackTrashDeleteParticipant(ctx, record.OperationID, record.Item.OriginalPath, record.ParticipantPayload); err != nil {
		return fmt.Errorf("roll back delete-to-Trash participant: %w", err)
	}
	if err := fs.removePreparedDeleteTransferReplica(ctx, record); err != nil {
		return fmt.Errorf("remove prepared delete-to-Trash replica: %w", err)
	}
	return fs.rollbackTrashTransferJournal(record)
}
