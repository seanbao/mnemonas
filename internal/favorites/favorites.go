// Package favorites provides file favorites functionality for MnemoNAS
package favorites

import (
	"bytes"
	"crypto/rand"
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
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var (
	ErrFavoriteNotFound              = errors.New("favorite not found")
	ErrAlreadyFavorited              = errors.New("already favorited")
	ErrTrashDeleteOperationConflict  = errors.New("trash delete operation conflict")
	ErrTrashRestoreOperationConflict = errors.New("trash restore operation conflict")
	errInvalidFavoritePath           = errors.New("invalid favorite path")
	errInvalidTrashDeleteOperation   = errors.New("invalid trash delete operation")
	errInvalidTrashRestoreOperation  = errors.New("invalid trash restore operation")
	errFavoritesStoreSymlink         = errors.New("favorites store path must not be a symlink")
)

// PersistenceWarningError reports that the favorites mutation is already
// visible on disk, but the final directory fsync did not complete.
type PersistenceWarningError struct {
	err error
}

func (e *PersistenceWarningError) Error() string {
	return e.err.Error()
}

func (e *PersistenceWarningError) Unwrap() error {
	return e.err
}

func WrapPersistenceWarning(err error) error {
	if err == nil {
		return nil
	}
	var warningErr *PersistenceWarningError
	if errors.As(err, &warningErr) {
		return err
	}
	return &PersistenceWarningError{err: err}
}

func IsPersistenceWarning(err error) bool {
	var warningErr *PersistenceWarningError
	return errors.As(err, &warningErr)
}

// Favorite represents a favorited file or folder
type Favorite struct {
	Path      string    `json:"path"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	Note      string    `json:"note,omitempty"`
}

// Store manages favorites persistence
type Store struct {
	mu      sync.RWMutex
	writeMu sync.Mutex
	// map[userID]map[path]*Favorite
	data                    map[string]map[string]*Favorite
	trashDeleteOperations   map[string]*trashDeleteOperation
	trashRestoreOperations  map[string]*trashRestoreOperation
	filePath                string
	version                 uint64
	recoveredFromCorruption bool
}

var favoritesStoreWriter = writeFavoritesStoreFile
var syncFavoritesStoreDir = syncFavoritesDir
var syncFavoritesStoreRootDir = syncFavoritesRootDir
var afterValidateFavoritesStorePath = func() {}

var favoritesStoreDirRootsMu sync.RWMutex
var favoritesStoreDirRoots = map[string]*os.Root{}

const favoritesStoreRootEscapeError = "path escapes from parent"

const favoritesStoreVersion = 1

type favoritePathIdentity struct {
	UserID string `json:"user_id"`
	Path   string `json:"path"`
}

type trashDeleteOperation struct {
	Planned        []*Favorite            `json:"planned"`
	Removed        []*Favorite            `json:"removed"`
	RestoreBlocked []favoritePathIdentity `json:"restore_blocked"`
	Committed      bool                   `json:"committed"`
	Completed      bool                   `json:"completed"`
}

type trashRestoreOperation struct {
	DeleteOperationID string      `json:"delete_operation_id"`
	Original          []*Favorite `json:"original"`
	Relocated         []*Favorite `json:"relocated"`
	Restored          []*Favorite `json:"restored"`
}

type favoritesStoreState struct {
	Version                int                               `json:"version"`
	Favorites              []*Favorite                       `json:"favorites"`
	TrashDeleteOperations  map[string]*trashDeleteOperation  `json:"trash_delete_operations"`
	TrashRestoreOperations map[string]*trashRestoreOperation `json:"trash_restore_operations"`
}

type favoritesSnapshot struct {
	data                   map[string]map[string]*Favorite
	trashDeleteOperations  map[string]*trashDeleteOperation
	trashRestoreOperations map[string]*trashRestoreOperation
	filePath               string
	version                uint64
}

func copyFavorite(fav *Favorite) *Favorite {
	if fav == nil {
		return nil
	}
	clone := *fav
	return &clone
}

func sortFavoritesCanonical(favorites []*Favorite) {
	sort.Slice(favorites, func(i, j int) bool {
		if favorites[i].UserID != favorites[j].UserID {
			return favorites[i].UserID < favorites[j].UserID
		}
		if favorites[i].Path != favorites[j].Path {
			return favorites[i].Path < favorites[j].Path
		}
		return favorites[i].CreatedAt.Before(favorites[j].CreatedAt)
	})
}

func normalizeStoredFavoritePath(rawPath string) (string, error) {
	return normalizeStoredFavoritePathWithPolicy(rawPath, false)
}

func normalizeLegacyStoredFavoritePath(rawPath string) (string, error) {
	return normalizeStoredFavoritePathWithPolicy(rawPath, true)
}

func normalizeStoredFavoritePathWithPolicy(rawPath string, allowCurrentDirSegment bool) (string, error) {
	normalized := strings.ReplaceAll(rawPath, "\\", "/")
	if containsFavoritePathControlCharacter(normalized) {
		return "", errInvalidFavoritePath
	}
	if strings.TrimSpace(normalized) == "" {
		return "", errInvalidFavoritePath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." || (!allowCurrentDirSegment && segment == ".") {
			return "", errInvalidFavoritePath
		}
	}
	cleaned := path.Clean("/" + normalized)
	if cleaned == "/" {
		return "", errInvalidFavoritePath
	}
	return cleaned, nil
}

func containsFavoritePathControlCharacter(filePath string) bool {
	return strings.IndexFunc(filePath, unicode.IsControl) >= 0
}

func normalizeRestoredFavorite(favorite *Favorite) (*Favorite, error) {
	if favorite == nil {
		return nil, errInvalidFavoritePath
	}
	normalized := copyFavorite(favorite)
	cleanPath, err := normalizeStoredFavoritePath(normalized.Path)
	if err != nil {
		return nil, err
	}
	normalized.Path = cleanPath
	return normalized, nil
}

// NewStore creates a new favorites store
func NewStore(filePath string) (*Store, error) {
	normalizedPath, err := ensureFavoritesStoreDirRoot(filePath, false)
	if err != nil {
		return nil, err
	}

	store := &Store{
		data:                   make(map[string]map[string]*Favorite),
		trashDeleteOperations:  make(map[string]*trashDeleteOperation),
		trashRestoreOperations: make(map[string]*trashRestoreOperation),
		filePath:               normalizedPath,
	}
	recoveryMarkerExists, err := favoritesStoreRecoveryMarkerExists(normalizedPath)
	if err != nil {
		return nil, err
	}
	store.recoveredFromCorruption = recoveryMarkerExists

	if err := store.load(); err != nil && !os.IsNotExist(err) {
		if recoveryMarkerExists {
			return nil, fmt.Errorf("failed to load favorites while recovery marker is present: %w", err)
		}
		if recoverErr := store.recoverCorruptFavorites(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to load favorites: %w", err),
				fmt.Errorf("recover corrupt favorites: %w", recoverErr),
			)
		}
	}

	return store, nil
}

// RecoveredFromCorruption reports whether this store instance replaced a
// corrupt persistence file during construction.
func (s *Store) RecoveredFromCorruption() bool {
	return s != nil && s.recoveredFromCorruption
}

func (s *Store) load() error {
	data, err := readRegisteredFavoritesStoreFile(s.filePath)
	if err != nil {
		return err
	}

	var state favoritesStoreState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return fmt.Errorf("failed to parse favorites file: %w", err)
	}
	if err := ensureFavoritesJSONEOF(decoder); err != nil {
		return fmt.Errorf("failed to parse favorites file: %w", err)
	}
	if state.Version != favoritesStoreVersion || state.Favorites == nil || state.TrashDeleteOperations == nil || state.TrashRestoreOperations == nil {
		return errors.New("invalid favorites store state")
	}

	s.data = make(map[string]map[string]*Favorite)
	s.trashDeleteOperations = make(map[string]*trashDeleteOperation, len(state.TrashDeleteOperations))
	s.trashRestoreOperations = make(map[string]*trashRestoreOperation, len(state.TrashRestoreOperations))
	needsRewrite := false
	for i, fav := range state.Favorites {
		if fav == nil {
			return fmt.Errorf("favorites file contains null entry at index %d", i)
		}
		cleanPath, err := normalizeLegacyStoredFavoritePath(fav.Path)
		if err != nil {
			needsRewrite = true
			continue
		}

		normalized := copyFavorite(fav)
		if normalized.Path != cleanPath {
			needsRewrite = true
		}
		normalized.Path = cleanPath
		if s.data[normalized.UserID] == nil {
			s.data[normalized.UserID] = make(map[string]*Favorite)
		}
		if _, exists := s.data[normalized.UserID][normalized.Path]; exists {
			needsRewrite = true
		}
		s.data[normalized.UserID][normalized.Path] = normalized
	}
	for operationID, operation := range state.TrashDeleteOperations {
		normalized, err := normalizeLoadedTrashDeleteOperation(operationID, operation)
		if err != nil {
			return err
		}
		s.trashDeleteOperations[operationID] = normalized
	}
	normalizedRestoreOperations, err := normalizeLoadedTrashRestoreOperations(state.TrashRestoreOperations, s.trashDeleteOperations)
	if err != nil {
		return err
	}
	s.trashRestoreOperations = normalizedRestoreOperations

	if needsRewrite {
		if err := saveFavoritesState(s.filePath, s.data, s.trashDeleteOperations, s.trashRestoreOperations); err != nil {
			if !IsPersistenceWarning(err) {
				return fmt.Errorf("persist normalized favorites: %w", err)
			}
		}
	}

	return nil
}

func (s *Store) recoverCorruptFavorites(loadErr error) error {
	if !isRecoverableFavoritesLoadError(loadErr) {
		return loadErr
	}

	if err := persistFavoritesStoreRecoveryMarker(s.filePath); err != nil {
		return err
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.filePath, time.Now().UnixNano())
	if err := renameRegisteredFavoritesStoreFile(s.filePath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt favorites file: %w", err)
	}
	if err := syncRegisteredFavoritesStoreDir(s.filePath); err != nil {
		if rollbackErr := renameRegisteredFavoritesStoreFile(corruptPath, s.filePath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt favorites directory: %w", err),
				fmt.Errorf("rollback corrupt favorites backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredFavoritesStoreDir(s.filePath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt favorites directory: %w", err),
				fmt.Errorf("sync corrupt favorites rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt favorites directory: %w", err)
	}

	s.data = make(map[string]map[string]*Favorite)
	s.trashDeleteOperations = make(map[string]*trashDeleteOperation)
	s.trashRestoreOperations = make(map[string]*trashRestoreOperation)
	s.recoveredFromCorruption = true
	return nil
}

func isRecoverableFavoritesLoadError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}

	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

func ensureFavoritesJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("favorites file contains trailing data")
	}
	return err
}

func saveFavoritesState(
	filePath string,
	dataByUser map[string]map[string]*Favorite,
	deleteOperations map[string]*trashDeleteOperation,
	restoreOperations map[string]*trashRestoreOperation,
) error {
	var favorites []*Favorite
	for _, userFavs := range dataByUser {
		for _, fav := range userFavs {
			favorites = append(favorites, copyFavorite(fav))
		}
	}
	sortFavoritesCanonical(favorites)
	state := favoritesStoreState{
		Version:                favoritesStoreVersion,
		Favorites:              favorites,
		TrashDeleteOperations:  cloneTrashDeleteOperations(deleteOperations),
		TrashRestoreOperations: cloneTrashRestoreOperations(restoreOperations),
	}
	if state.Favorites == nil {
		state.Favorites = []*Favorite{}
	}
	if state.TrashDeleteOperations == nil {
		state.TrashDeleteOperations = map[string]*trashDeleteOperation{}
	}
	if state.TrashRestoreOperations == nil {
		state.TrashRestoreOperations = map[string]*trashRestoreOperation{}
	}

	data, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize favorites: %w", err)
	}

	if err := favoritesStoreWriter(filePath, data); err != nil {
		return err
	}

	return nil
}

func cloneFavoritesData(data map[string]map[string]*Favorite) map[string]map[string]*Favorite {
	cloned := make(map[string]map[string]*Favorite, len(data))
	for userID, userFavs := range data {
		cloned[userID] = make(map[string]*Favorite, len(userFavs))
		for path, fav := range userFavs {
			cloned[userID][path] = copyFavorite(fav)
		}
	}
	return cloned
}

func cloneTrashDeleteOperation(operation *trashDeleteOperation) *trashDeleteOperation {
	if operation == nil {
		return nil
	}
	cloned := &trashDeleteOperation{
		Planned:        cloneFavoriteSlice(operation.Planned),
		Removed:        cloneFavoriteSlice(operation.Removed),
		RestoreBlocked: append([]favoritePathIdentity(nil), operation.RestoreBlocked...),
		Committed:      operation.Committed,
		Completed:      operation.Completed,
	}
	if cloned.Planned == nil {
		cloned.Planned = []*Favorite{}
	}
	if cloned.Removed == nil {
		cloned.Removed = []*Favorite{}
	}
	if cloned.RestoreBlocked == nil {
		cloned.RestoreBlocked = []favoritePathIdentity{}
	}
	return cloned
}

func cloneTrashDeleteOperations(operations map[string]*trashDeleteOperation) map[string]*trashDeleteOperation {
	cloned := make(map[string]*trashDeleteOperation, len(operations))
	for operationID, operation := range operations {
		cloned[operationID] = cloneTrashDeleteOperation(operation)
	}
	return cloned
}

func cloneTrashRestoreOperation(operation *trashRestoreOperation) *trashRestoreOperation {
	if operation == nil {
		return nil
	}
	cloned := &trashRestoreOperation{
		DeleteOperationID: operation.DeleteOperationID,
		Original:          cloneFavoriteSlice(operation.Original),
		Relocated:         cloneFavoriteSlice(operation.Relocated),
		Restored:          cloneFavoriteSlice(operation.Restored),
	}
	if cloned.Original == nil {
		cloned.Original = []*Favorite{}
	}
	if cloned.Relocated == nil {
		cloned.Relocated = []*Favorite{}
	}
	if cloned.Restored == nil {
		cloned.Restored = []*Favorite{}
	}
	return cloned
}

func cloneTrashRestoreOperations(operations map[string]*trashRestoreOperation) map[string]*trashRestoreOperation {
	cloned := make(map[string]*trashRestoreOperation, len(operations))
	for operationID, operation := range operations {
		cloned[operationID] = cloneTrashRestoreOperation(operation)
	}
	return cloned
}

func cloneFavoriteSlice(favorites []*Favorite) []*Favorite {
	if favorites == nil {
		return nil
	}
	cloned := make([]*Favorite, 0, len(favorites))
	for _, favorite := range favorites {
		cloned = append(cloned, copyFavorite(favorite))
	}
	return cloned
}

func validTrashDeleteOperationID(operationID string) bool {
	if len(operationID) != 32 {
		return false
	}
	for index := 0; index < len(operationID); index++ {
		character := operationID[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateTrashDeleteFavorite(favorite *Favorite) (*Favorite, error) {
	normalized, err := normalizeRestoredFavorite(favorite)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidTrashDeleteOperation, err)
	}
	if normalized.UserID == "" || normalized.UserID != strings.TrimSpace(normalized.UserID) || normalized.CreatedAt.IsZero() {
		return nil, errInvalidTrashDeleteOperation
	}
	return normalized, nil
}

func normalizeTrashDeleteFavorites(favorites []*Favorite) ([]*Favorite, error) {
	normalized := make([]*Favorite, 0, len(favorites))
	for _, favorite := range favorites {
		entry, err := validateTrashDeleteFavorite(favorite)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, entry)
	}
	sortFavoritesCanonical(normalized)
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].UserID == normalized[index].UserID && normalized[index-1].Path == normalized[index].Path {
			return nil, fmt.Errorf("%w: duplicate favorite path", errInvalidTrashDeleteOperation)
		}
	}
	return normalized, nil
}

func favoriteFullEqual(left, right *Favorite) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.UserID == right.UserID &&
		left.Path == right.Path &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.Note == right.Note
}

func favoriteSlicesEqual(left, right []*Favorite) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !favoriteFullEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func favoriteIdentityInSlice(favorite *Favorite, favorites []*Favorite) bool {
	for _, candidate := range favorites {
		if favoriteDeleteIdentityEqual(favorite, candidate) {
			return true
		}
	}
	return false
}

func sortFavoritePathIdentities(identities []favoritePathIdentity) {
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].UserID != identities[j].UserID {
			return identities[i].UserID < identities[j].UserID
		}
		return identities[i].Path < identities[j].Path
	})
}

func favoritePathIdentitiesEqual(left, right []favoritePathIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func favoritePathIdentityInSlice(identity favoritePathIdentity, identities []favoritePathIdentity) bool {
	for _, candidate := range identities {
		if candidate == identity {
			return true
		}
	}
	return false
}

func favoritePathInRemoved(identity favoritePathIdentity, removed []*Favorite) bool {
	for _, favorite := range removed {
		if favorite.UserID == identity.UserID && favorite.Path == identity.Path {
			return true
		}
	}
	return false
}

func normalizeLoadedTrashDeleteOperation(operationID string, operation *trashDeleteOperation) (*trashDeleteOperation, error) {
	if !validTrashDeleteOperationID(operationID) || operation == nil || operation.Planned == nil || operation.Removed == nil || operation.RestoreBlocked == nil {
		return nil, fmt.Errorf("%w: malformed operation %q", errInvalidTrashDeleteOperation, operationID)
	}
	if operation.Completed && !operation.Committed {
		return nil, fmt.Errorf("%w: completed operation %q is not committed", errInvalidTrashDeleteOperation, operationID)
	}
	planned, err := normalizeTrashDeleteFavorites(operation.Planned)
	if err != nil || !favoriteSlicesEqual(planned, operation.Planned) {
		return nil, fmt.Errorf("%w: invalid planned favorites for operation %q", errInvalidTrashDeleteOperation, operationID)
	}
	removed, err := normalizeTrashDeleteFavorites(operation.Removed)
	if err != nil || !favoriteSlicesEqual(removed, operation.Removed) {
		return nil, fmt.Errorf("%w: invalid removed favorites for operation %q", errInvalidTrashDeleteOperation, operationID)
	}
	for _, favorite := range removed {
		if !favoriteIdentityInSlice(favorite, planned) {
			return nil, fmt.Errorf("%w: removed favorite is outside the plan for operation %q", errInvalidTrashDeleteOperation, operationID)
		}
	}

	blocked := append([]favoritePathIdentity(nil), operation.RestoreBlocked...)
	for _, identity := range blocked {
		cleanPath, pathErr := normalizeStoredFavoritePath(identity.Path)
		if pathErr != nil || cleanPath != identity.Path || identity.UserID == "" || identity.UserID != strings.TrimSpace(identity.UserID) || !favoritePathInRemoved(identity, removed) {
			return nil, fmt.Errorf("%w: invalid restore block for operation %q", errInvalidTrashDeleteOperation, operationID)
		}
	}
	sortFavoritePathIdentities(blocked)
	if !favoritePathIdentitiesEqual(blocked, operation.RestoreBlocked) {
		return nil, fmt.Errorf("%w: non-canonical restore blocks for operation %q", errInvalidTrashDeleteOperation, operationID)
	}
	for index := 1; index < len(blocked); index++ {
		if blocked[index-1] == blocked[index] {
			return nil, fmt.Errorf("%w: duplicate restore block for operation %q", errInvalidTrashDeleteOperation, operationID)
		}
	}

	return &trashDeleteOperation{
		Planned:        planned,
		Removed:        removed,
		RestoreBlocked: blocked,
		Committed:      operation.Committed,
		Completed:      operation.Completed,
	}, nil
}

type trashRestoreTransition struct {
	original  *Favorite
	relocated *Favorite
}

func normalizeTrashRestorePlans(original, relocated []*Favorite) ([]*Favorite, []*Favorite, error) {
	if len(original) != len(relocated) {
		return nil, nil, fmt.Errorf("%w: restore plans have different lengths", errInvalidTrashRestoreOperation)
	}
	transitions := make([]trashRestoreTransition, 0, len(original))
	seenOriginal := make(map[favoritePathIdentity]struct{}, len(original))
	seenRelocated := make(map[favoritePathIdentity]struct{}, len(relocated))
	for index := range original {
		normalizedOriginal, err := validateTrashDeleteFavorite(original[index])
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid original favorite at index %d", errInvalidTrashRestoreOperation, index)
		}
		normalizedRelocated, err := validateTrashDeleteFavorite(relocated[index])
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid relocated favorite at index %d", errInvalidTrashRestoreOperation, index)
		}
		if normalizedOriginal.UserID != normalizedRelocated.UserID ||
			!normalizedOriginal.CreatedAt.Equal(normalizedRelocated.CreatedAt) ||
			normalizedOriginal.Note != normalizedRelocated.Note {
			return nil, nil, fmt.Errorf("%w: relocated favorite at index %d changes identity or metadata", errInvalidTrashRestoreOperation, index)
		}
		originalIdentity := favoritePathIdentity{UserID: normalizedOriginal.UserID, Path: normalizedOriginal.Path}
		if _, exists := seenOriginal[originalIdentity]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate original favorite path", errInvalidTrashRestoreOperation)
		}
		seenOriginal[originalIdentity] = struct{}{}
		relocatedIdentity := favoritePathIdentity{UserID: normalizedRelocated.UserID, Path: normalizedRelocated.Path}
		if _, exists := seenRelocated[relocatedIdentity]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate relocated favorite path", errInvalidTrashRestoreOperation)
		}
		seenRelocated[relocatedIdentity] = struct{}{}
		transitions = append(transitions, trashRestoreTransition{original: normalizedOriginal, relocated: normalizedRelocated})
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].original.UserID != transitions[j].original.UserID {
			return transitions[i].original.UserID < transitions[j].original.UserID
		}
		return transitions[i].original.Path < transitions[j].original.Path
	})
	normalizedOriginal := make([]*Favorite, 0, len(transitions))
	normalizedRelocated := make([]*Favorite, 0, len(transitions))
	for _, transition := range transitions {
		normalizedOriginal = append(normalizedOriginal, transition.original)
		normalizedRelocated = append(normalizedRelocated, transition.relocated)
	}
	return normalizedOriginal, normalizedRelocated, nil
}

func normalizeTrashRestoreOperation(
	operationID string,
	operation *trashRestoreOperation,
	requireCanonical bool,
) (*trashRestoreOperation, error) {
	if !validTrashDeleteOperationID(operationID) || operation == nil || !validTrashDeleteOperationID(operation.DeleteOperationID) || operation.Original == nil || operation.Relocated == nil || operation.Restored == nil {
		return nil, fmt.Errorf("%w: malformed operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	original, relocated, err := normalizeTrashRestorePlans(operation.Original, operation.Relocated)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid restore plans for operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	restored, err := normalizeTrashDeleteFavorites(operation.Restored)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid restored favorites for operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	if requireCanonical && (!favoriteSlicesEqual(original, operation.Original) || !favoriteSlicesEqual(relocated, operation.Relocated) || !favoriteSlicesEqual(restored, operation.Restored)) {
		return nil, fmt.Errorf("%w: non-canonical operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	return &trashRestoreOperation{
		DeleteOperationID: operation.DeleteOperationID,
		Original:          original,
		Relocated:         relocated,
		Restored:          restored,
	}, nil
}

func validateTrashRestoreOwnership(
	operationID string,
	operation *trashRestoreOperation,
	deleteOperations map[string]*trashDeleteOperation,
) error {
	deleteOperation := deleteOperations[operation.DeleteOperationID]
	if deleteOperation == nil || !deleteOperation.Completed {
		return fmt.Errorf("%w: restore operation %q does not reference completed delete ownership", errInvalidTrashRestoreOperation, operationID)
	}
	if !favoriteSlicesEqual(operation.Original, deleteOperation.Planned) {
		return fmt.Errorf("%w: restore operation %q has a different delete plan", errInvalidTrashRestoreOperation, operationID)
	}
	for _, favorite := range operation.Restored {
		owned := false
		for index, original := range operation.Original {
			for _, removed := range deleteOperation.Removed {
				if !favoriteDeleteIdentityEqual(removed, original) {
					continue
				}
				expected := copyFavorite(removed)
				expected.Path = operation.Relocated[index].Path
				if favoriteFullEqual(favorite, expected) {
					owned = true
				}
				break
			}
			if owned {
				break
			}
		}
		if !owned {
			return fmt.Errorf("%w: restore operation %q contains an unowned favorite", errInvalidTrashRestoreOperation, operationID)
		}
	}
	return nil
}

func normalizeLoadedTrashRestoreOperations(
	operations map[string]*trashRestoreOperation,
	deleteOperations map[string]*trashDeleteOperation,
) (map[string]*trashRestoreOperation, error) {
	normalized := make(map[string]*trashRestoreOperation, len(operations))
	deleteOwners := make(map[string]string, len(operations))
	for operationID, operation := range operations {
		normalizedOperation, err := normalizeTrashRestoreOperation(operationID, operation, true)
		if err != nil {
			return nil, err
		}
		if err := validateTrashRestoreOwnership(operationID, normalizedOperation, deleteOperations); err != nil {
			return nil, err
		}
		if owner, exists := deleteOwners[normalizedOperation.DeleteOperationID]; exists {
			return nil, fmt.Errorf("%w: restore operations %q and %q reference the same delete ownership", errInvalidTrashRestoreOperation, owner, operationID)
		}
		deleteOwners[normalizedOperation.DeleteOperationID] = operationID
		normalized[operationID] = normalizedOperation
	}
	return normalized, nil
}

func blockTrashDeleteRestore(
	operations map[string]*trashDeleteOperation,
	favorite *Favorite,
	excludedOperationID string,
) {
	if favorite == nil {
		return
	}
	identity := favoritePathIdentity{UserID: favorite.UserID, Path: favorite.Path}
	for operationID, operation := range operations {
		if operationID == excludedOperationID || operation == nil || !favoritePathInRemoved(identity, operation.Removed) || favoritePathIdentityInSlice(identity, operation.RestoreBlocked) {
			continue
		}
		operation.RestoreBlocked = append(operation.RestoreBlocked, identity)
		sortFavoritePathIdentities(operation.RestoreBlocked)
	}
}

func (s *Store) snapshotState() favoritesSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return favoritesSnapshot{
		data:                   cloneFavoritesData(s.data),
		trashDeleteOperations:  cloneTrashDeleteOperations(s.trashDeleteOperations),
		trashRestoreOperations: cloneTrashRestoreOperations(s.trashRestoreOperations),
		filePath:               s.filePath,
		version:                s.version,
	}
}

func (s *Store) commitSnapshot(snapshot favoritesSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != snapshot.version {
		return false
	}

	s.data = snapshot.data
	s.trashDeleteOperations = snapshot.trashDeleteOperations
	s.trashRestoreOperations = snapshot.trashRestoreOperations
	s.version++
	return true
}

func (s *Store) persistSnapshot(snapshot favoritesSnapshot) (bool, error) {
	err := saveFavoritesState(
		snapshot.filePath,
		snapshot.data,
		snapshot.trashDeleteOperations,
		snapshot.trashRestoreOperations,
	)
	if err != nil && !IsPersistenceWarning(err) {
		return false, err
	}
	if !s.commitSnapshot(snapshot) {
		return false, nil
	}
	return true, err
}

func validateFavoritesStorePath(path string) error {
	cleaned, err := normalizeFavoritesStorePath(path)
	if err != nil {
		return err
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat favorites store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errFavoritesStoreSymlink
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat favorites store: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errFavoritesStoreSymlink
		}
	}
	return nil
}

func normalizeFavoritesStorePath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve favorites store path: %w", err)
	}
	return absPath, nil
}

func ensureFavoritesStoreDirRoot(path string, create bool) (string, error) {
	normalizedPath, _, _, err := ensureFavoritesStoreDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureFavoritesStoreDirRootWithState(path string, create bool) (string, *os.Root, []string, error) {
	normalizedPath, err := normalizeFavoritesStorePath(path)
	if err != nil {
		return "", nil, nil, err
	}
	dir := filepath.Dir(normalizedPath)

	favoritesStoreDirRootsMu.RLock()
	root := favoritesStoreDirRoots[dir]
	favoritesStoreDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil, nil
	}

	if err := validateFavoritesStorePath(normalizedPath); err != nil {
		return "", nil, nil, err
	}

	createdDirs := []string(nil)
	if create {
		var err error
		createdDirs, err = ensureFavoritesDir(dir, 0755)
		if err != nil {
			return "", nil, createdDirs, fmt.Errorf("failed to create directory: %w", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil, nil
		}
		return "", nil, nil, fmt.Errorf("failed to stat favorites store directory: %w", err)
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, createdDirs, fmt.Errorf("failed to open favorites store directory root: %w", err)
	}

	favoritesStoreDirRootsMu.Lock()
	if existing := favoritesStoreDirRoots[dir]; existing != nil {
		favoritesStoreDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, createdDirs, nil
	}
	favoritesStoreDirRoots[dir] = root
	favoritesStoreDirRootsMu.Unlock()

	return normalizedPath, root, createdDirs, nil
}

func releaseRegisteredFavoritesStoreDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	favoritesStoreDirRootsMu.Lock()
	if favoritesStoreDirRoots[dir] == root {
		delete(favoritesStoreDirRoots, dir)
	}
	favoritesStoreDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredFavoritesStoreDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeFavoritesStorePath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	favoritesStoreDirRootsMu.RLock()
	root := favoritesStoreDirRoots[dir]
	favoritesStoreDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func readRegisteredFavoritesStoreFile(path string) ([]byte, error) {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		normalizedPath, _, _, err = ensureFavoritesStoreDirRootWithState(normalizedPath, false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredFavoritesStoreDirRoot(normalizedPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			if err := validateFavoritesStorePath(normalizedPath); err != nil {
				return nil, err
			}
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
	}
	return readFavoritesStoreFileWithRoot(root, normalizedPath)
}

func writeRegisteredFavoritesStoreFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		normalizedPath, _, _, err = ensureFavoritesStoreDirRootWithState(normalizedPath, true)
		if err != nil {
			return err
		}
		return writeRegisteredFavoritesStoreFileAtomically(normalizedPath, data)
	}
	return writeFavoritesStoreFileAtomicallyWithRoot(root, normalizedPath, data)
}

func renameRegisteredFavoritesStoreFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredFavoritesStoreDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeFavoritesStorePath(newPath)
	if err != nil {
		return err
	}
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("favorites rename requires same parent directory")
	}
	if ok {
		afterValidateFavoritesStorePath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapFavoritesRootPathError(err)
		}
		return nil
	}
	if err := validateFavoritesStorePath(normalizedOldPath); err != nil {
		return err
	}
	if err := validateFavoritesStorePath(normalizedNewPath); err != nil {
		return err
	}
	normalizedOldPath, _, _, err = ensureFavoritesStoreDirRootWithState(normalizedOldPath, false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredFavoritesStoreDirRoot(normalizedOldPath)
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateFavoritesStorePath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapFavoritesRootPathError(err)
	}
	return nil
}

func syncRegisteredFavoritesStoreDir(path string) error {
	root, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncFavoritesStoreRootDir(root)
	}
	return syncFavoritesStoreDir(filepath.Dir(normalizedPath))
}

func readFavoritesStoreFileWithRoot(root *os.Root, path string) ([]byte, error) {
	afterValidateFavoritesStorePath()

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapFavoritesRootPathError(err)
	}
	defer file.Close()

	return io.ReadAll(file)
}

func writeFavoritesStoreFileAtomicallyWithRoot(root *os.Root, path string, data []byte) error {
	afterValidateFavoritesStorePath()

	tmpFile, tmpName, err := createFavoritesTempFile(root, ".favorites-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp favorites file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to set temp favorites permissions: %w", err))
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to write favorites file: %w", err))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to sync favorites file: %w", err))
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to close temp favorites file: %w", err))
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupFavoritesTempPath(root, tmpName, fmt.Errorf("failed to replace favorites file: %w", mapFavoritesRootPathError(err)))
	}
	cleanup = false
	if err := syncRegisteredFavoritesStoreDir(path); err != nil {
		return WrapPersistenceWarning(fmt.Errorf("failed to sync favorites directory: %w", err))
	}

	return nil
}

func newFavoritesTempName(pattern string) (string, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	randomPart := hex.EncodeToString(randomBytes)
	name := strings.Replace(pattern, "*", randomPart, 1)
	if strings.Contains(pattern, "*") {
		return name, nil
	}
	return pattern + randomPart, nil
}

func createFavoritesTempFile(root *os.Root, pattern string) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newFavoritesTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapFavoritesRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique temp favorites file")
}

func cleanupFavoritesTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp favorites file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func cleanupCreatedFavoritesDirs(createdDirs []string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created favorites directory %s: %w", dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncFavoritesRootDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isFavoritesRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), favoritesStoreRootEscapeError)
}

func mapFavoritesRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isFavoritesRootEscapeError(err) {
		return errFavoritesStoreSymlink
	}
	return err
}

func writeFavoritesStoreFile(path string, data []byte) error {
	_, normalizedPath, ok, err := registeredFavoritesStoreDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return writeRegisteredFavoritesStoreFileAtomically(normalizedPath, data)
	}
	if err := validateFavoritesStorePath(normalizedPath); err != nil {
		return err
	}
	registeredRoot := (*os.Root)(nil)
	createdDirs := []string(nil)
	normalizedPath, registeredRoot, createdDirs, err = ensureFavoritesStoreDirRootWithState(normalizedPath, true)
	if err != nil {
		releaseRegisteredFavoritesStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
		return cleanupCreatedFavoritesDirs(createdDirs, err)
	}
	releaseRootOnError := registeredRoot != nil
	if err := writeRegisteredFavoritesStoreFileAtomically(normalizedPath, data); err != nil {
		if releaseRootOnError {
			releaseRegisteredFavoritesStoreDirRoot(filepath.Dir(normalizedPath), registeredRoot)
			return cleanupCreatedFavoritesDirs(createdDirs, err)
		}
		return err
	}
	return nil
}

func syncCreatedFavoritesDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncFavoritesStoreDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync favorites directory tree: %w", err)
		}
	}
	return nil
}

func ensureFavoritesDir(dir string, perm os.FileMode) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, errFavoritesStoreSymlink
		}
		return createdDirs, err
	}
	return createdDirs, syncCreatedFavoritesDirs(createdDirs)
}

func syncFavoritesDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func favoriteUserIdentifiers(userID string, extraUserIDs ...string) []string {
	identifiers := make([]string, 0, 1+len(extraUserIDs))
	appendUnique := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range identifiers {
			if existing == trimmed {
				return
			}
		}
		identifiers = append(identifiers, trimmed)
	}

	appendUnique(userID)
	for _, extraUserID := range extraUserIDs {
		appendUnique(extraUserID)
	}
	return identifiers
}

func findFavoriteOwner(data map[string]map[string]*Favorite, cleanPath string, identifiers []string) (string, *Favorite) {
	for _, identifier := range identifiers {
		userFavs := data[identifier]
		if userFavs == nil {
			continue
		}
		if favorite, ok := userFavs[cleanPath]; ok {
			return identifier, favorite
		}
	}
	return "", nil
}

// Add adds a path to favorites
func (s *Store) Add(userID, path, note string, extraUserIDs ...string) (*Favorite, error) {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return nil, err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	primaryUserID := ""
	if len(identifiers) > 0 {
		primaryUserID = identifiers[0]
	}

	fav := &Favorite{
		Path:      cleanPath,
		UserID:    primaryUserID,
		CreatedAt: time.Now(),
		Note:      note,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		if primaryUserID == "" {
			return nil, ErrAlreadyFavorited
		}
		if _, existing := findFavoriteOwner(snapshot.data, cleanPath, identifiers); existing != nil {
			return nil, ErrAlreadyFavorited
		}
		if snapshot.data[primaryUserID] == nil {
			snapshot.data[primaryUserID] = make(map[string]*Favorite)
		}

		snapshot.data[primaryUserID][cleanPath] = copyFavorite(fav)
		blockTrashDeleteRestore(snapshot.trashDeleteOperations, fav, "")
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return copyFavorite(fav), err
		}
		if err != nil {
			return nil, err
		}
	}
}

// Remove removes a path from favorites
func (s *Store) Remove(userID, path string, extraUserIDs ...string) error {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		ownerID, favorite := findFavoriteOwner(snapshot.data, cleanPath, identifiers)
		if ownerID == "" {
			return ErrFavoriteNotFound
		}

		blockTrashDeleteRestore(snapshot.trashDeleteOperations, favorite, "")
		delete(snapshot.data[ownerID], cleanPath)
		if len(snapshot.data[ownerID]) == 0 {
			delete(snapshot.data, ownerID)
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// List returns all favorites for a user, sorted by creation time (newest first)
func (s *Store) List(userID string, extraUserIDs ...string) []*Favorite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	if len(identifiers) == 0 {
		return []*Favorite{}
	}
	primaryUserID := identifiers[0]

	favoritesByPath := make(map[string]*Favorite)
	for _, identifier := range identifiers {
		for _, fav := range s.data[identifier] {
			if _, exists := favoritesByPath[fav.Path]; exists {
				continue
			}
			cloned := copyFavorite(fav)
			if primaryUserID != "" {
				cloned.UserID = primaryUserID
			}
			favoritesByPath[cloned.Path] = cloned
		}
	}

	favorites := make([]*Favorite, 0, len(favoritesByPath))
	for _, fav := range favoritesByPath {
		favorites = append(favorites, fav)
	}

	// Sort by creation time, newest first
	sort.Slice(favorites, func(i, j int) bool {
		if favorites[i].CreatedAt.Equal(favorites[j].CreatedAt) {
			return favorites[i].Path < favorites[j].Path
		}
		return favorites[i].CreatedAt.After(favorites[j].CreatedAt)
	})

	return favorites
}

// IsFavorite checks if a path is favorited by a user
func (s *Store) IsFavorite(userID, path string, extraUserIDs ...string) bool {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return false
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.mu.RLock()
	defer s.mu.RUnlock()

	ownerID, _ := findFavoriteOwner(s.data, cleanPath, identifiers)
	return ownerID != ""
}

// CheckPaths checks which paths are favorited from a list
func (s *Store) CheckPaths(userID string, paths []string, extraUserIDs ...string) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	result := make(map[string]bool, len(paths))

	for _, rawPath := range paths {
		cleanPath, err := normalizeStoredFavoritePath(rawPath)
		if err != nil {
			result[rawPath] = false
			continue
		}
		ownerID, _ := findFavoriteOwner(s.data, cleanPath, identifiers)
		result[rawPath] = ownerID != ""
	}

	return result
}

// UpdateNote updates the note for a favorite
func (s *Store) UpdateNote(userID, path, note string, extraUserIDs ...string) error {
	cleanPath, err := normalizeStoredFavoritePath(path)
	if err != nil {
		return err
	}
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		_, fav := findFavoriteOwner(snapshot.data, cleanPath, identifiers)
		if fav == nil {
			return ErrFavoriteNotFound
		}

		blockTrashDeleteRestore(snapshot.trashDeleteOperations, fav, "")
		fav.Note = note
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// Count returns the number of favorites for a user
func (s *Store) Count(userID string, extraUserIDs ...string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	identifiers := favoriteUserIdentifiers(userID, extraUserIDs...)
	if len(identifiers) == 0 {
		return 0
	}

	seenPaths := make(map[string]struct{})
	for _, identifier := range identifiers {
		for path := range s.data[identifier] {
			seenPaths[path] = struct{}{}
		}
	}
	return len(seenPaths)
}

func favoritePathMatchesOrDescendant(basePath, candidatePath string) bool {
	basePath = path.Clean(basePath)
	candidatePath = path.Clean(candidatePath)
	if basePath == "/" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return candidatePath == basePath || strings.HasPrefix(candidatePath, basePath+"/")
}

func relocateFavoritePath(currentPath, oldRoot, newRoot string) (string, bool) {
	currentPath = path.Clean(currentPath)
	oldRoot = path.Clean(oldRoot)
	newRoot = path.Clean(newRoot)
	if !favoritePathMatchesOrDescendant(oldRoot, currentPath) {
		return "", false
	}
	if currentPath == oldRoot {
		return newRoot, true
	}
	return path.Clean(newRoot + strings.TrimPrefix(currentPath, oldRoot)), true
}

// UpdatePathReferences rewrites favorite paths when a filesystem path is renamed.
func (s *Store) UpdatePathReferences(oldPath, newPath string) error {
	var err error
	oldPath, err = normalizeStoredFavoritePath(oldPath)
	if err != nil {
		return err
	}
	newPath, err = normalizeStoredFavoritePath(newPath)
	if err != nil {
		return err
	}
	if oldPath == newPath {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		type pendingFavoriteRewrite struct {
			userID      string
			currentPath string
			updated     *Favorite
		}
		pendingRewrites := make([]pendingFavoriteRewrite, 0)

		for userID, userFavs := range snapshot.data {
			for currentPath, fav := range userFavs {
				updatedPath, ok := relocateFavoritePath(currentPath, oldPath, newPath)
				if !ok || updatedPath == currentPath {
					continue
				}

				updated := copyFavorite(fav)
				updated.Path = updatedPath
				blockTrashDeleteRestore(snapshot.trashDeleteOperations, fav, "")
				blockTrashDeleteRestore(snapshot.trashDeleteOperations, updated, "")
				pendingRewrites = append(pendingRewrites, pendingFavoriteRewrite{
					userID:      userID,
					currentPath: currentPath,
					updated:     updated,
				})
				changed = true
			}
		}

		for _, rewrite := range pendingRewrites {
			delete(snapshot.data[rewrite.userID], rewrite.currentPath)
			snapshot.data[rewrite.userID][rewrite.updated.Path] = rewrite.updated
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// RemoveFavoritesUnderPath removes favorites that reference a deleted path.
func (s *Store) RemoveFavoritesUnderPath(targetPath string) error {
	_, err := s.RemoveFavoritesUnderPathWithRestore(targetPath)
	return err
}

// SnapshotDeleteExact returns detached copies of the favorites that a delete
// operation currently owns under targetPath.
func (s *Store) SnapshotDeleteExact(targetPath string) ([]*Favorite, error) {
	normalizedPath, err := normalizeStoredFavoritePath(targetPath)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	favorites := make([]*Favorite, 0)
	for _, userFavorites := range s.data {
		for currentPath, favorite := range userFavorites {
			if favorite == nil || !favoritePathMatchesOrDescendant(normalizedPath, currentPath) {
				continue
			}
			favorites = append(favorites, copyFavorite(favorite))
		}
	}
	sortFavoritesCanonical(favorites)
	return favorites, nil
}

// ApplyDeleteExact removes only favorites that still have the identity stored
// in the supplied snapshot.
func (s *Store) ApplyDeleteExact(favorites []*Favorite) error {
	if len(favorites) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, favorite := range favorites {
			if favorite == nil {
				continue
			}
			normalized, err := normalizeRestoredFavorite(favorite)
			if err != nil {
				return err
			}

			userFavorites := snapshot.data[normalized.UserID]
			current, ok := userFavorites[normalized.Path]
			if !ok || !favoriteDeleteIdentityEqual(current, normalized) {
				continue
			}

			blockTrashDeleteRestore(snapshot.trashDeleteOperations, current, "")
			delete(userFavorites, normalized.Path)
			if len(userFavorites) == 0 {
				delete(snapshot.data, normalized.UserID)
			}
			changed = true
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

func favoriteDeleteIdentityEqual(current, snapshot *Favorite) bool {
	if current == nil || snapshot == nil {
		return current == snapshot
	}
	return current.UserID == snapshot.UserID &&
		current.Path == snapshot.Path &&
		current.CreatedAt.Equal(snapshot.CreatedAt)
}

func removeTrashDeletePlanExact(
	data map[string]map[string]*Favorite,
	planned []*Favorite,
	operations map[string]*trashDeleteOperation,
	excludedOperationID string,
) []*Favorite {
	removed := make([]*Favorite, 0, len(planned))
	for _, favorite := range planned {
		userFavorites := data[favorite.UserID]
		current, ok := userFavorites[favorite.Path]
		if !ok || !favoriteDeleteIdentityEqual(current, favorite) {
			continue
		}
		removed = append(removed, copyFavorite(current))
		blockTrashDeleteRestore(operations, current, excludedOperationID)
		delete(userFavorites, favorite.Path)
		if len(userFavorites) == 0 {
			delete(data, favorite.UserID)
		}
	}
	sortFavoritesCanonical(removed)
	return removed
}

// ApplyTrashDeleteOperation durably applies an exact favorite deletion. A
// precommit application retains the exact removed objects for rollback. A
// committed application records a durable replay receipt with the deletion.
func (s *Store) ApplyTrashDeleteOperation(operationID string, planned []*Favorite, committed bool) error {
	if !validTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}
	normalizedPlanned, err := normalizeTrashDeleteFavorites(planned)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		existing := snapshot.trashDeleteOperations[operationID]
		if existing != nil && !favoriteSlicesEqual(existing.Planned, normalizedPlanned) {
			return fmt.Errorf("%w: operation %q has a different plan", ErrTrashDeleteOperationConflict, operationID)
		}
		if !committed && existing != nil {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}
		if committed && existing != nil && existing.Committed {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}

		removed := removeTrashDeletePlanExact(
			snapshot.data,
			normalizedPlanned,
			snapshot.trashDeleteOperations,
			operationID,
		)
		if committed {
			if existing == nil {
				existing = &trashDeleteOperation{
					Planned:        cloneFavoriteSlice(normalizedPlanned),
					Removed:        removed,
					RestoreBlocked: []favoritePathIdentity{},
				}
			}
			existing.Committed = true
			snapshot.trashDeleteOperations[operationID] = existing
		} else {
			snapshot.trashDeleteOperations[operationID] = &trashDeleteOperation{
				Planned:        cloneFavoriteSlice(normalizedPlanned),
				Removed:        removed,
				RestoreBlocked: []favoritePathIdentity{},
			}
		}

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// CompleteTrashDeleteOperation marks committed delete ownership complete.
// Missing receipts require a durability barrier; pending markers cannot complete.
func (s *Store) CompleteTrashDeleteOperation(operationID string) error {
	if !validTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		operation := snapshot.trashDeleteOperations[operationID]
		if operation == nil {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}
		if !operation.Committed {
			return fmt.Errorf("%w: operation %q is not committed", ErrTrashDeleteOperationConflict, operationID)
		}
		operation.Completed = true

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// PurgeCompletedTrashDeleteOperation removes one explicitly selected completed
// delete ownership marker. Missing markers require a durability barrier.
func (s *Store) PurgeCompletedTrashDeleteOperation(operationID string) error {
	if !validTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		operation := snapshot.trashDeleteOperations[operationID]
		if operation == nil {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}
		if !operation.Completed {
			return fmt.Errorf("%w: operation %q is not completed", ErrTrashDeleteOperationConflict, operationID)
		}
		for restoreOperationID, restoreOperation := range snapshot.trashRestoreOperations {
			if restoreOperation != nil && restoreOperation.DeleteOperationID == operationID {
				return fmt.Errorf("%w: completed operation %q is owned by restore operation %q", ErrTrashDeleteOperationConflict, operationID, restoreOperationID)
			}
		}
		delete(snapshot.trashDeleteOperations, operationID)

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// PruneCompletedTrashDeleteOperations removes completed ownership that is not
// listed as active. Pending and committed operations are never inferred stale.
func (s *Store) PruneCompletedTrashDeleteOperations(activeDeleteOperationIDs []string) error {
	active := make(map[string]struct{}, len(activeDeleteOperationIDs))
	for _, operationID := range activeDeleteOperationIDs {
		if !validTrashDeleteOperationID(operationID) {
			return fmt.Errorf("%w: invalid active operation ID", errInvalidTrashDeleteOperation)
		}
		active[operationID] = struct{}{}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		for operationID, operation := range snapshot.trashDeleteOperations {
			if operation == nil || !operation.Completed {
				continue
			}
			if _, exists := active[operationID]; exists {
				continue
			}
			for restoreOperationID, restoreOperation := range snapshot.trashRestoreOperations {
				if restoreOperation != nil && restoreOperation.DeleteOperationID == operationID {
					return fmt.Errorf("%w: completed operation %q is owned by restore operation %q", ErrTrashDeleteOperationConflict, operationID, restoreOperationID)
				}
			}
			delete(snapshot.trashDeleteOperations, operationID)
		}

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// ApplyTrashRestoreOperation restores only favorites owned by one completed
// delete operation and records the exact restored set in a durable receipt.
func (s *Store) ApplyTrashRestoreOperation(
	restoreOperationID string,
	deleteOperationID string,
	original []*Favorite,
	relocated []*Favorite,
) error {
	if !validTrashDeleteOperationID(restoreOperationID) || !validTrashDeleteOperationID(deleteOperationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashRestoreOperation)
	}
	normalizedOriginal, normalizedRelocated, err := normalizeTrashRestorePlans(original, relocated)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		existing, exists := snapshot.trashRestoreOperations[restoreOperationID]
		if exists {
			if existing.DeleteOperationID != deleteOperationID ||
				!favoriteSlicesEqual(existing.Original, normalizedOriginal) ||
				!favoriteSlicesEqual(existing.Relocated, normalizedRelocated) {
				return fmt.Errorf("%w: operation %q has different delete ownership or plan", ErrTrashRestoreOperationConflict, restoreOperationID)
			}
			deleteOperation := snapshot.trashDeleteOperations[deleteOperationID]
			if deleteOperation == nil || !deleteOperation.Completed || !favoriteSlicesEqual(deleteOperation.Planned, normalizedOriginal) {
				return fmt.Errorf("%w: operation %q lost completed delete ownership", ErrTrashRestoreOperationConflict, restoreOperationID)
			}
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}

		deleteOperation := snapshot.trashDeleteOperations[deleteOperationID]
		if deleteOperation == nil || !deleteOperation.Completed || !favoriteSlicesEqual(deleteOperation.Planned, normalizedOriginal) {
			return fmt.Errorf("%w: delete operation %q does not own the restore plan", ErrTrashRestoreOperationConflict, deleteOperationID)
		}
		for otherRestoreOperationID, operation := range snapshot.trashRestoreOperations {
			if otherRestoreOperationID != restoreOperationID && operation != nil && operation.DeleteOperationID == deleteOperationID {
				return fmt.Errorf("%w: delete operation %q already has restore operation %q", ErrTrashRestoreOperationConflict, deleteOperationID, otherRestoreOperationID)
			}
		}

		restored := make([]*Favorite, 0, len(deleteOperation.Removed))
		for index, initial := range normalizedOriginal {
			var removed *Favorite
			for _, candidate := range deleteOperation.Removed {
				if favoriteDeleteIdentityEqual(candidate, initial) {
					removed = candidate
					break
				}
			}
			if removed == nil {
				continue
			}
			identity := favoritePathIdentity{UserID: removed.UserID, Path: removed.Path}
			if favoritePathIdentityInSlice(identity, deleteOperation.RestoreBlocked) {
				continue
			}
			target := normalizedRelocated[index]
			userFavorites := snapshot.data[target.UserID]
			if userFavorites != nil {
				if _, exists := userFavorites[target.Path]; exists {
					continue
				}
			} else {
				userFavorites = make(map[string]*Favorite)
				snapshot.data[target.UserID] = userFavorites
			}

			restoredFavorite := copyFavorite(removed)
			restoredFavorite.Path = target.Path
			userFavorites[target.Path] = restoredFavorite
			restored = append(restored, copyFavorite(restoredFavorite))
		}
		sortFavoritesCanonical(restored)
		snapshot.trashRestoreOperations[restoreOperationID] = &trashRestoreOperation{
			DeleteOperationID: deleteOperationID,
			Original:          cloneFavoriteSlice(normalizedOriginal),
			Relocated:         cloneFavoriteSlice(normalizedRelocated),
			Restored:          restored,
		}

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// CompleteTrashRestoreOperation atomically removes a restore receipt and its
// matching completed delete ownership. Missing receipts require a barrier.
func (s *Store) CompleteTrashRestoreOperation(restoreOperationID string) error {
	if !validTrashDeleteOperationID(restoreOperationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashRestoreOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		restoreOperation, exists := snapshot.trashRestoreOperations[restoreOperationID]
		if !exists {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}
		deleteOperation := snapshot.trashDeleteOperations[restoreOperation.DeleteOperationID]
		if deleteOperation == nil || !deleteOperation.Completed || !favoriteSlicesEqual(deleteOperation.Planned, restoreOperation.Original) {
			return fmt.Errorf("%w: operation %q lost completed delete ownership", ErrTrashRestoreOperationConflict, restoreOperationID)
		}
		delete(snapshot.trashRestoreOperations, restoreOperationID)
		delete(snapshot.trashDeleteOperations, restoreOperation.DeleteOperationID)

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// RollbackTrashDeleteOperation restores only objects removed by the matching
// pre-commit application. Later creations at the same user and path suppress
// restoration, including when that later creation has since been deleted.
func (s *Store) RollbackTrashDeleteOperation(operationID string) error {
	if !validTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		operation := snapshot.trashDeleteOperations[operationID]
		if operation == nil {
			persisted, persistErr := s.persistSnapshot(snapshot)
			if persisted {
				return persistErr
			}
			if persistErr != nil {
				return persistErr
			}
			continue
		}
		if operation.Committed {
			return fmt.Errorf("%w: operation %q is committed", ErrTrashDeleteOperationConflict, operationID)
		}

		for _, favorite := range operation.Removed {
			identity := favoritePathIdentity{UserID: favorite.UserID, Path: favorite.Path}
			if favoritePathIdentityInSlice(identity, operation.RestoreBlocked) {
				continue
			}
			userFavorites := snapshot.data[favorite.UserID]
			if userFavorites != nil {
				if _, exists := userFavorites[favorite.Path]; exists {
					continue
				}
			} else {
				userFavorites = make(map[string]*Favorite)
				snapshot.data[favorite.UserID] = userFavorites
			}
			userFavorites[favorite.Path] = copyFavorite(favorite)
			blockTrashDeleteRestore(snapshot.trashDeleteOperations, favorite, operationID)
		}
		delete(snapshot.trashDeleteOperations, operationID)

		persisted, persistErr := s.persistSnapshot(snapshot)
		if persisted {
			return persistErr
		}
		if persistErr != nil {
			return persistErr
		}
	}
}

// RemoveFavoritesUnderPathWithRestore removes favorites under a deleted path and
// returns the removed favorites for rollback if a later step fails.
func (s *Store) RemoveFavoritesUnderPathWithRestore(targetPath string) ([]*Favorite, error) {
	var err error
	targetPath, err = normalizeStoredFavoritePath(targetPath)
	if err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false
		var removed []*Favorite

		for userID, userFavs := range snapshot.data {
			for currentPath, fav := range userFavs {
				if !favoritePathMatchesOrDescendant(targetPath, currentPath) {
					continue
				}

				removed = append(removed, copyFavorite(fav))
				blockTrashDeleteRestore(snapshot.trashDeleteOperations, fav, "")
				delete(snapshot.data[userID], currentPath)
				changed = true
			}
			if len(snapshot.data[userID]) == 0 {
				delete(snapshot.data, userID)
			}
		}

		if !changed {
			return nil, nil
		}
		sortFavoritesCanonical(removed)
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return removed, err
		}
		if err != nil {
			return nil, err
		}
	}
}

// RestoreFavorites restores favorites that were removed by a failed operation.
func (s *Store) RestoreFavorites(favorites []*Favorite) error {
	if len(favorites) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, favorite := range favorites {
			normalized, err := normalizeRestoredFavorite(favorite)
			if err != nil {
				return err
			}

			if snapshot.data[normalized.UserID] == nil {
				snapshot.data[normalized.UserID] = make(map[string]*Favorite)
			}

			current, ok := snapshot.data[normalized.UserID][normalized.Path]
			if ok && current.Note == normalized.Note && current.CreatedAt.Equal(normalized.CreatedAt) {
				continue
			}

			snapshot.data[normalized.UserID][normalized.Path] = normalized
			blockTrashDeleteRestore(snapshot.trashDeleteOperations, normalized, "")
			changed = true
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// RestoreFavoritesIfMissing restores removed favorites only when the same path
// has not been recreated during rollback.
func (s *Store) RestoreFavoritesIfMissing(favorites []*Favorite) error {
	if len(favorites) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		changed := false

		for _, favorite := range favorites {
			normalized, err := normalizeRestoredFavorite(favorite)
			if err != nil {
				return err
			}

			userFavs := snapshot.data[normalized.UserID]
			if userFavs != nil {
				if _, ok := userFavs[normalized.Path]; ok {
					continue
				}
			} else {
				userFavs = make(map[string]*Favorite)
				snapshot.data[normalized.UserID] = userFavs
			}

			userFavs[normalized.Path] = normalized
			blockTrashDeleteRestore(snapshot.trashDeleteOperations, normalized, "")
			changed = true
		}

		if !changed {
			return nil
		}
		committed, err := s.persistSnapshot(snapshot)
		if committed {
			return err
		}
		if err != nil {
			return err
		}
	}
}
