package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxUserQuotaTrendHistory = 64

// UserQuotaTrendPoint captures an aggregate user-quota snapshot for admin review.
type UserQuotaTrendPoint struct {
	CapturedAt       time.Time `json:"captured_at"`
	TotalCount       int       `json:"total_count"`
	ActiveCount      int       `json:"active_count"`
	LimitedCount     int       `json:"limited_count"`
	WarningCount     int       `json:"warning_count"`
	ExceededCount    int       `json:"exceeded_count"`
	AttentionCount   int       `json:"attention_count"`
	UsedBytes        int64     `json:"used_bytes"`
	LimitedUsedBytes int64     `json:"limited_used_bytes"`
	QuotaBytes       int64     `json:"quota_bytes"`
}

var (
	errUserQuotaHistorySymlink = errors.New("user quota history file path must not be a symlink")
	errInvalidUserQuotaHistory = errors.New("invalid user quota history")
)

func newUserQuotaTrendPoint(users []*User, capturedAt time.Time) UserQuotaTrendPoint {
	point := UserQuotaTrendPoint{
		CapturedAt: capturedAt.UTC(),
	}
	if point.CapturedAt.IsZero() {
		point.CapturedAt = time.Now().UTC()
	}

	for _, user := range users {
		if user == nil {
			continue
		}
		point.TotalCount++
		if !user.Disabled {
			point.ActiveCount++
		}
		usedBytes := nonNegativeQuotaValue(user.UsedBytes)
		quotaBytes := nonNegativeQuotaValue(user.QuotaBytes)
		point.UsedBytes += usedBytes
		if quotaBytes <= 0 {
			continue
		}
		point.LimitedCount++
		point.LimitedUsedBytes += usedBytes
		point.QuotaBytes += quotaBytes
		if usedBytes > quotaBytes {
			point.ExceededCount++
			point.AttentionCount++
			continue
		}
		if float64(usedBytes)/float64(quotaBytes) >= 0.9 {
			point.WarningCount++
			point.AttentionCount++
		}
	}

	return point
}

func nonNegativeQuotaValue(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func (s *UserStore) quotaHistoryFilePath() string {
	if s == nil || s.filePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.filePath), "user-quota-history.json")
}

// RecordUserQuotaTrendSnapshot records snapshot when aggregate quota values changed.
func (s *UserStore) RecordUserQuotaTrendSnapshot(snapshot UserQuotaTrendPoint) ([]UserQuotaTrendPoint, error) {
	if s == nil {
		return nil, ErrUserNotFound
	}
	if err := validateUserQuotaTrendPoint(snapshot); err != nil {
		return nil, err
	}
	historyPath := s.quotaHistoryFilePath()
	if historyPath == "" {
		return nil, fmt.Errorf("%w: missing history path", errInvalidUserQuotaHistory)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	history, err := loadUserQuotaTrendHistory(historyPath, maxUserQuotaTrendHistory)
	if err != nil {
		if recoverErr := recoverCorruptUserQuotaTrendHistory(historyPath, err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load user quota history: %w", err),
				fmt.Errorf("recover corrupt user quota history: %w", recoverErr),
			)
		}
		history = nil
	}

	history = normalizeUserQuotaTrendHistory(history, maxUserQuotaTrendHistory)
	if latest := latestUserQuotaTrendPoint(history); latest != nil && userQuotaTrendValuesEqual(*latest, snapshot) {
		return cloneUserQuotaTrendHistory(history), nil
	}

	nextHistory := normalizeUserQuotaTrendHistory(append([]UserQuotaTrendPoint{snapshot}, history...), maxUserQuotaTrendHistory)
	if err := saveUserQuotaTrendHistory(historyPath, nextHistory); err != nil {
		return nil, err
	}
	return cloneUserQuotaTrendHistory(nextHistory), nil
}

func latestUserQuotaTrendPoint(history []UserQuotaTrendPoint) *UserQuotaTrendPoint {
	if len(history) == 0 {
		return nil
	}
	return &history[0]
}

func loadUserQuotaTrendHistory(filePath string, maxSize int) ([]UserQuotaTrendPoint, error) {
	data, err := readRegisteredAuthFile(filePath, errUserQuotaHistorySymlink)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	var history []UserQuotaTrendPoint
	if err := decoder.Decode(&history); err != nil {
		return nil, err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing data after array", errInvalidUserQuotaHistory)
		}
		return nil, err
	}

	for index, point := range history {
		if err := validateUserQuotaTrendPoint(point); err != nil {
			return nil, fmt.Errorf("%w at index %d: %w", errInvalidUserQuotaHistory, index, err)
		}
	}
	return normalizeUserQuotaTrendHistory(history, maxSize), nil
}

func saveUserQuotaTrendHistory(filePath string, history []UserQuotaTrendPoint) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return writeRegisteredAuthFileAtomically(filePath, data, errUserQuotaHistorySymlink, ".user-quota-history-*.tmp", "user quota history")
}

func recoverCorruptUserQuotaTrendHistory(filePath string, loadErr error) error {
	if !isRecoverableUserQuotaTrendHistoryLoadError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", filePath, time.Now().UnixNano())
	if err := renameRegisteredAuthFile(filePath, corruptPath, errUserQuotaHistorySymlink); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("backup corrupt user quota history: %w", err)
	}
	if err := syncRegisteredAuthDir(filePath); err != nil {
		if rollbackErr := renameRegisteredAuthFile(corruptPath, filePath, errUserQuotaHistorySymlink); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt user quota history directory: %w", err),
				fmt.Errorf("rollback corrupt user quota history backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredAuthDir(filePath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt user quota history directory: %w", err),
				fmt.Errorf("sync corrupt user quota history rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt user quota history directory: %w", err)
	}
	return nil
}

func isRecoverableUserQuotaTrendHistoryLoadError(err error) bool {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errInvalidUserQuotaHistory) {
		return true
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

func validateUserQuotaTrendPoint(point UserQuotaTrendPoint) error {
	if point.CapturedAt.IsZero() {
		return fmt.Errorf("%w: captured_at is required", errInvalidUserQuotaHistory)
	}
	counts := map[string]int{
		"total_count":     point.TotalCount,
		"active_count":    point.ActiveCount,
		"limited_count":   point.LimitedCount,
		"warning_count":   point.WarningCount,
		"exceeded_count":  point.ExceededCount,
		"attention_count": point.AttentionCount,
	}
	for name, value := range counts {
		if value < 0 {
			return fmt.Errorf("%w: %s must be non-negative", errInvalidUserQuotaHistory, name)
		}
	}
	values := map[string]int64{
		"used_bytes":         point.UsedBytes,
		"limited_used_bytes": point.LimitedUsedBytes,
		"quota_bytes":        point.QuotaBytes,
	}
	for name, value := range values {
		if value < 0 {
			return fmt.Errorf("%w: %s must be non-negative", errInvalidUserQuotaHistory, name)
		}
	}
	if point.ActiveCount > point.TotalCount {
		return fmt.Errorf("%w: active_count must not exceed total_count", errInvalidUserQuotaHistory)
	}
	if point.LimitedCount > point.TotalCount {
		return fmt.Errorf("%w: limited_count must not exceed total_count", errInvalidUserQuotaHistory)
	}
	if point.WarningCount+point.ExceededCount != point.AttentionCount {
		return fmt.Errorf("%w: attention_count must equal warning_count plus exceeded_count", errInvalidUserQuotaHistory)
	}
	if point.AttentionCount > point.LimitedCount {
		return fmt.Errorf("%w: attention_count must not exceed limited_count", errInvalidUserQuotaHistory)
	}
	return nil
}

func normalizeUserQuotaTrendHistory(history []UserQuotaTrendPoint, maxSize int) []UserQuotaTrendPoint {
	normalized := make([]UserQuotaTrendPoint, 0, len(history))
	for _, point := range history {
		if err := validateUserQuotaTrendPoint(point); err == nil {
			point.CapturedAt = point.CapturedAt.UTC()
			normalized = append(normalized, point)
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].CapturedAt.After(normalized[j].CapturedAt)
	})
	if maxSize > 0 && len(normalized) > maxSize {
		normalized = normalized[:maxSize]
	}
	return normalized
}

func cloneUserQuotaTrendHistory(history []UserQuotaTrendPoint) []UserQuotaTrendPoint {
	cloned := make([]UserQuotaTrendPoint, len(history))
	copy(cloned, history)
	return cloned
}

func userQuotaTrendValuesEqual(left, right UserQuotaTrendPoint) bool {
	return left.TotalCount == right.TotalCount &&
		left.ActiveCount == right.ActiveCount &&
		left.LimitedCount == right.LimitedCount &&
		left.WarningCount == right.WarningCount &&
		left.ExceededCount == right.ExceededCount &&
		left.AttentionCount == right.AttentionCount &&
		left.UsedBytes == right.UsedBytes &&
		left.LimitedUsedBytes == right.LimitedUsedBytes &&
		left.QuotaBytes == right.QuotaBytes
}
