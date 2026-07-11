package storage

import (
	"context"
	"errors"
	"fmt"
)

func validateTrashPurgeParticipantHooks(hooks TrashParticipantHooks) error {
	var availabilityErr error
	if hooks.ValidatePurge == nil {
		availabilityErr = errors.Join(availabilityErr, errors.New("Trash purge participant validation is unavailable"))
	}
	if hooks.CompletePurge == nil {
		availabilityErr = errors.Join(availabilityErr, errors.New("Trash purge participant completion is unavailable"))
	}
	if hooks.RecoveryStateReliable == nil {
		availabilityErr = errors.Join(availabilityErr, errors.New("Trash purge participant recovery evidence is unavailable"))
	}
	return availabilityErr
}

func (fs *FileSystem) preflightTrashPurgeParticipant(
	ctx context.Context,
	operationID string,
	originalPath string,
	payload []byte,
) error {
	if len(payload) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if err := validateTrashPurgeParticipantHooks(hooks); err != nil {
		return err
	}
	if err := hooks.RecoveryStateReliable(); err != nil {
		return fmt.Errorf("Trash purge participant recovery evidence is unreliable: %w", err)
	}
	if err := hooks.ValidatePurge(ctx, operationID, originalPath, append([]byte(nil), payload...)); err != nil {
		return fmt.Errorf("validate Trash purge participant: %w", err)
	}
	return nil
}

func (fs *FileSystem) preflightTrashPurgeParticipantRecovery(
	ctx context.Context,
	records map[string]*trashPurgeRecoveryRecords,
	committedIDs []string,
) error {
	requiresParticipant := false
	for _, operationID := range committedIDs {
		operationRecords := records[operationID]
		if operationRecords != nil && operationRecords.committed != nil && len(operationRecords.committed.Item.RestoreData) > 0 {
			requiresParticipant = true
			break
		}
	}
	if !requiresParticipant {
		return nil
	}

	hooks := fs.trashParticipantHooksSnapshot()
	if err := validateTrashPurgeParticipantHooks(hooks); err != nil {
		return err
	}
	if err := hooks.RecoveryStateReliable(); err != nil {
		return fmt.Errorf("Trash purge participant recovery evidence is unreliable: %w", err)
	}

	var validationErr error
	for _, operationID := range committedIDs {
		if err := ctx.Err(); err != nil {
			return errors.Join(validationErr, err)
		}
		operationRecords := records[operationID]
		if operationRecords == nil || operationRecords.committed == nil {
			continue
		}
		record := operationRecords.committed
		if len(record.Item.RestoreData) == 0 {
			continue
		}
		if err := hooks.ValidatePurge(
			ctx,
			record.OperationID,
			record.Item.OriginalPath,
			append([]byte(nil), record.Item.RestoreData...),
		); err != nil {
			validationErr = errors.Join(
				validationErr,
				fmt.Errorf("validate Trash purge participant for operation %s: %w", operationID, err),
			)
		}
	}
	return validationErr
}

func (fs *FileSystem) completeTrashPurgeParticipant(
	ctx context.Context,
	operationID string,
	originalPath string,
	payload []byte,
) error {
	if len(payload) == 0 {
		return nil
	}
	hooks := fs.trashParticipantHooksSnapshot()
	if hooks.CompletePurge == nil {
		return errors.New("Trash purge participant completion is unavailable")
	}
	return hooks.CompletePurge(ctx, operationID, originalPath, append([]byte(nil), payload...))
}
