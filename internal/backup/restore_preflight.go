package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seanbao/mnemonas/internal/config"
)

func (m *Manager) runRestorePreflight(ctx context.Context, job config.BackupJobConfig, opts RestoreOptions, result *RestoreResult) error {
	preview := &RestorePreviewResult{
		ID:          result.ID,
		JobID:       job.ID,
		Status:      StatusRunning,
		StartedAt:   result.StartedAt,
		Source:      effectiveSource(job, m.storageRoot),
		Destination: backupTarget(job),
		TargetPath:  strings.TrimSpace(opts.TargetPath),
	}
	if err := m.runRestorePreview(ctx, job, RestorePreviewOptions{
		TargetPath:    opts.TargetPath,
		IncludeConfig: opts.IncludeConfig,
	}, preview); err != nil {
		if preview.TargetPath != "" {
			result.TargetPath = preview.TargetPath
		}
		return err
	}
	result.TargetPath = preview.TargetPath
	result.SnapshotPath = preview.SnapshotPath
	result.ManifestPath = preview.ManifestPath
	result.PreflightChecks = cloneRestorePreflightChecks(preview.PreflightChecks)
	result.Warnings = append([]string(nil), preview.Warnings...)
	result.CutoverChecklist = append([]string(nil), preview.CutoverChecklist...)
	result.RollbackChecklist = append([]string(nil), preview.RollbackChecklist...)
	return nil
}

func attachRestorePreflight(job config.BackupJobConfig, targetPath string, result *RestorePreviewResult) {
	checks, warnings, cutover, rollback := buildRestorePreflight(job, targetPath, result)
	result.PreflightChecks = checks
	result.Warnings = warnings
	result.CutoverChecklist = cutover
	result.RollbackChecklist = rollback
}

func buildRestorePreflight(job config.BackupJobConfig, targetPath string, result *RestorePreviewResult) ([]RestorePreflightCheck, []string, []string, []string) {
	var checks []RestorePreflightCheck
	var warnings []string
	addCheck := func(id, status, title, detail string) {
		checks = append(checks, RestorePreflightCheck{
			ID:     id,
			Status: status,
			Title:  title,
			Detail: detail,
		})
		if status == RestorePreflightWarning || status == RestorePreflightFailed {
			warnings = append(warnings, detail)
		}
	}

	addCheck("target_scope", RestorePreflightPassed, "目标路径隔离", "目标目录位于当前数据目录、备份来源和本地备份目标之外。")
	addCheck("target_state", RestorePreflightPassed, "目标目录状态", restoreTargetStatePreflightDetail(targetPath))
	if result.FileCount == 0 {
		addCheck("backup_content", RestorePreflightWarning, "备份内容", "预览未发现常规文件；请确认该备份目标确实包含可恢复数据。")
	} else {
		addCheck("backup_content", RestorePreflightPassed, "备份内容", fmt.Sprintf("预览发现 %d 个文件，预计恢复 %s。", result.FileCount, formatBytesForMessage(result.TotalBytes)))
	}

	if availableBytes, err := restoreAvailableBytesFunc(restoreTargetCapacityProbePath(targetPath)); err != nil {
		addCheck("target_capacity", RestorePreflightWarning, "目标容量", fmt.Sprintf("无法读取目标文件系统可用空间: %v", err))
	} else if result.TotalBytes > 0 && availableBytes < result.TotalBytes {
		addCheck("target_capacity", RestorePreflightFailed, "目标容量", fmt.Sprintf("目标文件系统可用空间 %s，小于预计恢复数据 %s。", formatBytesForMessage(availableBytes), formatBytesForMessage(result.TotalBytes)))
	} else {
		addCheck("target_capacity", RestorePreflightPassed, "目标容量", fmt.Sprintf("目标文件系统可用空间 %s，预计恢复 %s。", formatBytesForMessage(availableBytes), formatBytesForMessage(result.TotalBytes)))
	}

	switch job.Type {
	case JobTypeLocal:
		if result.ConfigAvailable && result.ConfigIncluded {
			addCheck("config_restore", RestorePreflightPassed, "配置文件", "本地快照包含配置文件，并将恢复到 .mnemonas-restore/config.toml。")
		} else if result.ConfigAvailable {
			addCheck("config_restore", RestorePreflightWarning, "配置文件", "本地快照包含配置文件，但本次不会恢复；切换 storage.root 前请确认配置是否仍适配。")
		} else if result.ConfigIncluded {
			addCheck("config_restore", RestorePreflightWarning, "配置文件", "请求恢复配置文件，但最近快照中没有可恢复配置。")
		} else {
			addCheck("config_restore", RestorePreflightPassed, "配置文件", "本次只恢复数据文件，不写入配置文件。")
		}
	case JobTypeRestic:
		addCheck("remote_restore_mode", RestorePreflightPassed, "远端恢复方式", "恢复前会读取 restic 最新快照清单；恢复后请执行只读目录校验。")
	case JobTypeRclone:
		addCheck("remote_restore_mode", RestorePreflightPassed, "远端恢复方式", "恢复前会读取 rclone 远端清单；恢复后会执行 rclone check --one-way。")
	}

	cutover := []string{
		"先对恢复目录执行只读校验，确认文件数、字节数和结构符合预期。",
		"保留当前 storage.root 和当前配置，不要直接覆盖线上目录。",
		"需要整体切换时，停止 mnemonas 和 mnemonas-dataplane 后再修改 storage.root 指向恢复目录。",
		"切换后验证健康检查、登录、文件列表、上传、下载、版本历史和分享入口。",
	}
	if result.ConfigIncluded {
		cutover = append(cutover, "若准备使用恢复出的配置文件，先人工对比端口、路径、证书、告警和公开访问设置。")
	}
	rollback := []string{
		"切换失败时停止服务，将配置指回原 storage.root 并重新启动。",
		"保留失败的恢复目录、恢复结果和校验报告，定位完成后再清理。",
		"如果恢复内容不符合预期，重新生成恢复预览，换用另一个备份任务或快照来源。",
	}
	return checks, warnings, cutover, rollback
}

func restoreTargetStatePreflightDetail(targetPath string) string {
	info, err := os.Lstat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "目标目录尚不存在；恢复会先写入临时目录，再安装到该路径。"
		}
		return "目标路径已通过安全校验；恢复执行前会再次确认目标状态。"
	}
	if info.IsDir() {
		return "目标目录已存在且为空；恢复会先写入临时目录，再安装到该目录。"
	}
	return "目标路径已通过安全校验；恢复执行前会再次确认目标状态。"
}

func restoreTargetCapacityProbePath(targetPath string) string {
	info, err := os.Lstat(targetPath)
	if err == nil && info.IsDir() {
		return targetPath
	}
	return filepath.Dir(targetPath)
}

func firstFailedRestorePreflight(checks []RestorePreflightCheck) error {
	for _, check := range checks {
		if check.Status != RestorePreflightFailed {
			continue
		}
		detail := check.Detail
		if detail == "" {
			detail = check.Title
		}
		return invalidRestoreRequestErrorf("%w: restore preflight failed: %s", ErrUnsafePath, detail)
	}
	return nil
}
