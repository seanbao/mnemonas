package backup

import (
	"fmt"
	"strings"
)

// BuildRestoreReport returns a point-in-time restore summary for one job.
func (m *Manager) BuildRestoreReport(id string) (*RestoreReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	view := m.jobViewLocked(id, job)
	state := m.state.Jobs[id]
	lastMatchingRestoreVerify := matchingRestoreVerifyForRestore(state.LastRestore, state.LastRestoreVerify)
	report := &RestoreReport{
		GeneratedAt:               m.now().UTC(),
		Job:                       view,
		LastRun:                   cloneRunResult(view.LastRun),
		LastSuccessfulRun:         cloneRunResult(view.LastSuccessfulRun),
		LastRetentionCheck:        cloneRetentionCheckResult(view.LastRetentionCheck),
		LastRestoreDrill:          cloneRestoreDrillResult(view.LastRestoreDrill),
		RestoreDrillHistory:       cloneRestoreDrillResults(view.RestoreDrillHistory),
		RestoreDrillStats:         cloneRestoreDrillStats(view.RestoreDrillStats),
		LastRestore:               cloneRestoreResult(view.LastRestore),
		LastRestoreVerify:         cloneRestoreVerifyResult(view.LastRestoreVerify),
		LastMatchingRestoreVerify: cloneRestoreVerifyResult(lastMatchingRestoreVerify),
		RestoreHistory:            cloneRestoreResults(view.RestoreHistory),
	}
	report.Findings = cloneStringSlice(view.RestoreReportFindings)
	return report, nil
}

func restoreReportFindings(view JobView) []string {
	return restoreReportFindingsWithMatchingVerify(view, matchingRestoreVerifyForRestore(view.LastRestore, view.LastRestoreVerify))
}

func restoreReportFindingsWithMatchingVerify(view JobView, matchingRestoreVerify *RestoreVerifyResult) []string {
	var findings []string
	appendFinding := func(prefix, message string) {
		message = sanitizeBackupMessageForAPI(message)
		if strings.TrimSpace(message) == "" {
			findings = append(findings, prefix)
			return
		}
		findings = append(findings, prefix+": "+message)
	}
	if view.LastSuccessfulRun == nil {
		findings = append(findings, "尚未发现成功备份，恢复前需要先完成一次备份。")
	}
	if view.RetentionStatus == "failed" || view.RetentionStatus == "warning" {
		appendFinding("保留策略需要处理", view.RetentionMessage)
	}
	if view.RestoreDrillStatus == "failed" || view.RestoreDrillStatus == "due" || view.RestoreDrillStatus == "stale" {
		appendFinding("恢复演练需要处理", view.RestoreDrillMessage)
	}
	if view.LastRestoreDrill != nil {
		if view.LastRestoreDrill.Warning && len(view.LastRestoreDrill.Warnings) == 0 {
			findings = append(findings, "最近恢复演练完成但存在警告。")
		}
		for _, warning := range view.LastRestoreDrill.Warnings {
			findings = append(findings, "恢复演练警告: "+sanitizeBackupMessageForAPI(warning))
		}
	}
	if failedDrills := failedRestoreDrillHistoryCount(view.RestoreDrillHistory); failedDrills > 0 && view.RestoreDrillStatus != "failed" {
		findings = append(findings, fmt.Sprintf("最近恢复演练历史中有 %d 次失败，建议复查失败原因和最近一次成功演练的覆盖范围。", failedDrills))
	}
	if view.RestoreDrillStats != nil && view.RestoreDrillStats.LastFailureCategory != "" {
		appendFinding("最近恢复演练失败类型", view.RestoreDrillStats.LastFailureCategory)
	}

	if view.LastRestore == nil {
		findings = append(findings, "尚未执行过显式恢复。")
	} else if view.LastRestore.Status == StatusRunning {
		findings = append(findings, "最近一次显式恢复仍在运行，完成前不应切换恢复目标。")
	} else if view.LastRestore.Status == StatusFailed {
		appendFinding("最近一次显式恢复失败", view.LastRestore.ErrorMessage)
	} else if view.LastRestore.Status == StatusCompleted && matchingRestoreVerify == nil {
		findings = append(findings, "最近一次显式恢复尚未完成匹配的只读校验。")
		if view.LastRestoreVerify != nil {
			findings = append(findings, "最近一次只读校验不属于当前恢复目标或早于恢复完成，需重新检查当前恢复目录。")
		}
	}
	if view.LastRestoreVerify == nil && (view.LastRestore == nil || view.LastRestore.Status != StatusCompleted) {
		findings = append(findings, "尚未持久化恢复后的只读校验报告。")
	} else if matchingRestoreVerify != nil {
		if matchingRestoreVerify.Status == StatusRunning {
			findings = append(findings, "最近一次恢复目录校验仍在运行，完成前不应切换恢复目标。")
		} else if matchingRestoreVerify.Status == StatusFailed {
			appendFinding("最近一次恢复目录校验失败", matchingRestoreVerify.ErrorMessage)
		}
		for _, warning := range matchingRestoreVerify.Warnings {
			findings = append(findings, "恢复目录校验警告: "+sanitizeBackupMessageForAPI(warning))
		}
	}
	if len(findings) == 0 {
		findings = append(findings, "未发现阻塞项；仍需在切换前按恢复清单人工复核。")
	}
	return findings
}

func matchingRestoreVerifyForRestore(restore *RestoreResult, verify *RestoreVerifyResult) *RestoreVerifyResult {
	if !restoreVerifyMatchesRestore(restore, verify) {
		return nil
	}
	return verify
}

func restoreVerifyMatchesRestore(restore *RestoreResult, verify *RestoreVerifyResult) bool {
	if restore == nil || verify == nil {
		return false
	}
	if restore.Status != StatusCompleted {
		return false
	}
	if verify.Status != StatusRunning && verify.Status != StatusCompleted && verify.Status != StatusFailed {
		return false
	}
	restoreTarget := strings.TrimSpace(restore.TargetPath)
	verifyTarget := strings.TrimSpace(verify.TargetPath)
	if restoreTarget == "" || verifyTarget == "" || restoreTarget != verifyTarget {
		return false
	}
	restoreTime := restore.StartedAt
	if restore.FinishedAt != nil {
		restoreTime = *restore.FinishedAt
	}
	verifyStart := verify.StartedAt
	if restoreTime.IsZero() || verifyStart.IsZero() {
		return false
	}
	return !verifyStart.Before(restoreTime)
}

func failedRestoreDrillHistoryCount(history []*RestoreDrillResult) int {
	count := 0
	for _, result := range history {
		if result != nil && result.Status == StatusFailed {
			count++
		}
	}
	return count
}
