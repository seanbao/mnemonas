package api

import (
	"context"
	"sort"
	"time"

	"github.com/seanbao/mnemonas/internal/alerts"
	"github.com/seanbao/mnemonas/internal/share"
)

const (
	shareExpiringSoonAlertType       = "share_expiring_soon"
	shareExpiryReminderWindow        = 72 * time.Hour
	shareExpiryReminderPollInterval  = time.Hour
	shareExpiryReminderSentRetention = 7 * 24 * time.Hour
)

func (s *Server) startShareExpiryReminderScheduler(ctx context.Context) bool {
	if s.shareStore == nil {
		return false
	}
	if _, ok := s.alertMonitor.(AlertEventSender); !ok {
		return false
	}

	schedulerCtx, cancel := context.WithCancel(ctx)
	s.shareExpiryReminderMu.Lock()
	if s.shareExpiryReminderCancel != nil {
		s.shareExpiryReminderCancel()
	}
	s.shareExpiryReminderCancel = cancel
	s.shareExpiryReminderMu.Unlock()

	go func() {
		ticker := time.NewTicker(shareExpiryReminderPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-schedulerCtx.Done():
				return
			case now := <-ticker.C:
				s.sendShareExpiryReminderAlerts(schedulerCtx, now.UTC())
			}
		}
	}()
	return true
}

func (s *Server) sendShareExpiryReminderAlerts(ctx context.Context, now time.Time) int {
	cfg := s.currentConfig()
	if cfg == nil || !cfg.Share.Enabled || !cfg.Alerts.Enabled || len(configuredAlertChannels(cfg.Alerts)) == 0 {
		return 0
	}
	sender, ok := s.alertMonitor.(AlertEventSender)
	if !ok || sender == nil || s.shareStore == nil {
		return 0
	}

	dueShares := s.shareExpiryReminderDueShares(now.UTC())
	if len(dueShares) == 0 {
		return 0
	}

	event := shareExpiringSoonAlertEvent(dueShares, now.UTC())
	if err := sender.SendEvent(context.WithoutCancel(ctx), event); err != nil {
		s.logger.Warn().Err(err).Str("event_type", event.Type).Msg("failed to send share expiry reminder alert event")
		return 0
	}
	s.markShareExpiryRemindersSent(dueShares, now.UTC())
	return len(dueShares)
}

func (s *Server) shareExpiryReminderDueShares(now time.Time) []*share.Share {
	shares := s.shareStore.ListAll()
	if len(shares) == 0 {
		return nil
	}

	s.shareExpiryReminderMu.Lock()
	defer s.shareExpiryReminderMu.Unlock()
	s.pruneShareExpiryReminderSentLocked(now)

	dueShares := make([]*share.Share, 0, len(shares))
	for _, shareInfo := range shares {
		if !shareNeedsExpiryReminder(shareInfo, now) {
			continue
		}
		key := shareExpiryReminderKey(shareInfo)
		if key == "" {
			continue
		}
		if _, sent := s.shareExpiryReminderSent[key]; sent {
			continue
		}
		dueShares = append(dueShares, shareInfo)
	}
	sortShareExpiryReminderShares(dueShares)
	return dueShares
}

func (s *Server) markShareExpiryRemindersSent(shares []*share.Share, now time.Time) {
	if len(shares) == 0 {
		return
	}
	s.shareExpiryReminderMu.Lock()
	defer s.shareExpiryReminderMu.Unlock()
	if s.shareExpiryReminderSent == nil {
		s.shareExpiryReminderSent = make(map[string]time.Time, len(shares))
	}
	for _, shareInfo := range shares {
		if key := shareExpiryReminderKey(shareInfo); key != "" {
			s.shareExpiryReminderSent[key] = now
		}
	}
	s.pruneShareExpiryReminderSentLocked(now)
}

func (s *Server) pruneShareExpiryReminderSentLocked(now time.Time) {
	if len(s.shareExpiryReminderSent) == 0 {
		return
	}
	for key, sentAt := range s.shareExpiryReminderSent {
		if now.Sub(sentAt) > shareExpiryReminderSentRetention {
			delete(s.shareExpiryReminderSent, key)
		}
	}
}

func shareNeedsExpiryReminder(shareInfo *share.Share, now time.Time) bool {
	if shareInfo == nil || shareInfo.ExpiresAt == nil || !shareInfo.IsActive(now) {
		return false
	}
	expiresIn := shareInfo.ExpiresAt.Sub(now)
	return expiresIn > 0 && expiresIn <= shareExpiryReminderWindow
}

func shareExpiryReminderKey(shareInfo *share.Share) string {
	if shareInfo == nil || shareInfo.ID == "" || shareInfo.ExpiresAt == nil {
		return ""
	}
	return shareInfo.ID + "|" + shareInfo.ExpiresAt.UTC().Format(time.RFC3339Nano)
}

func shareExpiringSoonAlertEvent(shares []*share.Share, now time.Time) alerts.EventPayload {
	sortShareExpiryReminderShares(shares)
	summary := shareExpiryReminderSummary(shares)

	soonestExpiresAt := time.Time{}
	if len(shares) > 0 && shares[0].ExpiresAt != nil {
		soonestExpiresAt = shares[0].ExpiresAt.UTC()
	}

	details := map[string]any{
		"source":                       "share",
		"share_count":                  len(shares),
		"window_hours":                 int(shareExpiryReminderWindow / time.Hour),
		"expires_before":               now.Add(shareExpiryReminderWindow).UTC().Format(time.RFC3339),
		"file_share_count":             summary.fileShareCount,
		"folder_share_count":           summary.folderShareCount,
		"passwordless_share_count":     summary.passwordlessShareCount,
		"unlimited_access_share_count": summary.unlimitedAccessShareCount,
	}
	if !soonestExpiresAt.IsZero() {
		details["soonest_expires_at"] = soonestExpiresAt.Format(time.RFC3339)
	}

	message := "share links are expiring soon"
	if len(shares) == 1 {
		message = "share link is expiring soon"
	}
	return alerts.EventPayload{
		Type:      shareExpiringSoonAlertType,
		Level:     alerts.AlertLevelWarning,
		Message:   message,
		Timestamp: now.UTC(),
		Details:   details,
	}
}

type shareExpiryReminderAggregate struct {
	fileShareCount            int
	folderShareCount          int
	passwordlessShareCount    int
	unlimitedAccessShareCount int
}

func shareExpiryReminderSummary(shares []*share.Share) shareExpiryReminderAggregate {
	var summary shareExpiryReminderAggregate
	for _, shareInfo := range shares {
		if shareInfo == nil {
			continue
		}
		switch shareInfo.Type {
		case share.ShareTypeFolder:
			summary.folderShareCount++
		default:
			summary.fileShareCount++
		}
		if !shareInfo.HasPassword() {
			summary.passwordlessShareCount++
		}
		if shareInfo.MaxAccess == 0 {
			summary.unlimitedAccessShareCount++
		}
	}
	return summary
}

func sortShareExpiryReminderShares(shares []*share.Share) {
	sort.SliceStable(shares, func(i, j int) bool {
		left := shares[i]
		right := shares[j]
		if left == nil || left.ExpiresAt == nil {
			return false
		}
		if right == nil || right.ExpiresAt == nil {
			return true
		}
		if !left.ExpiresAt.Equal(*right.ExpiresAt) {
			return left.ExpiresAt.Before(*right.ExpiresAt)
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.ID < right.ID
	})
}
