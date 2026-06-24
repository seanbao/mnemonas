package api

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/backup"
	"github.com/seanbao/mnemonas/internal/config"
)

const (
	setupLifecyclePending   = "pending"
	setupLifecycleDeferred  = "deferred"
	setupLifecycleCompleted = "completed"

	setupOverallReady          = "ready"
	setupOverallActionRequired = "action_required"
	setupOverallUnavailable    = "unavailable"

	setupRequirementRequired    = "required"
	setupRequirementRecommended = "recommended"

	setupCheckComplete      = "complete"
	setupCheckIncomplete    = "incomplete"
	setupCheckUnavailable   = "unavailable"
	setupCheckNotApplicable = "not_applicable"
)

type setupReadinessCount struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

type setupReadinessCheck struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
	Deferrable  bool   `json:"deferrable"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Action      string `json:"action"`
}

type setupReadinessSummary struct {
	AuthEnabled                      bool       `json:"auth_enabled"`
	ActiveAdminCount                 int        `json:"active_admin_count"`
	PasswordChangeRequiredAdminCount int        `json:"password_change_required_admin_count"`
	InitialPasswordFile              string     `json:"initial_password_file"`
	EnabledBackupJobCount            int        `json:"enabled_backup_job_count"`
	LatestBackupSuccessAt            *time.Time `json:"latest_backup_success_at,omitempty"`
	LatestRestoreVerificationAt      *time.Time `json:"latest_restore_verification_at,omitempty"`
	SecurityStatus                   string     `json:"security_status"`
	SecurityBlockingCheckIDs         []string   `json:"security_blocking_check_ids"`
}

type setupReadinessData struct {
	Lifecycle     string                `json:"lifecycle"`
	Prompt        bool                  `json:"prompt"`
	GeneratedAt   time.Time             `json:"generated_at"`
	CompletedAt   *time.Time            `json:"completed_at,omitempty"`
	DeferredUntil *time.Time            `json:"deferred_until,omitempty"`
	OverallStatus string                `json:"overall_status"`
	CanComplete   bool                  `json:"can_complete"`
	CanDefer      bool                  `json:"can_defer"`
	Required      setupReadinessCount   `json:"required"`
	Recommended   setupReadinessCount   `json:"recommended"`
	Checks        []setupReadinessCheck `json:"checks"`
	Summary       setupReadinessSummary `json:"summary"`
}

type setupReadinessInputs struct {
	LifecycleAvailable bool
	Lifecycle          config.SetupLifecycleState
	AuthEnabled        bool
	UsersAvailable     bool
	Users              []*auth.User
	Backup             backup.ReadinessSnapshot
	SecurityAvailable  bool
	Security           securityCheckResponse
}

type deferSetupRequest struct {
	RemindInDays int `json:"remind_in_days"`
}

func setupCheck(id, requirement, status string, deferrable bool, title, message, action string) setupReadinessCheck {
	return setupReadinessCheck{
		ID:          id,
		Requirement: requirement,
		Status:      status,
		Deferrable:  deferrable,
		Title:       title,
		Message:     message,
		Action:      action,
	}
}

func evaluateSetupReadiness(inputs setupReadinessInputs, now time.Time) setupReadinessData {
	now = now.UTC()
	activeAdmins := 0
	passwordChangeRequiredAdmins := 0
	if inputs.UsersAvailable {
		for _, user := range inputs.Users {
			if user == nil || user.Disabled || user.Role != auth.RoleAdmin {
				continue
			}
			activeAdmins++
			if user.MustChangePassword {
				passwordChangeRequiredAdmins++
			}
		}
	}

	checks := make([]setupReadinessCheck, 0, 10)
	switch {
	case !inputs.AuthEnabled:
		checks = append(checks, setupCheck("admin_access", setupRequirementRequired, setupCheckIncomplete, false, "启用管理员登录", "管理界面尚未启用账号登录。", "review_security"))
	case !inputs.UsersAvailable:
		checks = append(checks, setupCheck("admin_access", setupRequirementRequired, setupCheckUnavailable, false, "验证管理员访问", "当前无法读取管理员账号状态。", "manage_users"))
	case activeAdmins == 0:
		checks = append(checks, setupCheck("admin_access", setupRequirementRequired, setupCheckIncomplete, false, "保留可用管理员", "没有启用中的管理员账号。", "manage_users"))
	default:
		checks = append(checks, setupCheck("admin_access", setupRequirementRequired, setupCheckComplete, false, "管理员访问可用", "至少有一个启用中的管理员账号。", "manage_users"))
	}

	switch {
	case !inputs.AuthEnabled:
		checks = append(checks, setupCheck("bootstrap_credential", setupRequirementRequired, setupCheckIncomplete, false, "保护初始凭据", "启用管理员登录后才能验证初始凭据是否已更换。", "review_security"))
	case !inputs.UsersAvailable:
		checks = append(checks, setupCheck("bootstrap_credential", setupRequirementRequired, setupCheckUnavailable, false, "验证初始凭据", "当前无法读取管理员密码状态。", "change_password"))
	case activeAdmins == 0:
		checks = append(checks, setupCheck("bootstrap_credential", setupRequirementRequired, setupCheckIncomplete, false, "保护初始凭据", "需要先创建一个可用管理员账号。", "manage_users"))
	case passwordChangeRequiredAdmins > 0:
		checks = append(checks, setupCheck("bootstrap_credential", setupRequirementRequired, setupCheckIncomplete, false, "更换初始密码", "仍有管理员使用初始化流程生成的密码。", "change_password"))
	default:
		checks = append(checks, setupCheck("bootstrap_credential", setupRequirementRequired, setupCheckComplete, false, "初始密码已更换", "启用中的管理员均已完成密码更换。", "change_password"))
	}

	initialPasswordStatus := setupCheckUnavailable
	initialPasswordSummary := "unavailable"
	securityBlockingIDs := make([]string, 0)
	securityWarning := false
	if inputs.SecurityAvailable {
		for _, check := range inputs.Security.Checks {
			if check.Status == securityCheckBlock {
				securityBlockingIDs = append(securityBlockingIDs, check.ID)
			}
			if check.Status == securityCheckWarning {
				securityWarning = true
			}
			if check.ID != "initial_password_file" {
				continue
			}
			switch check.Status {
			case securityCheckPass:
				initialPasswordStatus = setupCheckComplete
				initialPasswordSummary = "missing"
			case securityCheckBlock:
				initialPasswordStatus = setupCheckIncomplete
				initialPasswordSummary = "present"
			default:
				initialPasswordStatus = setupCheckUnavailable
				initialPasswordSummary = "unavailable"
			}
		}
	}
	sort.Strings(securityBlockingIDs)
	securityBlockingIDs = compactSortedStrings(securityBlockingIDs)
	initialPasswordMessage := "无法验证服务器上的初始密码文件。"
	if initialPasswordStatus == setupCheckComplete {
		initialPasswordMessage = "服务器上没有遗留初始密码文件。"
	} else if initialPasswordStatus == setupCheckIncomplete {
		initialPasswordMessage = "服务器上仍存在初始密码文件或其路径不安全。"
	}
	checks = append(checks, setupCheck("initial_password_file", setupRequirementRequired, initialPasswordStatus, false, "清理初始密码文件", initialPasswordMessage, "change_password"))

	baselineBlockingIDs := make([]string, 0, len(securityBlockingIDs))
	for _, id := range securityBlockingIDs {
		if id != "initial_password_file" && id != "admin_accounts" {
			baselineBlockingIDs = append(baselineBlockingIDs, id)
		}
	}
	securityBaselineStatus := setupCheckComplete
	securityBaselineMessage := "安全基线没有阻断项。"
	if !inputs.SecurityAvailable {
		securityBaselineStatus = setupCheckUnavailable
		securityBaselineMessage = "当前无法完成安全基线检查。"
	} else if len(baselineBlockingIDs) > 0 {
		securityBaselineStatus = setupCheckIncomplete
		securityBaselineMessage = "安全自检仍有必须处理的阻断项。"
	}
	checks = append(checks, setupCheck("security_baseline", setupRequirementRequired, securityBaselineStatus, false, "满足安全基线", securityBaselineMessage, "review_security"))

	backupJobStatus := setupCheckIncomplete
	backupJobMessage := "尚未添加启用中的备份任务。"
	if !inputs.Backup.Available {
		backupJobStatus = setupCheckUnavailable
		backupJobMessage = "当前无法读取备份任务状态。"
	} else if inputs.Backup.EnabledJobCount > 0 {
		backupJobStatus = setupCheckComplete
		backupJobMessage = "已配置启用中的独立备份任务。"
	}
	checks = append(checks, setupCheck("backup_job", setupRequirementRequired, backupJobStatus, true, "添加独立备份", backupJobMessage, "create_backup"))

	backupSuccessStatus := setupCheckIncomplete
	backupSuccessMessage := "尚无当前有效的成功备份。"
	if !inputs.Backup.Available {
		backupSuccessStatus = setupCheckUnavailable
		backupSuccessMessage = "当前无法读取最近备份结果。"
	} else if inputs.Backup.HasCurrentHealthyBackup {
		backupSuccessStatus = setupCheckComplete
		backupSuccessMessage = "已有当前有效的成功备份。"
	}
	checks = append(checks, setupCheck("backup_success", setupRequirementRequired, backupSuccessStatus, true, "完成首次备份", backupSuccessMessage, "run_backup"))

	adminRedundancyStatus := setupCheckIncomplete
	adminRedundancyMessage := "建议再准备一个启用中的管理员账号。"
	if !inputs.UsersAvailable {
		adminRedundancyStatus = setupCheckUnavailable
		adminRedundancyMessage = "当前无法验证备用管理员。"
	} else if activeAdmins >= 2 {
		adminRedundancyStatus = setupCheckComplete
		adminRedundancyMessage = "已有备用管理员账号。"
	}
	checks = append(checks, setupCheck("admin_redundancy", setupRequirementRecommended, adminRedundancyStatus, false, "准备备用管理员", adminRedundancyMessage, "manage_users"))

	backupScheduleStatus := setupCheckIncomplete
	backupScheduleMessage := "建议为备份任务启用自动计划。"
	if !inputs.Backup.Available {
		backupScheduleStatus = setupCheckUnavailable
		backupScheduleMessage = "当前无法验证备份计划。"
	} else if inputs.Backup.EnabledScheduledJobCount > 0 {
		backupScheduleStatus = setupCheckComplete
		backupScheduleMessage = "至少有一个启用中的自动备份计划。"
	}
	checks = append(checks, setupCheck("backup_schedule", setupRequirementRecommended, backupScheduleStatus, false, "启用自动备份", backupScheduleMessage, "create_backup"))

	restoreStatus := setupCheckIncomplete
	restoreMessage := "建议执行一次恢复演练并保持验证结果有效。"
	if !inputs.Backup.Available {
		restoreStatus = setupCheckUnavailable
		restoreMessage = "当前无法读取恢复验证状态。"
	} else if inputs.Backup.HasCurrentRestoreEvidence {
		restoreStatus = setupCheckComplete
		restoreMessage = "已有当前有效的恢复验证记录。"
	}
	checks = append(checks, setupCheck("restore_verification", setupRequirementRecommended, restoreStatus, false, "验证恢复能力", restoreMessage, "run_restore_drill"))

	securityRecommendationStatus := setupCheckIncomplete
	securityRecommendationMessage := "安全自检仍有建议处理的项目。"
	if !inputs.SecurityAvailable {
		securityRecommendationStatus = setupCheckUnavailable
		securityRecommendationMessage = "当前无法读取安全建议。"
	} else if !securityWarning && len(securityBlockingIDs) == 0 {
		securityRecommendationStatus = setupCheckComplete
		securityRecommendationMessage = "安全自检全部通过。"
	}
	checks = append(checks, setupCheck("security_recommendations", setupRequirementRecommended, securityRecommendationStatus, false, "处理安全建议", securityRecommendationMessage, "review_security"))

	required := countSetupChecks(checks, setupRequirementRequired)
	recommended := countSetupChecks(checks, setupRequirementRecommended)
	requiredUnavailable := false
	nonDeferrableReady := true
	deferrableIncomplete := false
	deferrableUnavailable := false
	for _, check := range checks {
		if check.Requirement != setupRequirementRequired {
			continue
		}
		if check.Status == setupCheckUnavailable {
			requiredUnavailable = true
		}
		if check.Deferrable {
			if check.Status == setupCheckUnavailable {
				deferrableUnavailable = true
			}
			if check.Status == setupCheckIncomplete {
				deferrableIncomplete = true
			}
			continue
		}
		if check.Status != setupCheckComplete && check.Status != setupCheckNotApplicable {
			nonDeferrableReady = false
		}
	}

	overallStatus := setupOverallActionRequired
	canComplete := required.Completed == required.Total
	if !inputs.LifecycleAvailable || requiredUnavailable {
		overallStatus = setupOverallUnavailable
		canComplete = false
	} else if canComplete {
		overallStatus = setupOverallReady
	}
	canDefer := inputs.LifecycleAvailable && nonDeferrableReady && deferrableIncomplete && !deferrableUnavailable

	lifecycle := setupLifecyclePending
	prompt := inputs.LifecycleAvailable
	var completedAt *time.Time
	var deferredUntil *time.Time
	if inputs.LifecycleAvailable {
		completedAt = cloneSetupTime(inputs.Lifecycle.CompletedAt)
		deferredUntil = cloneSetupTime(inputs.Lifecycle.DeferredUntil)
		switch {
		case completedAt != nil:
			lifecycle = setupLifecycleCompleted
			prompt = false
			canComplete = false
			canDefer = false
		case deferredUntil != nil && now.Before(*deferredUntil) && nonDeferrableReady && deferrableIncomplete && !requiredUnavailable:
			lifecycle = setupLifecycleDeferred
			prompt = false
		default:
			lifecycle = setupLifecyclePending
			prompt = true
		}
	}

	securityStatus := "unavailable"
	if inputs.SecurityAvailable {
		securityStatus = string(inputs.Security.Status)
	}
	return setupReadinessData{
		Lifecycle:     lifecycle,
		Prompt:        prompt,
		GeneratedAt:   now,
		CompletedAt:   completedAt,
		DeferredUntil: deferredUntil,
		OverallStatus: overallStatus,
		CanComplete:   canComplete,
		CanDefer:      canDefer,
		Required:      required,
		Recommended:   recommended,
		Checks:        checks,
		Summary: setupReadinessSummary{
			AuthEnabled:                      inputs.AuthEnabled,
			ActiveAdminCount:                 activeAdmins,
			PasswordChangeRequiredAdminCount: passwordChangeRequiredAdmins,
			InitialPasswordFile:              initialPasswordSummary,
			EnabledBackupJobCount:            inputs.Backup.EnabledJobCount,
			LatestBackupSuccessAt:            cloneSetupTime(inputs.Backup.LastSuccessfulBackupAt),
			LatestRestoreVerificationAt:      cloneSetupTime(inputs.Backup.LastValidRestoreEvidenceAt),
			SecurityStatus:                   securityStatus,
			SecurityBlockingCheckIDs:         securityBlockingIDs,
		},
	}
}

func countSetupChecks(checks []setupReadinessCheck, requirement string) setupReadinessCount {
	count := setupReadinessCount{}
	for _, check := range checks {
		if check.Requirement != requirement {
			continue
		}
		count.Total++
		if check.Status == setupCheckComplete || check.Status == setupCheckNotApplicable {
			count.Completed++
		}
	}
	return count
}

func cloneSetupTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}

func (s *Server) buildSetupReadiness(r *http.Request, now time.Time) setupReadinessData {
	inputs := setupReadinessInputs{
		AuthEnabled:    s.authEnabled,
		UsersAvailable: s.userStore != nil,
	}
	if s.backupManager != nil {
		inputs.Backup = s.backupManager.ReadinessSnapshotContext(r.Context())
	}
	if s.userStore != nil {
		inputs.Users = s.userStore.List()
	}

	cfg := s.currentConfig()
	if cfg != nil {
		secrets, err := config.LoadSecrets(cfg.Storage.Root)
		if err == nil && secrets != nil {
			inputs.LifecycleAvailable = true
			inputs.Lifecycle = secrets.SetupLifecycle
		}
	}
	security, err := s.buildSecurityCheck(r)
	if err == nil && security != nil {
		inputs.SecurityAvailable = true
		inputs.Security = *security
	}
	return evaluateSetupReadiness(inputs, now)
}

func (s *Server) handleGetSetupReadiness(w http.ResponseWriter, r *http.Request) {
	setSetupReadResponseHeaders(w)
	readiness := s.buildSetupReadiness(r, apiTimeNow())
	NewAPIResponse(readiness).Write(w, http.StatusOK)
}

func setSetupReadResponseHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", "private, no-store")
	appendVaryHeaderToken(header, "Cookie")
	appendVaryHeaderToken(header, "Authorization")
}

func withSetupReadResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSetupReadResponseHeaders(w)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAcknowledgeSetup(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		Forbidden(w, "setup completion requires administrator authentication")
		return
	}
	var req struct{}
	if err := decodeJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}

	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	now := apiTimeNow().UTC()
	readiness := s.buildSetupReadiness(r, now)
	if readiness.Lifecycle == setupLifecycleCompleted {
		NewAPIResponse(readiness).WithMessage("setup already completed").Write(w, http.StatusOK)
		return
	}
	if readiness.OverallStatus == setupOverallUnavailable {
		writeSetupReadinessError(w, http.StatusServiceUnavailable, "SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable", readiness, true)
		return
	}
	if !readiness.CanComplete {
		writeSetupReadinessError(w, http.StatusConflict, "SETUP_NOT_READY", "required setup checks are incomplete", readiness, false)
		return
	}

	cfg := s.currentConfig()
	if cfg == nil {
		NewAPIError("SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable").Write(w, http.StatusServiceUnavailable)
		return
	}
	if _, err := config.CompleteSetup(cfg.Storage.Root, now); err != nil {
		if errors.Is(err, config.ErrSecretsNotFound) {
			NewAPIError("SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable").Write(w, http.StatusServiceUnavailable)
			return
		}
		s.respondInternalError(w, "complete setup", err)
		return
	}
	updated := s.buildSetupReadiness(r, now)
	NewAPIResponse(updated).WithMessage("setup completed").Write(w, http.StatusOK)
}

func (s *Server) handleDeferSetup(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled {
		Forbidden(w, "setup deferral requires administrator authentication")
		return
	}
	var req deferSetupRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeLimitedJSONBodyError(w, err, DefaultJSONRequestBodyLimit)
		return
	}
	if req.RemindInDays < 1 || req.RemindInDays > 30 {
		BadRequest(w, "remind_in_days must be between 1 and 30")
		return
	}

	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	now := apiTimeNow().UTC()
	readiness := s.buildSetupReadiness(r, now)
	if readiness.Lifecycle == setupLifecycleCompleted {
		writeSetupReadinessError(w, http.StatusConflict, "SETUP_DEFER_FORBIDDEN", "setup cannot be deferred", readiness, false)
		return
	}
	if readiness.OverallStatus == setupOverallUnavailable {
		writeSetupReadinessError(w, http.StatusServiceUnavailable, "SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable", readiness, true)
		return
	}
	if !readiness.CanDefer {
		writeSetupReadinessError(w, http.StatusConflict, "SETUP_DEFER_FORBIDDEN", "setup cannot be deferred", readiness, false)
		return
	}

	cfg := s.currentConfig()
	if cfg == nil {
		NewAPIError("SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable").Write(w, http.StatusServiceUnavailable)
		return
	}
	until := now.AddDate(0, 0, req.RemindInDays)
	if _, err := config.DeferSetup(cfg.Storage.Root, now, until); err != nil {
		if errors.Is(err, config.ErrSecretsNotFound) {
			NewAPIError("SETUP_READINESS_UNAVAILABLE", "setup readiness is unavailable").Write(w, http.StatusServiceUnavailable)
			return
		}
		s.respondInternalError(w, "defer setup", err)
		return
	}
	updated := s.buildSetupReadiness(r, now)
	NewAPIResponse(updated).WithMessage("setup deferred").Write(w, http.StatusOK)
}

func writeSetupReadinessError(w http.ResponseWriter, status int, code, message string, readiness setupReadinessData, includeUnavailable bool) {
	ids := make([]string, 0)
	for _, check := range readiness.Checks {
		if check.Requirement != setupRequirementRequired || check.Status == setupCheckComplete || check.Status == setupCheckNotApplicable {
			continue
		}
		if !includeUnavailable && check.Status == setupCheckUnavailable {
			continue
		}
		ids = append(ids, check.ID)
	}
	sort.Strings(ids)
	NewAPIError(code, message).WithDetails(map[string]any{"required_check_ids": ids}).Write(w, status)
}
