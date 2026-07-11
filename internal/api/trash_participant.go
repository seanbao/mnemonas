package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const durableTrashParticipantPayloadVersion = 1

type durableTrashParticipantPayload struct {
	Version           int                   `json:"version"`
	DeleteOperationID string                `json:"delete_operation_id"`
	SourcePath        string                `json:"source_path"`
	Shares            []*share.Share        `json:"shares,omitempty"`
	Favorites         []*favorites.Favorite `json:"favorites,omitempty"`
}

type durableTrashParticipant struct {
	server *Server
}

// newDurableTrashParticipantHooks constructs the durable share, favorites,
// and committed path-notification participant for live Trash transfers.
func newDurableTrashParticipantHooks(server *Server) storage.TrashParticipantHooks {
	participant := &durableTrashParticipant{server: server}
	return storage.TrashParticipantHooks{
		PrepareDelete:         participant.prepareDelete,
		ApplyDelete:           participant.applyDelete,
		RollbackDelete:        participant.rollbackDelete,
		CompleteDelete:        participant.completeDelete,
		ApplyRestore:          participant.applyRestore,
		CompleteRestore:       participant.completeRestore,
		ValidatePurge:         participant.validatePurge,
		CompletePurge:         participant.completePurge,
		RecoveryStateReliable: participant.recoveryStateReliable,
	}
}

func (p *durableTrashParticipant) recoveryStateReliable() error {
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}
	var recoveryErr error
	if p.server.shareStore != nil && p.server.shareStore.RecoveredFromCorruption() {
		recoveryErr = errors.Join(recoveryErr, errors.New("share store recovery evidence was rebuilt after corruption"))
	}
	if p.server.favoritesStore != nil && p.server.favoritesStore.RecoveredFromCorruption() {
		recoveryErr = errors.Join(recoveryErr, errors.New("favorites store recovery evidence was rebuilt after corruption"))
	}
	return recoveryErr
}

func (p *durableTrashParticipant) prepareDelete(ctx context.Context, operationID, targetPath string) ([]byte, error) {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.server == nil {
		return nil, errors.New("durable Trash participant server is unavailable")
	}
	normalizedPath, err := normalizeDurableTrashParticipantPath(targetPath)
	if err != nil {
		return nil, err
	}

	var shares []*share.Share
	if p.server.shareStore != nil {
		shares, err = p.server.shareStore.SnapshotDeleteExact(normalizedPath)
		if err != nil {
			return nil, fmt.Errorf("snapshot shares before delete-to-Trash: %w", err)
		}
	}

	var favoriteItems []*favorites.Favorite
	if p.server.favoritesStore != nil {
		favoriteItems, err = p.server.favoritesStore.SnapshotDeleteExact(normalizedPath)
		if err != nil {
			return nil, fmt.Errorf("snapshot favorites before delete-to-Trash: %w", err)
		}
	}

	payload := durableTrashParticipantPayload{
		Version:           durableTrashParticipantPayloadVersion,
		DeleteOperationID: operationID,
		SourcePath:        normalizedPath,
		Shares:            shares,
		Favorites:         favoriteItems,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode durable Trash participant payload: %w", err)
	}
	return encoded, nil
}

func (p *durableTrashParticipant) applyDelete(ctx context.Context, operationID, targetPath string, encoded []byte, committed bool) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}

	payload, err := decodeDurableTrashParticipantPayload(encoded, targetPath)
	if err != nil {
		return err
	}
	if payload.DeleteOperationID != operationID {
		return errors.New("durable Trash participant delete operation ID mismatch")
	}
	if err := p.validateStores(payload); err != nil {
		return err
	}

	applyWarning, err := p.applyDeleteState(operationID, payload, committed)
	if err != nil {
		return err
	}
	if committed {
		if err := p.notifyCommittedPathDeleted(payload.SourcePath); err != nil {
			return err
		}
	}
	return applyWarning
}

func (p *durableTrashParticipant) rollbackDelete(ctx context.Context, operationID, targetPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}

	payload, err := decodeDurableTrashParticipantPayload(encoded, targetPath)
	if err != nil {
		return err
	}
	if payload.DeleteOperationID != operationID {
		return errors.New("durable Trash participant delete operation ID mismatch")
	}
	if err := p.validateStores(payload); err != nil {
		return err
	}
	return p.rollbackDeleteState(operationID)
}

func (p *durableTrashParticipant) applyRestore(ctx context.Context, operationID, originalPath, restoredPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}

	payload, err := decodeDurableTrashParticipantPayload(encoded, originalPath)
	if err != nil {
		return err
	}
	originalState := deletedPathRestoreState{
		Shares:    payload.Shares,
		Favorites: payload.Favorites,
	}
	state := relocateDeletedPathRestoreState(originalState, payload.SourcePath, restoredPath)
	if err := p.validateStores(&durableTrashParticipantPayload{Shares: state.Shares, Favorites: state.Favorites}); err != nil {
		return err
	}

	var applyWarning error
	if len(state.Shares) > 0 {
		if err := p.server.shareStore.ApplyTrashRestoreOperation(operationID, payload.DeleteOperationID, originalState.Shares, state.Shares); err != nil {
			if share.IsPersistenceWarning(err) {
				applyWarning = errors.Join(applyWarning, fmt.Errorf("restore shares from Trash participant: %w", err))
			} else {
				return fmt.Errorf("restore shares from Trash participant: %w", err)
			}
		}
	}
	if len(state.Favorites) > 0 {
		if err := p.server.favoritesStore.ApplyTrashRestoreOperation(operationID, payload.DeleteOperationID, originalState.Favorites, state.Favorites); err != nil {
			if favorites.IsPersistenceWarning(err) {
				applyWarning = errors.Join(applyWarning, fmt.Errorf("restore favorites from Trash participant: %w", err))
			} else {
				return errors.Join(applyWarning, fmt.Errorf("restore favorites from Trash participant: %w", err))
			}
		}
	}
	return workspace.WrapVisibleMutationWarning(applyWarning)
}

func (p *durableTrashParticipant) completeDelete(ctx context.Context, operationID, targetPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}
	payload, err := decodeDurableTrashParticipantPayload(encoded, targetPath)
	if err != nil {
		return err
	}
	if payload.DeleteOperationID != operationID {
		return errors.New("durable Trash participant delete operation ID mismatch")
	}
	if err := p.validateStores(payload); err != nil {
		return err
	}

	var completeWarning error
	var completeErr error
	if len(payload.Favorites) > 0 {
		if err := p.server.favoritesStore.CompleteTrashDeleteOperation(operationID); err != nil {
			wrapped := fmt.Errorf("complete favorites delete participant: %w", err)
			if favorites.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if len(payload.Shares) > 0 {
		if err := p.server.shareStore.CompleteTrashDeleteOperation(operationID); err != nil {
			wrapped := fmt.Errorf("complete shares delete participant: %w", err)
			if share.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if completeErr != nil {
		return errors.Join(completeErr, completeWarning)
	}
	return workspace.WrapVisibleMutationWarning(completeWarning)
}

func (p *durableTrashParticipant) completeRestore(ctx context.Context, operationID, originalPath, restoredPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}
	payload, err := decodeDurableTrashParticipantPayload(encoded, originalPath)
	if err != nil {
		return err
	}
	state := relocateDeletedPathRestoreState(deletedPathRestoreState{
		Shares:    payload.Shares,
		Favorites: payload.Favorites,
	}, payload.SourcePath, restoredPath)
	if err := p.validateStores(&durableTrashParticipantPayload{Shares: state.Shares, Favorites: state.Favorites}); err != nil {
		return err
	}

	var completeWarning error
	var completeErr error
	if len(state.Favorites) > 0 {
		if err := p.server.favoritesStore.CompleteTrashRestoreOperation(operationID); err != nil {
			wrapped := fmt.Errorf("complete favorites restore participant: %w", err)
			if favorites.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if len(state.Shares) > 0 {
		if err := p.server.shareStore.CompleteTrashRestoreOperation(operationID); err != nil {
			wrapped := fmt.Errorf("complete shares restore participant: %w", err)
			if share.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if completeErr != nil {
		return errors.Join(completeErr, completeWarning)
	}
	return workspace.WrapVisibleMutationWarning(completeWarning)
}

func (p *durableTrashParticipant) completePurge(ctx context.Context, operationID, originalPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}
	payload, err := decodeDurableTrashParticipantPayload(encoded, originalPath)
	if err != nil {
		return err
	}
	if err := p.validateStores(payload); err != nil {
		return err
	}

	var completeWarning error
	var completeErr error
	if len(payload.Favorites) > 0 {
		if err := p.server.favoritesStore.PurgeCompletedTrashDeleteOperation(payload.DeleteOperationID); err != nil {
			wrapped := fmt.Errorf("purge favorites delete ownership: %w", err)
			if favorites.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if len(payload.Shares) > 0 {
		if err := p.server.shareStore.PurgeCompletedTrashDeleteOperation(payload.DeleteOperationID); err != nil {
			wrapped := fmt.Errorf("purge shares delete ownership: %w", err)
			if share.IsPersistenceWarning(err) {
				completeWarning = errors.Join(completeWarning, wrapped)
			} else {
				completeErr = errors.Join(completeErr, wrapped)
			}
		}
	}
	if completeErr != nil {
		return errors.Join(completeErr, completeWarning)
	}
	return workspace.WrapVisibleMutationWarning(completeWarning)
}

func (p *durableTrashParticipant) validatePurge(ctx context.Context, operationID, originalPath string, encoded []byte) error {
	if err := validateDurableTrashParticipantOperationID(operationID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.server == nil {
		return errors.New("durable Trash participant server is unavailable")
	}
	payload, err := decodeDurableTrashParticipantPayload(encoded, originalPath)
	if err != nil {
		return err
	}
	return p.validateStores(payload)
}

func (p *durableTrashParticipant) validateStores(payload *durableTrashParticipantPayload) error {
	if len(payload.Shares) > 0 && p.server.shareStore == nil {
		return errors.New("share store unavailable for durable Trash participant")
	}
	if len(payload.Favorites) > 0 && p.server.favoritesStore == nil {
		return errors.New("favorites store unavailable for durable Trash participant")
	}
	return nil
}

func (p *durableTrashParticipant) applyDeleteState(operationID string, payload *durableTrashParticipantPayload, committed bool) (warningErr, hardErr error) {
	var applyWarning error
	if len(payload.Shares) > 0 {
		if err := p.server.shareStore.ApplyTrashDeleteOperation(operationID, payload.Shares, committed); err != nil {
			if share.IsPersistenceWarning(err) {
				applyWarning = errors.Join(applyWarning, fmt.Errorf("disable shares after delete: %w", err))
			} else {
				return nil, fmt.Errorf("disable shares after delete: %w", err)
			}
		}
	}

	if len(payload.Favorites) > 0 {
		if err := p.server.favoritesStore.ApplyTrashDeleteOperation(operationID, payload.Favorites, committed); err != nil {
			if favorites.IsPersistenceWarning(err) {
				applyWarning = errors.Join(applyWarning, fmt.Errorf("remove favorites after delete: %w", err))
			} else {
				if !committed && len(payload.Shares) > 0 {
					if rollbackErr := p.server.shareStore.RollbackTrashDeleteOperation(operationID); rollbackErr != nil {
						return nil, errors.Join(
							fmt.Errorf("remove favorites after delete: %w", err),
							fmt.Errorf("rollback shares after delete: %w", rollbackErr),
						)
					}
				}
				return nil, fmt.Errorf("remove favorites after delete: %w", err)
			}
		}
	}

	return workspace.WrapVisibleMutationWarning(applyWarning), nil
}

func (p *durableTrashParticipant) rollbackDeleteState(operationID string) error {
	var rollbackErr error
	if p.server.favoritesStore != nil {
		if err := p.server.favoritesStore.RollbackTrashDeleteOperation(operationID); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore favorites after delete rollback: %w", err))
		}
	}
	if p.server.shareStore != nil {
		if err := p.server.shareStore.RollbackTrashDeleteOperation(operationID); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore shares after delete rollback: %w", err))
		}
	}
	return rollbackErr
}

func (p *durableTrashParticipant) notifyCommittedPathDeleted(targetPath string) error {
	if p.server.afterPathDeleted == nil {
		return nil
	}
	result := p.server.afterPathDeleted(targetPath)
	if result != nil && len(result.RestoreData) > 0 {
		return errors.New("committed path-delete listener returned unsupported restore metadata")
	}
	return nil
}

func validateDurableTrashParticipantOperationID(operationID string) error {
	if len(operationID) != 32 {
		return errors.New("invalid durable Trash participant operation ID")
	}
	for index := range operationID {
		character := operationID[index]
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return errors.New("invalid durable Trash participant operation ID")
	}
	return nil
}

func decodeDurableTrashParticipantPayload(encoded []byte, expectedSourcePath string) (*durableTrashParticipantPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var payload durableTrashParticipantPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode durable Trash participant payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("durable Trash participant payload contains trailing data")
	}
	if err := validateDurableTrashParticipantPayload(&payload, expectedSourcePath); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode canonical durable Trash participant payload: %w", err)
	}
	if !bytes.Equal(encoded, canonical) {
		return nil, errors.New("durable Trash participant payload is not canonical JSON")
	}
	return &payload, nil
}

func validateDurableTrashParticipantPayload(payload *durableTrashParticipantPayload, expectedSourcePath string) error {
	if payload.Version != durableTrashParticipantPayloadVersion {
		return fmt.Errorf("unsupported durable Trash participant payload version %d", payload.Version)
	}
	if err := validateDurableTrashParticipantOperationID(payload.DeleteOperationID); err != nil {
		return fmt.Errorf("invalid durable Trash participant delete operation ID: %w", err)
	}
	normalizedExpectedPath, err := normalizeDurableTrashParticipantPath(expectedSourcePath)
	if err != nil {
		return err
	}
	normalizedSourcePath, err := normalizeDurableTrashParticipantPath(payload.SourcePath)
	if err != nil || payload.SourcePath != normalizedExpectedPath || payload.SourcePath != normalizedSourcePath {
		return errors.New("durable Trash participant source path mismatch")
	}

	shareIDs := make(map[string]struct{}, len(payload.Shares))
	for _, item := range payload.Shares {
		if item == nil || !item.Enabled || !durableTrashParticipantPathWithin(payload.SourcePath, item.Path) {
			return errors.New("durable Trash participant contains an invalid share snapshot")
		}
		if _, exists := shareIDs[item.ID]; exists {
			return errors.New("durable Trash participant contains duplicate share snapshots")
		}
		shareIDs[item.ID] = struct{}{}
	}
	if !sort.SliceIsSorted(payload.Shares, func(i, j int) bool {
		left := payload.Shares[i]
		right := payload.Shares[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.CreatedBy != right.CreatedBy {
			return left.CreatedBy < right.CreatedBy
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	}) {
		return errors.New("durable Trash participant share snapshots are not canonical")
	}

	type favoriteKey struct {
		userID string
		path   string
	}
	favoriteKeys := make(map[favoriteKey]struct{}, len(payload.Favorites))
	for _, item := range payload.Favorites {
		if item == nil || !durableTrashParticipantPathWithin(payload.SourcePath, item.Path) {
			return errors.New("durable Trash participant contains an invalid favorite snapshot")
		}
		key := favoriteKey{userID: item.UserID, path: item.Path}
		if _, exists := favoriteKeys[key]; exists {
			return errors.New("durable Trash participant contains duplicate favorite snapshots")
		}
		favoriteKeys[key] = struct{}{}
	}
	if !sort.SliceIsSorted(payload.Favorites, func(i, j int) bool {
		left := payload.Favorites[i]
		right := payload.Favorites[j]
		if left.UserID != right.UserID {
			return left.UserID < right.UserID
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.CreatedAt.Before(right.CreatedAt)
	}) {
		return errors.New("durable Trash participant favorite snapshots are not canonical")
	}
	return nil
}

func durableTrashParticipantPathWithin(sourcePath, candidatePath string) bool {
	normalizedCandidatePath, err := normalizeDurableTrashParticipantPath(candidatePath)
	if err != nil || candidatePath != normalizedCandidatePath {
		return false
	}
	if sourcePath == "/" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return candidatePath == sourcePath || strings.HasPrefix(candidatePath, sourcePath+"/")
}

func normalizeDurableTrashParticipantPath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if strings.TrimSpace(normalized) == "" || strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", errors.New("invalid durable Trash participant path")
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("invalid durable Trash participant path")
		}
	}
	return path.Clean("/" + normalized), nil
}
