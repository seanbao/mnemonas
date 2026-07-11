package share

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrTrashDeleteOperationConflict reports reuse of an operation ID with a different plan.
	ErrTrashDeleteOperationConflict = errors.New("trash delete operation conflict")
	errInvalidTrashDeleteOperation  = errors.New("invalid trash delete operation")
)

// ApplyTrashDeleteOperation applies or commits one durable Trash delete participant.
// A precommit records the exact shares changed by this operation. A committed
// application makes the desired disabled state and its replay receipt durable.
func (s *ShareStore) ApplyTrashDeleteOperation(operationID string, planned []*Share, committed bool) error {
	if !isValidTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}
	normalizedPlanned, err := normalizeTrashDeleteShares(planned)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		marker, exists := snapshot.trashDeleteOperations[operationID]
		if exists && !shareSlicesEqual(marker.Planned, normalizedPlanned) {
			return fmt.Errorf("%w: operation ID %q has a different plan", ErrTrashDeleteOperationConflict, operationID)
		}

		if !committed {
			if exists {
				persisted, persistErr := s.persistSnapshot(snapshot)
				if persisted {
					return persistErr
				}
				if persistErr != nil {
					return persistErr
				}
				continue
			}
			changed := disableSharesMatchingPlan(snapshot.shares, normalizedPlanned)
			blockTrashDeleteRestores(snapshot.trashDeleteOperations, changed, operationID)
			snapshot.trashDeleteOperations[operationID] = &shareTrashDeleteOperation{
				Planned:        cloneShareSlice(normalizedPlanned),
				Changed:        changed,
				RestoreBlocked: []string{},
			}
		} else {
			if exists && marker.Committed {
				persisted, persistErr := s.persistSnapshot(snapshot)
				if persisted {
					return persistErr
				}
				if persistErr != nil {
					return persistErr
				}
				continue
			}
			changed := disableSharesMatchingPlan(snapshot.shares, normalizedPlanned)
			blockTrashDeleteRestores(snapshot.trashDeleteOperations, changed, operationID)
			if !exists {
				marker = &shareTrashDeleteOperation{
					Planned:        cloneShareSlice(normalizedPlanned),
					Changed:        changed,
					RestoreBlocked: []string{},
				}
			}
			marker.Committed = true
			snapshot.trashDeleteOperations[operationID] = marker
			clearCommittedTrashDeleteOwnership(snapshot, operationID, normalizedPlanned)
		}

		persisted, err := s.persistSnapshot(snapshot)
		if persisted {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// CompleteTrashDeleteOperation marks committed delete ownership complete.
// Missing receipts require a durability barrier; pending markers cannot complete.
func (s *ShareStore) CompleteTrashDeleteOperation(operationID string) error {
	if !isValidTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		marker, exists := snapshot.trashDeleteOperations[operationID]
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
		if !marker.Committed {
			return fmt.Errorf("%w: operation ID %q is not committed", ErrTrashDeleteOperationConflict, operationID)
		}
		marker.Completed = true

		persisted, err := s.persistSnapshot(snapshot)
		if persisted {
			return err
		}
		if err != nil {
			return err
		}
	}
}

// PurgeCompletedTrashDeleteOperation removes one explicitly selected completed
// delete ownership marker. Missing markers require a durability barrier.
func (s *ShareStore) PurgeCompletedTrashDeleteOperation(operationID string) error {
	if !isValidTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		marker, exists := snapshot.trashDeleteOperations[operationID]
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
		if !marker.Completed {
			return fmt.Errorf("%w: operation ID %q is not completed", ErrTrashDeleteOperationConflict, operationID)
		}
		for restoreOperationID, restoreOperation := range snapshot.trashRestoreOperations {
			if restoreOperation != nil && restoreOperation.DeleteOperationID == operationID {
				return fmt.Errorf(
					"%w: completed operation ID %q is owned by restore operation %q",
					ErrTrashDeleteOperationConflict,
					operationID,
					restoreOperationID,
				)
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

// RollbackTrashDeleteOperation restores only shares still owned by the durable
// precommit marker, preserves newer mutable metadata, and removes the marker.
func (s *ShareStore) RollbackTrashDeleteOperation(operationID string) error {
	if !isValidTrashDeleteOperationID(operationID) {
		return fmt.Errorf("%w: invalid operation ID", errInvalidTrashDeleteOperation)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	for {
		snapshot := s.snapshotState()
		marker, exists := snapshot.trashDeleteOperations[operationID]
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
		if marker.Committed {
			return fmt.Errorf("%w: operation ID %q is committed", ErrTrashDeleteOperationConflict, operationID)
		}

		for _, original := range marker.Changed {
			if trashDeleteRestoreBlocked(marker, original.ID) {
				continue
			}
			current, ok := snapshot.shares[original.ID]
			if !ok || current.Enabled || current.Path != original.Path {
				continue
			}
			if !sameTrashDeleteShareGeneration(current, original) {
				continue
			}
			if shareOwnedByAnotherTrashDelete(snapshot.trashDeleteOperations, operationID, current) {
				continue
			}

			updated := copyShare(current)
			updated.Enabled = original.Enabled
			blockTrashDeleteRestore(snapshot.trashDeleteOperations, current, operationID)
			snapshot.shares[updated.ID] = updated
		}
		delete(snapshot.trashDeleteOperations, operationID)

		persisted, err := s.persistSnapshot(snapshot)
		if persisted {
			return err
		}
		if err != nil {
			return err
		}
	}
}

func isValidTrashDeleteOperationID(operationID string) bool {
	if len(operationID) != 32 {
		return false
	}
	for index := 0; index < len(operationID); index++ {
		character := operationID[index]
		if (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func normalizeTrashDeleteShares(shares []*Share) ([]*Share, error) {
	normalized := make([]*Share, len(shares))
	seen := make(map[string]struct{}, len(shares))
	for index, share := range shares {
		if share == nil {
			return nil, fmt.Errorf("%w: null planned share at index %d", errInvalidTrashDeleteOperation, index)
		}
		cloned := copyShare(share)
		if err := validateShareInvariants(cloned); err != nil {
			return nil, fmt.Errorf("%w: planned share %d: %v", errInvalidTrashDeleteOperation, index, err)
		}
		if _, exists := seen[cloned.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate share ID %q", errInvalidTrashDeleteOperation, cloned.ID)
		}
		seen[cloned.ID] = struct{}{}
		normalized[index] = cloned
	}
	sortSharesCanonical(normalized)
	return normalized, nil
}

func normalizeLoadedTrashDeleteOperations(
	operations map[string]*shareTrashDeleteOperation,
) (map[string]*shareTrashDeleteOperation, error) {
	normalized := make(map[string]*shareTrashDeleteOperation, len(operations))
	for operationID, marker := range operations {
		if !isValidTrashDeleteOperationID(operationID) {
			return nil, fmt.Errorf("%w: invalid loaded operation ID %q", errInvalidTrashDeleteOperation, operationID)
		}
		normalizedMarker, err := normalizeTrashDeleteOperationMarker(marker, true)
		if err != nil {
			return nil, fmt.Errorf("%w: operation %q: %v", errInvalidTrashDeleteOperation, operationID, err)
		}
		normalized[operationID] = normalizedMarker
	}
	return normalized, nil
}

func normalizeTrashDeleteOperationMarker(
	marker *shareTrashDeleteOperation,
	requireCanonical bool,
) (*shareTrashDeleteOperation, error) {
	if marker == nil {
		return nil, errors.New("null marker")
	}
	if marker.Planned == nil {
		return nil, errors.New("null planned list")
	}
	if marker.Changed == nil {
		return nil, errors.New("null changed list")
	}
	if marker.RestoreBlocked == nil {
		return nil, errors.New("null restore blocked list")
	}
	if marker.Completed && !marker.Committed {
		return nil, errors.New("completed marker is not committed")
	}

	planned, err := normalizeTrashDeleteShares(marker.Planned)
	if err != nil {
		return nil, err
	}
	changed, err := normalizeTrashDeleteShares(marker.Changed)
	if err != nil {
		return nil, err
	}
	if requireCanonical && (!shareSlicesEqual(marker.Planned, planned) || !shareSlicesEqual(marker.Changed, changed)) {
		return nil, errors.New("marker share lists are not canonical")
	}

	plannedByID := make(map[string]*Share, len(planned))
	for _, share := range planned {
		plannedByID[share.ID] = share
	}
	for _, share := range changed {
		plannedShare, exists := plannedByID[share.ID]
		if !exists || plannedShare.Path != share.Path || !plannedShare.Enabled || !share.Enabled ||
			!sameTrashDeleteShareGeneration(plannedShare, share) {
			return nil, fmt.Errorf("changed share %q is not an enabled member of the plan", share.ID)
		}
	}

	blocked := append([]string{}, marker.RestoreBlocked...)
	sort.Strings(blocked)
	if !stringSlicesEqual(blocked, marker.RestoreBlocked) {
		return nil, errors.New("restore blocked share IDs are not canonical")
	}
	changedByID := indexTrashRestoreShares(changed)
	for index, shareID := range blocked {
		if shareID == "" || changedByID[shareID] == nil {
			return nil, fmt.Errorf("restore blocked share %q is outside the changed set", shareID)
		}
		if index > 0 && blocked[index-1] == shareID {
			return nil, fmt.Errorf("duplicate restore blocked share ID %q", shareID)
		}
	}
	return &shareTrashDeleteOperation{
		Planned:        planned,
		Changed:        changed,
		RestoreBlocked: blocked,
		Committed:      marker.Committed,
		Completed:      marker.Completed,
	}, nil
}

func cloneTrashDeleteOperations(
	operations map[string]*shareTrashDeleteOperation,
) map[string]*shareTrashDeleteOperation {
	cloned := make(map[string]*shareTrashDeleteOperation, len(operations))
	for operationID, marker := range operations {
		if marker == nil {
			cloned[operationID] = nil
			continue
		}
		cloned[operationID] = &shareTrashDeleteOperation{
			Planned:        cloneShareSlice(marker.Planned),
			Changed:        cloneShareSlice(marker.Changed),
			RestoreBlocked: append([]string{}, marker.RestoreBlocked...),
			Committed:      marker.Committed,
			Completed:      marker.Completed,
		}
	}
	return cloned
}

func cloneTrashDeleteOperationsCanonical(
	operations map[string]*shareTrashDeleteOperation,
) (map[string]*shareTrashDeleteOperation, error) {
	if operations == nil {
		return nil, errors.New("trash delete operations map is nil")
	}
	cloned := make(map[string]*shareTrashDeleteOperation, len(operations))
	for operationID, marker := range operations {
		if !isValidTrashDeleteOperationID(operationID) {
			return nil, fmt.Errorf("invalid trash delete operation ID %q", operationID)
		}
		normalized, err := normalizeTrashDeleteOperationMarker(marker, false)
		if err != nil {
			return nil, fmt.Errorf("invalid trash delete operation %q: %w", operationID, err)
		}
		cloned[operationID] = normalized
	}
	return cloned, nil
}

func cloneShareSlice(shares []*Share) []*Share {
	cloned := make([]*Share, len(shares))
	for index, share := range shares {
		cloned[index] = copyShare(share)
	}
	return cloned
}

func shareSlicesEqual(left, right []*Share) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sharesEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(left, right []string) bool {
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

func disableSharesMatchingPlan(shares map[string]*Share, planned []*Share) []*Share {
	changed := make([]*Share, 0, len(planned))
	for _, expected := range planned {
		current, exists := shares[expected.ID]
		if !exists || current.Path != expected.Path || current.Enabled != expected.Enabled || !expected.Enabled {
			continue
		}
		changed = append(changed, copyShare(current))
		updated := copyShare(current)
		updated.Enabled = false
		shares[updated.ID] = updated
	}
	sortSharesCanonical(changed)
	return changed
}

func sameTrashDeleteShareGeneration(current, original *Share) bool {
	if current == nil || original == nil {
		return current == original
	}
	return current.ID == original.ID &&
		current.Type == original.Type &&
		current.CreatedBy == original.CreatedBy &&
		current.CreatedAt.Equal(original.CreatedAt)
}

func trashDeleteRestoreBlocked(marker *shareTrashDeleteOperation, shareID string) bool {
	if marker == nil {
		return false
	}
	for _, blockedID := range marker.RestoreBlocked {
		if blockedID == shareID {
			return true
		}
	}
	return false
}

func blockTrashDeleteRestore(
	operations map[string]*shareTrashDeleteOperation,
	share *Share,
	excludedOperationID string,
) {
	if share == nil {
		return
	}
	for operationID, marker := range operations {
		if operationID == excludedOperationID || marker == nil || trashDeleteRestoreBlocked(marker, share.ID) {
			continue
		}
		matched := false
		for _, changed := range marker.Changed {
			if changed.ID == share.ID && sameTrashDeleteShareGeneration(changed, share) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		marker.RestoreBlocked = append(marker.RestoreBlocked, share.ID)
		sort.Strings(marker.RestoreBlocked)
	}
}

func blockTrashDeleteRestores(
	operations map[string]*shareTrashDeleteOperation,
	shares []*Share,
	excludedOperationID string,
) {
	for _, share := range shares {
		blockTrashDeleteRestore(operations, share, excludedOperationID)
	}
}

func shareOwnedByAnotherTrashDelete(
	operations map[string]*shareTrashDeleteOperation,
	excludedOperationID string,
	current *Share,
) bool {
	for operationID, marker := range operations {
		if operationID == excludedOperationID || marker == nil || marker.Committed {
			continue
		}
		for _, changed := range marker.Changed {
			if changed.ID == current.ID && changed.Path == current.Path && sameTrashDeleteShareGeneration(current, changed) {
				return true
			}
		}
	}
	return false
}

func clearCommittedTrashDeleteOwnership(
	snapshot shareStoreSnapshot,
	committedOperationID string,
	planned []*Share,
) bool {
	plannedPaths := make(map[string]string, len(planned))
	for _, share := range planned {
		if current, exists := snapshot.shares[share.ID]; exists && current.Path == share.Path && !current.Enabled {
			plannedPaths[share.ID] = share.Path
		}
	}
	if len(plannedPaths) == 0 {
		return false
	}

	changed := false
	for operationID, marker := range snapshot.trashDeleteOperations {
		if operationID == committedOperationID || marker == nil || marker.Committed || len(marker.Changed) == 0 {
			continue
		}
		retained := make([]*Share, 0, len(marker.Changed))
		for _, owned := range marker.Changed {
			if plannedPath, exists := plannedPaths[owned.ID]; exists && plannedPath == owned.Path {
				changed = true
				continue
			}
			retained = append(retained, copyShare(owned))
		}
		if len(retained) != len(marker.Changed) {
			retainedIDs := make(map[string]struct{}, len(retained))
			for _, retainedShare := range retained {
				retainedIDs[retainedShare.ID] = struct{}{}
			}
			blocked := make([]string, 0, len(marker.RestoreBlocked))
			for _, blockedID := range marker.RestoreBlocked {
				if _, exists := retainedIDs[blockedID]; exists {
					blocked = append(blocked, blockedID)
				}
			}
			updated := &shareTrashDeleteOperation{
				Planned:        cloneShareSlice(marker.Planned),
				Changed:        retained,
				RestoreBlocked: blocked,
				Committed:      marker.Committed,
				Completed:      marker.Completed,
			}
			snapshot.trashDeleteOperations[operationID] = updated
		}
	}
	return changed
}
