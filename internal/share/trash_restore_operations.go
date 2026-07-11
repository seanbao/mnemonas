package share

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrTrashRestoreOperationConflict reports reuse of an operation ID with a different restore plan.
	ErrTrashRestoreOperationConflict = errors.New("trash restore operation conflict")
	errInvalidTrashRestoreOperation  = errors.New("invalid trash restore operation")
)

// ApplyTrashRestoreOperation restores only shares owned by one completed
// delete operation and records the exact restored set in a durable receipt.
func (s *ShareStore) ApplyTrashRestoreOperation(
	restoreOperationID string,
	deleteOperationID string,
	original []*Share,
	relocated []*Share,
) error {
	if !isValidTrashDeleteOperationID(restoreOperationID) || !isValidTrashDeleteOperationID(deleteOperationID) {
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
				!shareSlicesEqual(existing.Original, normalizedOriginal) ||
				!shareSlicesEqual(existing.Relocated, normalizedRelocated) {
				return fmt.Errorf(
					"%w: operation ID %q has different delete ownership or plan",
					ErrTrashRestoreOperationConflict,
					restoreOperationID,
				)
			}
			deleteOperation := snapshot.trashDeleteOperations[deleteOperationID]
			if deleteOperation == nil || !deleteOperation.Completed ||
				!shareSlicesEqual(deleteOperation.Planned, normalizedOriginal) {
				return fmt.Errorf(
					"%w: operation ID %q lost completed delete ownership",
					ErrTrashRestoreOperationConflict,
					restoreOperationID,
				)
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
		if deleteOperation == nil || !deleteOperation.Completed ||
			!shareSlicesEqual(deleteOperation.Planned, normalizedOriginal) {
			return fmt.Errorf(
				"%w: delete operation ID %q does not own the restore plan",
				ErrTrashRestoreOperationConflict,
				deleteOperationID,
			)
		}
		for otherRestoreOperationID, operation := range snapshot.trashRestoreOperations {
			if otherRestoreOperationID != restoreOperationID && operation != nil &&
				operation.DeleteOperationID == deleteOperationID {
				return fmt.Errorf(
					"%w: delete operation ID %q already has restore operation %q",
					ErrTrashRestoreOperationConflict,
					deleteOperationID,
					otherRestoreOperationID,
				)
			}
		}

		relocatedByID := indexTrashRestoreShares(normalizedRelocated)
		restored := make([]*Share, 0, len(deleteOperation.Changed))
		for _, changed := range deleteOperation.Changed {
			if trashDeleteRestoreBlocked(deleteOperation, changed.ID) {
				continue
			}
			current, exists := snapshot.shares[changed.ID]
			if !exists || current.Enabled || current.Path != changed.Path ||
				!sameTrashDeleteShareGeneration(current, changed) {
				continue
			}
			target := relocatedByID[changed.ID]
			if target == nil || !sameTrashDeleteShareGeneration(target, changed) {
				continue
			}

			updated := copyShare(current)
			if updated.Path != target.Path {
				moveSharePathIndex(snapshot.pathIdx, updated.Path, target.Path, updated.ID)
				updated.Path = target.Path
			}
			updated.Enabled = target.Enabled
			if err := bumpDownloadTicketRevision(&snapshot, updated); err != nil {
				return err
			}
			snapshot.shares[updated.ID] = updated
			restored = append(restored, copyShare(updated))
		}
		sortSharesCanonical(restored)
		snapshot.trashRestoreOperations[restoreOperationID] = &shareTrashRestoreOperation{
			DeleteOperationID: deleteOperationID,
			Original:          cloneShareSlice(normalizedOriginal),
			Relocated:         cloneShareSlice(normalizedRelocated),
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
func (s *ShareStore) CompleteTrashRestoreOperation(restoreOperationID string) error {
	if !isValidTrashDeleteOperationID(restoreOperationID) {
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
		if deleteOperation == nil || !deleteOperation.Completed ||
			!shareSlicesEqual(deleteOperation.Planned, restoreOperation.Original) {
			return fmt.Errorf(
				"%w: operation ID %q lost completed delete ownership",
				ErrTrashRestoreOperationConflict,
				restoreOperationID,
			)
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

type shareTrashRestoreTransition struct {
	original  *Share
	relocated *Share
}

func normalizeTrashRestorePlans(original, relocated []*Share) ([]*Share, []*Share, error) {
	if len(original) != len(relocated) {
		return nil, nil, fmt.Errorf("%w: restore plans have different lengths", errInvalidTrashRestoreOperation)
	}
	transitions := make([]shareTrashRestoreTransition, 0, len(original))
	seenIDs := make(map[string]struct{}, len(original))
	for index := range original {
		initial := copyShare(original[index])
		if initial == nil || validateShareInvariants(initial) != nil || !initial.Enabled {
			return nil, nil, fmt.Errorf("%w: invalid original share at index %d", errInvalidTrashRestoreOperation, index)
		}
		target := copyShare(relocated[index])
		if target == nil || validateShareInvariants(target) != nil || !target.Enabled {
			return nil, nil, fmt.Errorf("%w: invalid relocated share at index %d", errInvalidTrashRestoreOperation, index)
		}
		if _, exists := seenIDs[initial.ID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate share ID %q", errInvalidTrashRestoreOperation, initial.ID)
		}
		seenIDs[initial.ID] = struct{}{}

		expectedTarget := copyShare(initial)
		expectedTarget.Path = target.Path
		if !sharesEqual(expectedTarget, target) {
			return nil, nil, fmt.Errorf(
				"%w: relocated share %q changes identity or metadata",
				errInvalidTrashRestoreOperation,
				initial.ID,
			)
		}
		transitions = append(transitions, shareTrashRestoreTransition{original: initial, relocated: target})
	}
	sort.Slice(transitions, func(i, j int) bool {
		left := transitions[i].original
		right := transitions[j].original
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
	})
	normalizedOriginal := make([]*Share, 0, len(transitions))
	normalizedRelocated := make([]*Share, 0, len(transitions))
	for _, transition := range transitions {
		normalizedOriginal = append(normalizedOriginal, transition.original)
		normalizedRelocated = append(normalizedRelocated, transition.relocated)
	}
	return normalizedOriginal, normalizedRelocated, nil
}

func normalizeTrashRestoreOperation(
	operationID string,
	operation *shareTrashRestoreOperation,
	requireCanonical bool,
) (*shareTrashRestoreOperation, error) {
	if !isValidTrashDeleteOperationID(operationID) || operation == nil ||
		!isValidTrashDeleteOperationID(operation.DeleteOperationID) ||
		operation.Original == nil || operation.Relocated == nil || operation.Restored == nil {
		return nil, fmt.Errorf("%w: malformed operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	original, relocated, err := normalizeTrashRestorePlans(operation.Original, operation.Relocated)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid restore plans for operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	restored, err := normalizeTrashRestoreShares(operation.Restored, "restored")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid restored shares for operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	if requireCanonical && (!shareSlicesEqual(original, operation.Original) ||
		!shareSlicesEqual(relocated, operation.Relocated) ||
		!shareSlicesEqual(restored, operation.Restored)) {
		return nil, fmt.Errorf("%w: non-canonical operation %q", errInvalidTrashRestoreOperation, operationID)
	}
	return &shareTrashRestoreOperation{
		DeleteOperationID: operation.DeleteOperationID,
		Original:          original,
		Relocated:         relocated,
		Restored:          restored,
	}, nil
}

func validateTrashRestoreOwnership(
	operationID string,
	operation *shareTrashRestoreOperation,
	deleteOperations map[string]*shareTrashDeleteOperation,
) error {
	deleteOperation := deleteOperations[operation.DeleteOperationID]
	if deleteOperation == nil || !deleteOperation.Completed {
		return fmt.Errorf(
			"%w: restore operation %q does not reference completed delete ownership",
			errInvalidTrashRestoreOperation,
			operationID,
		)
	}
	if !shareSlicesEqual(operation.Original, deleteOperation.Planned) {
		return fmt.Errorf("%w: restore operation %q has a different delete plan", errInvalidTrashRestoreOperation, operationID)
	}
	relocatedByID := indexTrashRestoreShares(operation.Relocated)
	changedByID := indexTrashRestoreShares(deleteOperation.Changed)
	for _, restored := range operation.Restored {
		changed := changedByID[restored.ID]
		target := relocatedByID[restored.ID]
		if changed == nil || target == nil || !sameTrashDeleteShareGeneration(restored, changed) ||
			restored.Path != target.Path || restored.Enabled != target.Enabled {
			return fmt.Errorf(
				"%w: restore operation %q contains an unowned share",
				errInvalidTrashRestoreOperation,
				operationID,
			)
		}
	}
	return nil
}

func normalizeTrashRestoreShares(shares []*Share, label string) ([]*Share, error) {
	normalized := make([]*Share, len(shares))
	seen := make(map[string]struct{}, len(shares))
	for index, share := range shares {
		if share == nil {
			return nil, fmt.Errorf("%w: null %s share at index %d", errInvalidTrashRestoreOperation, label, index)
		}
		cloned := copyShare(share)
		if err := validateShareInvariants(cloned); err != nil {
			return nil, fmt.Errorf("%w: %s share %d: %v", errInvalidTrashRestoreOperation, label, index, err)
		}
		if _, exists := seen[cloned.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate %s share ID %q", errInvalidTrashRestoreOperation, label, cloned.ID)
		}
		seen[cloned.ID] = struct{}{}
		normalized[index] = cloned
	}
	sortSharesCanonical(normalized)
	return normalized, nil
}

func normalizeLoadedTrashRestoreOperations(
	operations map[string]*shareTrashRestoreOperation,
	deleteOperations map[string]*shareTrashDeleteOperation,
) (map[string]*shareTrashRestoreOperation, error) {
	normalized := make(map[string]*shareTrashRestoreOperation, len(operations))
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
			return nil, fmt.Errorf(
				"%w: restore operations %q and %q reference the same delete ownership",
				errInvalidTrashRestoreOperation,
				owner,
				operationID,
			)
		}
		deleteOwners[normalizedOperation.DeleteOperationID] = operationID
		normalized[operationID] = normalizedOperation
	}
	return normalized, nil
}

func cloneTrashRestoreOperations(
	operations map[string]*shareTrashRestoreOperation,
) map[string]*shareTrashRestoreOperation {
	cloned := make(map[string]*shareTrashRestoreOperation, len(operations))
	for operationID, operation := range operations {
		cloned[operationID] = cloneTrashRestoreOperation(operation)
	}
	return cloned
}

func cloneTrashRestoreOperationsCanonical(
	operations map[string]*shareTrashRestoreOperation,
) (map[string]*shareTrashRestoreOperation, error) {
	if operations == nil {
		return nil, errors.New("trash restore operations map is nil")
	}
	cloned := make(map[string]*shareTrashRestoreOperation, len(operations))
	for operationID, operation := range operations {
		normalized, err := normalizeTrashRestoreOperation(operationID, operation, false)
		if err != nil {
			return nil, fmt.Errorf("invalid trash restore operation %q: %w", operationID, err)
		}
		cloned[operationID] = normalized
	}
	return cloned, nil
}

func cloneTrashRestoreOperation(operation *shareTrashRestoreOperation) *shareTrashRestoreOperation {
	if operation == nil {
		return nil
	}
	return &shareTrashRestoreOperation{
		DeleteOperationID: operation.DeleteOperationID,
		Original:          cloneShareSlice(operation.Original),
		Relocated:         cloneShareSlice(operation.Relocated),
		Restored:          cloneShareSlice(operation.Restored),
	}
}

func shareTrashRestoreOperationsEqual(left, right *shareTrashRestoreOperation) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.DeleteOperationID == right.DeleteOperationID &&
		shareSlicesEqual(left.Original, right.Original) &&
		shareSlicesEqual(left.Relocated, right.Relocated) &&
		shareSlicesEqual(left.Restored, right.Restored)
}

func indexTrashRestoreShares(shares []*Share) map[string]*Share {
	indexed := make(map[string]*Share, len(shares))
	for _, current := range shares {
		indexed[current.ID] = current
	}
	return indexed
}
