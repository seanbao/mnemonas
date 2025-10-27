package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
)

func (s *Store) loadDirectoryAccessReviewRecords() error {
	file, err := openRegisteredActivityLogFile(s.directoryAccessReviewRecordsFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	records, err := decodeDirectoryAccessReviewRecords(file, s.directoryAccessReviewMaxSize)
	if err != nil {
		return err
	}
	s.directoryAccessReviewRecords = records
	return nil
}

func decodeDirectoryAccessReviewRecords(reader io.Reader, maxSize int) ([]DirectoryAccessReviewRecord, error) {
	decoder := json.NewDecoder(reader)

	startToken, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	startDelim, ok := startToken.(json.Delim)
	if !ok || startDelim != '[' {
		return nil, wrapActivityLogFormatError(errors.New("failed to parse directory access review records: expected JSON array"))
	}

	records := make([]DirectoryAccessReviewRecord, 0)
	seenIDs := make(map[string]struct{})
	for decoder.More() {
		var record DirectoryAccessReviewRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		if err := validateDecodedDirectoryAccessReviewRecord(&record, seenIDs); err != nil {
			return nil, wrapActivityLogFormatError(err)
		}
		if maxSize <= 0 || len(records) < maxSize {
			records = append(records, record)
		}
	}

	endToken, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	endDelim, ok := endToken.(json.Delim)
	if !ok || endDelim != ']' {
		return nil, wrapActivityLogFormatError(errors.New("failed to parse directory access review records: expected closing array delimiter"))
	}

	extraToken, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, wrapActivityLogFormatError(fmt.Errorf("failed to parse directory access review records: trailing data after array (%v)", extraToken))
}

func validateDecodedDirectoryAccessReviewRecord(record *DirectoryAccessReviewRecord, seenIDs map[string]struct{}) error {
	if err := validateActivityID(record.ID); err != nil {
		return err
	}
	if _, ok := seenIDs[record.ID]; ok {
		return fmt.Errorf("%w: %q", errDuplicateActivityID, record.ID)
	}
	seenIDs[record.ID] = struct{}{}
	if err := validateActivityTimestamp(record.ReviewedAt); err != nil {
		return err
	}
	normalized, err := normalizeDirectoryAccessReviewRecordInput(DirectoryAccessReviewRecordInput{
		Reviewer:                record.Reviewer,
		Title:                   record.Title,
		Path:                    record.Path,
		Preview:                 record.Preview,
		Users:                   record.Users,
		ReadAllowed:             record.ReadAllowed,
		ReadDenied:              record.ReadDenied,
		WriteAllowed:            record.WriteAllowed,
		WriteDenied:             record.WriteDenied,
		RelatedShares:           record.RelatedShares,
		ActiveRelatedShares:     record.ActiveRelatedShares,
		PasswordProtectedShares: record.PasswordProtectedShares,
		ReportText:              record.ReportText,
	})
	if err != nil {
		return err
	}
	record.Reviewer = normalized.Reviewer
	record.Title = normalized.Title
	record.Path = normalized.Path
	record.Users = normalized.Users
	record.ReadAllowed = normalized.ReadAllowed
	record.ReadDenied = normalized.ReadDenied
	record.WriteAllowed = normalized.WriteAllowed
	record.WriteDenied = normalized.WriteDenied
	record.RelatedShares = normalized.RelatedShares
	record.ActiveRelatedShares = normalized.ActiveRelatedShares
	record.PasswordProtectedShares = normalized.PasswordProtectedShares
	record.ReportText = normalized.ReportText
	return nil
}

func saveDirectoryAccessReviewRecords(path string, records []DirectoryAccessReviewRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return activityLogWriter(path, data)
}

func cloneDirectoryAccessReviewRecords(records []DirectoryAccessReviewRecord) []DirectoryAccessReviewRecord {
	cloned := make([]DirectoryAccessReviewRecord, len(records))
	copy(cloned, records)
	return cloned
}

func (s *Store) updateDirectoryAccessReviewRecords(mutator func([]DirectoryAccessReviewRecord) ([]DirectoryAccessReviewRecord, error)) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	currentRecords := cloneDirectoryAccessReviewRecords(s.directoryAccessReviewRecords)
	reviewPath := s.directoryAccessReviewRecordsFilePath()
	s.mu.RUnlock()

	nextRecords, err := mutator(currentRecords)
	if err != nil {
		return err
	}
	if err := saveDirectoryAccessReviewRecords(reviewPath, nextRecords); err != nil {
		return err
	}

	s.mu.Lock()
	s.directoryAccessReviewRecords = nextRecords
	s.mu.Unlock()
	return nil
}

func (s *Store) recoverCorruptDirectoryAccessReviewRecords(loadErr error) error {
	if !isRecoverableActivityLogError(loadErr) {
		return loadErr
	}

	reviewPath := s.directoryAccessReviewRecordsFilePath()
	corruptPath := fmt.Sprintf("%s.corrupt.%d", reviewPath, time.Now().UnixNano())
	if err := renameRegisteredActivityLogFile(reviewPath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt directory access review records: %w", err)
	}
	if err := syncRegisteredActivityLogDir(reviewPath); err != nil {
		if rollbackErr := renameRegisteredActivityLogFile(corruptPath, reviewPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt directory access review records directory: %w", err),
				fmt.Errorf("rollback corrupt directory access review records backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredActivityLogDir(reviewPath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt directory access review records directory: %w", err),
				fmt.Errorf("sync corrupt directory access review records rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt directory access review records directory: %w", err)
	}

	s.directoryAccessReviewRecords = make([]DirectoryAccessReviewRecord, 0)
	return nil
}

func normalizeDirectoryAccessReviewRecordInput(input DirectoryAccessReviewRecordInput) (DirectoryAccessReviewRecordInput, error) {
	reviewer, err := normalizeActivityReviewText(input.Reviewer, maxActivityReviewReviewerLength, "reviewer")
	if err != nil {
		return DirectoryAccessReviewRecordInput{}, err
	}
	title, err := normalizeActivityReviewText(input.Title, maxActivityReviewScopeLength, "title")
	if err != nil {
		return DirectoryAccessReviewRecordInput{}, err
	}
	if strings.TrimSpace(input.Path) == "" {
		return DirectoryAccessReviewRecordInput{}, fmt.Errorf("%w: path is required", ErrInvalidReviewRecord)
	}
	pathValue, err := normalizeActivityEntryPath(input.Path)
	if err != nil || pathValue == "" {
		return DirectoryAccessReviewRecordInput{}, fmt.Errorf("%w: path is invalid", ErrInvalidReviewRecord)
	}
	if err := validateDirectoryAccessReviewCounts(input); err != nil {
		return DirectoryAccessReviewRecordInput{}, err
	}
	reportText, err := normalizeDirectoryAccessReviewReportText(input.ReportText)
	if err != nil {
		return DirectoryAccessReviewRecordInput{}, err
	}
	input.Reviewer = reviewer
	input.Title = title
	input.Path = pathValue
	input.ReportText = reportText
	return input, nil
}

func validateDirectoryAccessReviewCounts(input DirectoryAccessReviewRecordInput) error {
	values := map[string]int{
		"users":                     input.Users,
		"read_allowed":              input.ReadAllowed,
		"read_denied":               input.ReadDenied,
		"write_allowed":             input.WriteAllowed,
		"write_denied":              input.WriteDenied,
		"related_shares":            input.RelatedShares,
		"active_related_shares":     input.ActiveRelatedShares,
		"password_protected_shares": input.PasswordProtectedShares,
	}
	for name, value := range values {
		if value < 0 {
			return fmt.Errorf("%w: %s must be non-negative", ErrInvalidReviewRecord, name)
		}
	}
	if input.ReadAllowed+input.ReadDenied != input.Users {
		return fmt.Errorf("%w: read counts must add up to users", ErrInvalidReviewRecord)
	}
	if input.WriteAllowed+input.WriteDenied != input.Users {
		return fmt.Errorf("%w: write counts must add up to users", ErrInvalidReviewRecord)
	}
	if input.ActiveRelatedShares > input.RelatedShares {
		return fmt.Errorf("%w: active_related_shares must not exceed related_shares", ErrInvalidReviewRecord)
	}
	if input.PasswordProtectedShares > input.RelatedShares {
		return fmt.Errorf("%w: password_protected_shares must not exceed related_shares", ErrInvalidReviewRecord)
	}
	return nil
}

func normalizeDirectoryAccessReviewReportText(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", fmt.Errorf("%w: report_text is required", ErrInvalidReviewRecord)
	}
	if len(normalized) > maxDirectoryAccessReviewTextLength {
		return "", fmt.Errorf("%w: report_text is too long", ErrInvalidReviewRecord)
	}
	if strings.IndexFunc(normalized, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0 {
		return "", fmt.Errorf("%w: report_text contains control characters", ErrInvalidReviewRecord)
	}
	return normalized, nil
}

func generateUniqueDirectoryAccessReviewID(records []DirectoryAccessReviewRecord) (string, error) {
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.ID] = struct{}{}
	}

	var invalidIDErr error
	for attempt := 0; attempt < maxActivityIDAttempts; attempt++ {
		id, err := activityIDGenerator()
		if err != nil {
			return "", fmt.Errorf("generate directory access review ID: %w", err)
		}
		if err := validateActivityID(id); err != nil {
			invalidIDErr = err
			continue
		}
		if _, ok := existing[id]; !ok {
			return id, nil
		}
	}
	if invalidIDErr != nil {
		return "", invalidIDErr
	}
	return "", errors.New("generate unique directory access review ID: collision limit exceeded")
}

// RecordDirectoryAccessReview persists a directory-access review snapshot.
func (s *Store) RecordDirectoryAccessReview(input DirectoryAccessReviewRecordInput) (DirectoryAccessReviewRecord, error) {
	normalized, err := normalizeDirectoryAccessReviewRecordInput(input)
	if err != nil {
		return DirectoryAccessReviewRecord{}, err
	}
	reviewedAt := activityTimeNow()
	if err := validateActivityTimestamp(reviewedAt); err != nil {
		return DirectoryAccessReviewRecord{}, err
	}

	var created DirectoryAccessReviewRecord
	if err := s.updateDirectoryAccessReviewRecords(func(records []DirectoryAccessReviewRecord) ([]DirectoryAccessReviewRecord, error) {
		id, err := generateUniqueDirectoryAccessReviewID(records)
		if err != nil {
			return nil, err
		}
		created = DirectoryAccessReviewRecord{
			ID:                      id,
			ReviewedAt:              reviewedAt,
			Reviewer:                normalized.Reviewer,
			Title:                   normalized.Title,
			Path:                    normalized.Path,
			Preview:                 normalized.Preview,
			Users:                   normalized.Users,
			ReadAllowed:             normalized.ReadAllowed,
			ReadDenied:              normalized.ReadDenied,
			WriteAllowed:            normalized.WriteAllowed,
			WriteDenied:             normalized.WriteDenied,
			RelatedShares:           normalized.RelatedShares,
			ActiveRelatedShares:     normalized.ActiveRelatedShares,
			PasswordProtectedShares: normalized.PasswordProtectedShares,
			ReportText:              normalized.ReportText,
		}

		nextRecords := make([]DirectoryAccessReviewRecord, 0, len(records)+1)
		nextRecords = append(nextRecords, created)
		for _, record := range records {
			if record.Reviewer == created.Reviewer && record.Path == created.Path && record.Title == created.Title && record.Preview == created.Preview {
				continue
			}
			nextRecords = append(nextRecords, record)
		}
		if len(nextRecords) > s.directoryAccessReviewMaxSize {
			nextRecords = nextRecords[:s.directoryAccessReviewMaxSize]
		}
		return nextRecords, nil
	}); err != nil {
		return DirectoryAccessReviewRecord{}, err
	}
	return copyDirectoryAccessReviewRecord(created), nil
}

// ListDirectoryAccessReviewRecords returns recent directory-access review snapshots.
func (s *Store) ListDirectoryAccessReviewRecords(limit, offset int) ([]DirectoryAccessReviewRecord, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if offset < 0 {
		offset = 0
	}
	total := len(s.directoryAccessReviewRecords)
	if limit <= 0 || offset >= total {
		return []DirectoryAccessReviewRecord{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	records := make([]DirectoryAccessReviewRecord, end-offset)
	copy(records, s.directoryAccessReviewRecords[offset:end])
	return records, total
}

// ClearDirectoryAccessReviewRecords removes all persisted directory-access review snapshots.
func (s *Store) ClearDirectoryAccessReviewRecords() error {
	return s.updateDirectoryAccessReviewRecords(func([]DirectoryAccessReviewRecord) ([]DirectoryAccessReviewRecord, error) {
		return []DirectoryAccessReviewRecord{}, nil
	})
}
