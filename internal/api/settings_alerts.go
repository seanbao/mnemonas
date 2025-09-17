package api

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/config"
)

const settingsPolicyChangedAlertType = "settings_policy_changed"

func settingsPolicyChangedAlertEvent(currentConfig, updatedConfig config.Config) (alerts.EventPayload, bool) {
	directoryRulesChanged := !reflect.DeepEqual(currentConfig.Storage.DirectoryAccessRules, updatedConfig.Storage.DirectoryAccessRules)
	shareEnabledChanged := currentConfig.Share.Enabled != updatedConfig.Share.Enabled
	shareDefaultExpiryChanged := currentConfig.Share.DefaultExpiresIn != updatedConfig.Share.DefaultExpiresIn
	shareDefaultMaxAccessChanged := currentConfig.Share.DefaultMaxAccess != updatedConfig.Share.DefaultMaxAccess
	sharePolicyRulesChanged := !reflect.DeepEqual(currentConfig.Share.PolicyRules, updatedConfig.Share.PolicyRules)
	sharePolicyChanged := shareEnabledChanged || shareDefaultExpiryChanged || shareDefaultMaxAccessChanged || sharePolicyRulesChanged

	if !directoryRulesChanged && !sharePolicyChanged {
		return alerts.EventPayload{}, false
	}

	changedSections := make([]string, 0, 2)
	if directoryRulesChanged {
		changedSections = append(changedSections, "directory_access_rules")
	}
	if sharePolicyChanged {
		changedSections = append(changedSections, "share_policy")
	}

	return alerts.EventPayload{
		Type:      settingsPolicyChangedAlertType,
		Level:     alerts.AlertLevelWarning,
		Message:   settingsPolicyChangedAlertMessage(directoryRulesChanged, sharePolicyChanged),
		Timestamp: time.Now().UTC(),
		Details: map[string]any{
			"source":                           "settings",
			"changed_sections":                 changedSections,
			"directory_access_rules_changed":   directoryRulesChanged,
			"directory_access_rule_count":      len(updatedConfig.Storage.DirectoryAccessRules),
			"directory_access_rule_paths":      directoryAccessRulePaths(updatedConfig.Storage.DirectoryAccessRules),
			"share_policy_changed":             sharePolicyChanged,
			"share_enabled_changed":            shareEnabledChanged,
			"share_default_expiry_changed":     shareDefaultExpiryChanged,
			"share_default_max_access_changed": shareDefaultMaxAccessChanged,
			"share_policy_rules_changed":       sharePolicyRulesChanged,
			"share_policy_rule_count":          len(updatedConfig.Share.PolicyRules),
			"share_policy_rule_paths":          sharePolicyRulePaths(updatedConfig.Share.PolicyRules),
		},
	}, true
}

func settingsPolicyChangedAlertMessage(directoryRulesChanged, sharePolicyChanged bool) string {
	switch {
	case directoryRulesChanged && sharePolicyChanged:
		return "directory access and share policies changed"
	case directoryRulesChanged:
		return "directory access policy changed"
	default:
		return "share policy changed"
	}
}

func directoryAccessRulePaths(rules []config.DirectoryAccessRuleConfig) []string {
	paths := make([]string, 0, len(rules))
	for _, rule := range rules {
		paths = append(paths, rule.Path)
	}
	sort.Strings(paths)
	return paths
}

func sharePolicyRulePaths(rules []config.SharePolicyRuleConfig) []string {
	paths := make([]string, 0, len(rules))
	for _, rule := range rules {
		paths = append(paths, rule.Path)
	}
	sort.Strings(paths)
	return paths
}

func (s *Server) sendSettingsPolicyChangedAlertEvent(ctx context.Context, event alerts.EventPayload) {
	sender, ok := s.alertMonitor.(AlertEventSender)
	if !ok || sender == nil {
		return
	}
	if err := sender.SendEvent(context.WithoutCancel(ctx), event); err != nil {
		s.logger.Warn().Err(err).Str("event_type", event.Type).Msg("failed to send settings policy alert event")
	}
}
