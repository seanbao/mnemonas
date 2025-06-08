package backup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const batchRestoreLimit = 20

// BatchRestoreItemOptions describes one restore request in a batch.
type BatchRestoreItemOptions struct {
	JobID         string `json:"job_id"`
	TargetPath    string `json:"target_path"`
	IncludeConfig bool   `json:"include_config"`
}

// BatchRestoreOptions controls batch restore preview or execution.
type BatchRestoreOptions struct {
	Items []BatchRestoreItemOptions `json:"items"`
}

// BatchRestorePreviewItemResult records one item in a batch restore preview.
type BatchRestorePreviewItemResult struct {
	Index         int                   `json:"index"`
	JobID         string                `json:"job_id"`
	TargetPath    string                `json:"target_path"`
	IncludeConfig bool                  `json:"include_config"`
	Status        string                `json:"status"`
	Preview       *RestorePreviewResult `json:"preview,omitempty"`
	ErrorMessage  string                `json:"error_message,omitempty"`
}

// BatchRestorePreviewResult records a non-destructive preview for multiple restore targets.
type BatchRestorePreviewResult struct {
	ID           string                          `json:"id"`
	Status       string                          `json:"status"`
	StartedAt    time.Time                       `json:"started_at"`
	FinishedAt   *time.Time                      `json:"finished_at,omitempty"`
	DurationMs   int64                           `json:"duration_ms"`
	Items        []BatchRestorePreviewItemResult `json:"items"`
	TotalFiles   int64                           `json:"total_files"`
	TotalBytes   int64                           `json:"total_bytes"`
	Warning      bool                            `json:"warning,omitempty"`
	Warnings     []string                        `json:"warnings,omitempty"`
	ErrorMessage string                          `json:"error_message,omitempty"`
}

// BatchRestoreItemResult records one item in a batch restore.
type BatchRestoreItemResult struct {
	Index         int                  `json:"index"`
	JobID         string               `json:"job_id"`
	TargetPath    string               `json:"target_path"`
	IncludeConfig bool                 `json:"include_config"`
	Status        string               `json:"status"`
	Restore       *RestoreResult       `json:"restore,omitempty"`
	Verify        *RestoreVerifyResult `json:"verify,omitempty"`
	Warnings      []string             `json:"warnings,omitempty"`
	ErrorMessage  string               `json:"error_message,omitempty"`
}

// BatchRestoreResult records sequential restore execution for multiple targets.
type BatchRestoreResult struct {
	ID            string                   `json:"id"`
	Status        string                   `json:"status"`
	StartedAt     time.Time                `json:"started_at"`
	FinishedAt    *time.Time               `json:"finished_at,omitempty"`
	DurationMs    int64                    `json:"duration_ms"`
	Items         []BatchRestoreItemResult `json:"items"`
	TotalFiles    int64                    `json:"total_files"`
	VerifiedBytes int64                    `json:"verified_bytes"`
	Warning       bool                     `json:"warning,omitempty"`
	Warnings      []string                 `json:"warnings,omitempty"`
	ErrorMessage  string                   `json:"error_message,omitempty"`
}

// RunBatchRestorePreview validates multiple restore requests without writing target data.
func (m *Manager) RunBatchRestorePreview(ctx context.Context, opts BatchRestoreOptions) (*BatchRestorePreviewResult, error) {
	if err := validateBatchRestoreItems(opts.Items); err != nil {
		return nil, err
	}
	startedAt := m.now().UTC()
	result := &BatchRestorePreviewResult{
		ID:        formatRunID(startedAt),
		Status:    StatusRunning,
		StartedAt: startedAt,
		Items:     make([]BatchRestorePreviewItemResult, 0, len(opts.Items)),
	}

	targets := map[string]int{}
	for index, item := range opts.Items {
		item = normalizeBatchRestoreItem(item)
		itemResult := BatchRestorePreviewItemResult{
			Index:         index,
			JobID:         item.JobID,
			TargetPath:    item.TargetPath,
			IncludeConfig: item.IncludeConfig,
			Status:        StatusRunning,
		}
		if conflict := batchRestoreTargetConflict(targets, item.TargetPath, index); conflict != "" {
			itemResult.Status = StatusFailed
			itemResult.ErrorMessage = conflict
			result.Items = append(result.Items, itemResult)
			continue
		}
		preview, err := m.RunRestorePreview(ctx, item.JobID, RestorePreviewOptions{
			TargetPath:    item.TargetPath,
			IncludeConfig: item.IncludeConfig,
		})
		if err != nil {
			itemResult.Status = StatusFailed
			itemResult.Preview = preview
			itemResult.ErrorMessage = err.Error()
			result.Items = append(result.Items, itemResult)
			continue
		}
		if failedPreflight := firstFailedRestorePreflight(preview.PreflightChecks); failedPreflight != nil {
			itemResult.Status = StatusFailed
			itemResult.Preview = preview
			itemResult.ErrorMessage = failedPreflight.Error()
			result.Items = append(result.Items, itemResult)
			continue
		}
		itemResult.Status = StatusCompleted
		itemResult.Preview = preview
		result.TotalFiles += preview.FileCount
		result.TotalBytes += preview.TotalBytes
		result.Items = append(result.Items, itemResult)
	}
	finishBatchRestorePreview(result, m.now().UTC())
	return cloneBatchRestorePreviewResult(result), nil
}

// RunBatchRestore executes multiple restore requests sequentially.
func (m *Manager) RunBatchRestore(ctx context.Context, opts BatchRestoreOptions) (*BatchRestoreResult, error) {
	if err := validateBatchRestoreItems(opts.Items); err != nil {
		return nil, err
	}
	startedAt := m.now().UTC()
	result := &BatchRestoreResult{
		ID:        formatRunID(startedAt),
		Status:    StatusRunning,
		StartedAt: startedAt,
		Items:     make([]BatchRestoreItemResult, 0, len(opts.Items)),
	}

	targets := map[string]int{}
	for index, item := range opts.Items {
		item = normalizeBatchRestoreItem(item)
		itemResult := BatchRestoreItemResult{
			Index:         index,
			JobID:         item.JobID,
			TargetPath:    item.TargetPath,
			IncludeConfig: item.IncludeConfig,
			Status:        StatusRunning,
		}
		if conflict := batchRestoreTargetConflict(targets, item.TargetPath, index); conflict != "" {
			itemResult.Status = StatusFailed
			itemResult.ErrorMessage = conflict
			result.Items = append(result.Items, itemResult)
			continue
		}
		restore, err := m.RunRestore(ctx, item.JobID, RestoreOptions{
			TargetPath:    item.TargetPath,
			IncludeConfig: item.IncludeConfig,
		})
		itemResult.Restore = restore
		if err != nil {
			itemResult.Status = StatusFailed
			itemResult.ErrorMessage = err.Error()
			result.Items = append(result.Items, itemResult)
			continue
		}
		itemResult.TargetPath = restore.TargetPath
		itemResult.Warnings = append(itemResult.Warnings, restore.Warnings...)
		verify, verifyErr := m.RunRestoreVerify(ctx, item.JobID, RestoreVerifyOptions{TargetPath: restore.TargetPath})
		itemResult.Verify = verify
		if verifyErr != nil {
			itemResult.Status = StatusFailed
			itemResult.ErrorMessage = verifyErr.Error()
			result.Items = append(result.Items, itemResult)
			continue
		}
		if len(verify.Warnings) > 0 {
			itemResult.Warnings = append(itemResult.Warnings, verify.Warnings...)
		}
		itemResult.Status = StatusCompleted
		result.TotalFiles += restore.FileCount
		result.VerifiedBytes += restore.VerifiedBytes
		result.Items = append(result.Items, itemResult)
	}
	finishBatchRestore(result, m.now().UTC())
	return cloneBatchRestoreResult(result), nil
}

func validateBatchRestoreItems(items []BatchRestoreItemOptions) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: batch restore items are empty", ErrUnsafePath)
	}
	if len(items) > batchRestoreLimit {
		return fmt.Errorf("%w: batch restore supports at most %d items", ErrUnsafePath, batchRestoreLimit)
	}
	return nil
}

func normalizeBatchRestoreItem(item BatchRestoreItemOptions) BatchRestoreItemOptions {
	item.JobID = strings.TrimSpace(item.JobID)
	item.TargetPath = strings.TrimSpace(item.TargetPath)
	return item
}

func batchRestoreTargetConflict(targets map[string]int, targetPath string, itemIndex int) string {
	if !filepath.IsAbs(targetPath) {
		return ""
	}
	target := filepath.Clean(targetPath)
	for existing, index := range targets {
		if pathContainsOrEquals(existing, target) || pathContainsOrEquals(target, existing) {
			return fmt.Sprintf("%v: restore target conflicts with batch item %d", ErrRestoreTargetExists, index)
		}
	}
	targets[target] = itemIndex
	return ""
}

func finishBatchRestorePreview(result *BatchRestorePreviewResult, finishedAt time.Time) {
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	failures := 0
	for _, item := range result.Items {
		if item.Status == StatusFailed {
			failures++
			if item.ErrorMessage != "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("item %d: %s", item.Index, item.ErrorMessage))
			}
			continue
		}
		if item.Preview != nil && len(item.Preview.Warnings) > 0 {
			result.Warning = true
			result.Warnings = append(result.Warnings, item.Preview.Warnings...)
		}
	}
	setBatchOutcome(&result.Status, &result.Warning, &result.ErrorMessage, failures, len(result.Items))
}

func finishBatchRestore(result *BatchRestoreResult, finishedAt time.Time) {
	result.FinishedAt = &finishedAt
	result.DurationMs = finishedAt.Sub(result.StartedAt).Milliseconds()
	failures := 0
	for _, item := range result.Items {
		if item.Status == StatusFailed {
			failures++
			if item.ErrorMessage != "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("item %d: %s", item.Index, item.ErrorMessage))
			}
			continue
		}
		if len(item.Warnings) > 0 {
			result.Warning = true
			result.Warnings = append(result.Warnings, item.Warnings...)
		}
	}
	setBatchOutcome(&result.Status, &result.Warning, &result.ErrorMessage, failures, len(result.Items))
}

func setBatchOutcome(status *string, warning *bool, errorMessage *string, failures int, total int) {
	switch {
	case failures == 0:
		*status = StatusCompleted
	case failures == total:
		*status = StatusFailed
		*warning = true
		*errorMessage = "all batch restore items failed"
	default:
		*status = StatusCompleted
		*warning = true
		*errorMessage = fmt.Sprintf("%d of %d batch restore items failed", failures, total)
	}
}

func cloneBatchRestorePreviewResult(result *BatchRestorePreviewResult) *BatchRestorePreviewResult {
	if result == nil {
		return nil
	}
	clone := *result
	clone.FinishedAt = cloneTime(result.FinishedAt)
	if len(result.Items) > 0 {
		clone.Items = make([]BatchRestorePreviewItemResult, 0, len(result.Items))
		for _, item := range result.Items {
			item.Preview = cloneRestorePreviewResult(item.Preview)
			clone.Items = append(clone.Items, item)
		}
	}
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	return &clone
}

func cloneBatchRestoreResult(result *BatchRestoreResult) *BatchRestoreResult {
	if result == nil {
		return nil
	}
	clone := *result
	clone.FinishedAt = cloneTime(result.FinishedAt)
	if len(result.Items) > 0 {
		clone.Items = make([]BatchRestoreItemResult, 0, len(result.Items))
		for _, item := range result.Items {
			item.Restore = cloneRestoreResult(item.Restore)
			item.Verify = cloneRestoreVerifyResult(item.Verify)
			if len(item.Warnings) > 0 {
				item.Warnings = append([]string(nil), item.Warnings...)
			}
			clone.Items = append(clone.Items, item)
		}
	}
	if len(result.Warnings) > 0 {
		clone.Warnings = append([]string(nil), result.Warnings...)
	}
	return &clone
}
