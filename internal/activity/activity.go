// Package activity provides activity logging and audit trail functionality
package activity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/seanbao/mnemonas/internal/rootio"
)

var errActivityLogSymlink = errors.New("activity log path must not be a symlink")
var errUnknownActivityAction = errors.New("unknown activity action")
var errDuplicateActivityID = errors.New("duplicate activity ID")
var errZeroActivityTimestamp = errors.New("activity timestamp must not be zero")
var errEmptyActivityID = errors.New("activity ID must not be empty")
var errNoncanonicalActivityID = errors.New("activity ID must not contain surrounding whitespace")
var errInvalidActivityPath = errors.New("invalid activity path")

// ErrInvalidReviewRecord reports invalid activity review disposition input.
var ErrInvalidReviewRecord = errors.New("invalid activity review record")

// ErrReviewRecordNotFound reports a missing persisted activity review record.
var ErrReviewRecordNotFound = errors.New("activity review record not found")

const maxActivityReviewRecords = 1000
const maxDirectoryAccessReviewRecords = 100
const maxActivityReviewNoteLength = 1000
const maxActivityReviewScopeLength = 80
const maxActivityReviewFilterSummaryLength = 512
const maxActivityReviewReviewerLength = 128
const maxActivityReviewEntryIDs = 500
const maxActivityReviewSamples = 10
const maxDirectoryAccessReviewTextLength = 64 * 1024

// ReviewDispositionStatus classifies the operational outcome recorded for reviewed activity entries.
type ReviewDispositionStatus string

const (
	ReviewDispositionDocumented    ReviewDispositionStatus = "documented"
	ReviewDispositionConfirmed     ReviewDispositionStatus = "confirmed"
	ReviewDispositionRestored      ReviewDispositionStatus = "restored"
	ReviewDispositionDisabled      ReviewDispositionStatus = "disabled"
	ReviewDispositionNeedsFollowUp ReviewDispositionStatus = "needs_follow_up"
)

func normalizeReviewDispositionStatus(status ReviewDispositionStatus) (ReviewDispositionStatus, error) {
	switch status {
	case "":
		return ReviewDispositionDocumented, nil
	case ReviewDispositionDocumented,
		ReviewDispositionConfirmed,
		ReviewDispositionRestored,
		ReviewDispositionDisabled,
		ReviewDispositionNeedsFollowUp:
		return status, nil
	default:
		return "", fmt.Errorf("%w: disposition_status is invalid", ErrInvalidReviewRecord)
	}
}

type activityLogFormatError struct {
	err error
}

func (e *activityLogFormatError) Error() string {
	return e.err.Error()
}

func (e *activityLogFormatError) Unwrap() error {
	return e.err
}

func wrapActivityLogFormatError(err error) error {
	if err == nil {
		return nil
	}
	var formatErr *activityLogFormatError
	if errors.As(err, &formatErr) {
		return err
	}
	return &activityLogFormatError{err: err}
}

func validateActivityAction(action ActionType) error {
	if IsKnownAction(action) {
		return nil
	}
	return fmt.Errorf("%w: %q", errUnknownActivityAction, action)
}

func validateActivityTimestamp(timestamp time.Time) error {
	if timestamp.IsZero() {
		return errZeroActivityTimestamp
	}
	return nil
}

func validateActivityID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errEmptyActivityID
	}
	if id != trimmed {
		return errNoncanonicalActivityID
	}
	return nil
}

func normalizeActivityEntryPath(rawPath string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(rawPath), "\\", "/")
	if normalized == "" {
		return "", nil
	}
	if containsActivityPathControlCharacter(normalized) {
		return "", errInvalidActivityPath
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "." || segment == ".." {
			return "", errInvalidActivityPath
		}
	}
	return pathpkg.Clean("/" + strings.TrimPrefix(normalized, "/")), nil
}

func containsActivityPathControlCharacter(filePath string) bool {
	return strings.IndexFunc(filePath, unicode.IsControl) >= 0
}

func validateDecodedActivityEntry(entry *Entry, seenIDs map[string]struct{}) error {
	if err := validateActivityAction(entry.Action); err != nil {
		return err
	}
	if err := validateActivityTimestamp(entry.Timestamp); err != nil {
		return err
	}
	if err := validateActivityID(entry.ID); err != nil {
		return err
	}
	if _, ok := seenIDs[entry.ID]; ok {
		return fmt.Errorf("%w: %q", errDuplicateActivityID, entry.ID)
	}
	seenIDs[entry.ID] = struct{}{}
	if normalizedPath, err := normalizeActivityEntryPath(entry.Path); err == nil {
		entry.Path = normalizedPath
	} else {
		entry.Path = ""
	}
	entry.Details = normalizeActivityDetails(entry.Details)
	return nil
}

var syncActivityLogRootDir = syncActivityRootDir
var afterValidateActivityLogPath = func() {}

var activityLogDirRootsMu sync.RWMutex
var activityLogDirRoots = map[string]*os.Root{}

const activityRootEscapeError = "path escapes from parent"

const maxActivityIDAttempts = 4

// ActionType represents the type of activity
type ActionType string

const (
	ActionUpload       ActionType = "upload"
	ActionDownload     ActionType = "download"
	ActionDelete       ActionType = "delete"
	ActionRename       ActionType = "rename"
	ActionMove         ActionType = "move"
	ActionCopy         ActionType = "copy"
	ActionCreate       ActionType = "create"
	ActionRestore      ActionType = "restore"
	ActionShare        ActionType = "share"
	ActionUnshare      ActionType = "unshare"
	ActionFavorite     ActionType = "favorite"
	ActionUnfavorite   ActionType = "unfavorite"
	ActionFavoriteNote ActionType = "favorite_note_update"
	ActionLogin        ActionType = "login"
	ActionLogout       ActionType = "logout"
	ActionTrashRestore ActionType = "trash_restore"
	ActionTrashDelete  ActionType = "trash_delete"
	ActionTrashEmpty   ActionType = "trash_empty"
	ActionDiskHealth   ActionType = "disk_health"
	ActionScrub        ActionType = "scrub"
)

// IsKnownAction reports whether action is part of MnemoNAS' public activity vocabulary.
func IsKnownAction(action ActionType) bool {
	switch action {
	case ActionUpload,
		ActionDownload,
		ActionDelete,
		ActionRename,
		ActionMove,
		ActionCopy,
		ActionCreate,
		ActionRestore,
		ActionShare,
		ActionUnshare,
		ActionFavorite,
		ActionUnfavorite,
		ActionFavoriteNote,
		ActionLogin,
		ActionLogout,
		ActionTrashRestore,
		ActionTrashDelete,
		ActionTrashEmpty,
		ActionDiskHealth,
		ActionScrub:
		return true
	default:
		return false
	}
}

// ActionGroup groups related activity actions for review workflows.
type ActionGroup string

const (
	ActionGroupShare ActionGroup = "share"
	ActionGroupRisk  ActionGroup = "risk"
)

var actionGroupActions = map[ActionGroup][]ActionType{
	ActionGroupShare: {
		ActionShare,
		ActionUnshare,
	},
	ActionGroupRisk: {
		ActionDelete,
		ActionRename,
		ActionMove,
		ActionRestore,
		ActionShare,
		ActionUnshare,
		ActionTrashRestore,
		ActionTrashDelete,
		ActionTrashEmpty,
	},
}

// Entry represents a single activity log entry
type Entry struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	Action    ActionType        `json:"action"`
	Path      string            `json:"path,omitempty"`
	User      string            `json:"user,omitempty"`
	IP        string            `json:"ip,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// ListFilter limits activity queries by metadata and timestamp.
type ListFilter struct {
	Action  ActionType
	Actions []ActionType
	User    string
	Path    string
	Since   *time.Time
	Until   *time.Time
}

// ReviewRecord represents a persisted review disposition for a set of activity entries.
type ReviewRecord struct {
	ID                string                  `json:"id"`
	ReviewedAt        time.Time               `json:"reviewed_at"`
	Reviewer          string                  `json:"reviewer"`
	Note              string                  `json:"note"`
	ScopeLabel        string                  `json:"scope_label"`
	FilterSummary     string                  `json:"filter_summary"`
	DispositionStatus ReviewDispositionStatus `json:"disposition_status"`
	ActionCounts      map[ActionType]int      `json:"action_counts,omitempty"`
	ReviewCount       int                     `json:"review_count"`
	TotalCount        int                     `json:"total_count"`
	PathCount         int                     `json:"path_count"`
	UserCount         int                     `json:"user_count"`
	PathSamples       []string                `json:"path_samples,omitempty"`
	UserSamples       []string                `json:"user_samples,omitempty"`
	ActivityEntryIDs  []string                `json:"activity_entry_ids"`
}

// ReviewRecordInput contains user-visible review disposition data before persistence.
type ReviewRecordInput struct {
	Reviewer          string
	Note              string
	ScopeLabel        string
	FilterSummary     string
	DispositionStatus ReviewDispositionStatus
	ActionCounts      map[ActionType]int
	ReviewCount       int
	TotalCount        int
	PathCount         int
	UserCount         int
	PathSamples       []string
	UserSamples       []string
	ActivityEntryIDs  []string
}

// ReviewRecordFilter limits activity review record queries.
type ReviewRecordFilter struct {
	Reviewer          string
	ActivityEntryID   string
	DispositionStatus ReviewDispositionStatus
	Actions           []ActionType
	Since             *time.Time
	Until             *time.Time
}

// DirectoryAccessReviewRecord represents a persisted directory-access review snapshot.
type DirectoryAccessReviewRecord struct {
	ID                      string    `json:"id"`
	ReviewedAt              time.Time `json:"reviewed_at"`
	Reviewer                string    `json:"reviewer"`
	Title                   string    `json:"title"`
	Path                    string    `json:"path"`
	Preview                 bool      `json:"preview"`
	Users                   int       `json:"users"`
	ReadAllowed             int       `json:"read_allowed"`
	ReadDenied              int       `json:"read_denied"`
	WriteAllowed            int       `json:"write_allowed"`
	WriteDenied             int       `json:"write_denied"`
	RelatedShares           int       `json:"related_shares"`
	ActiveRelatedShares     int       `json:"active_related_shares"`
	PasswordProtectedShares int       `json:"password_protected_shares"`
	ReportText              string    `json:"report_text"`
}

// DirectoryAccessReviewRecordInput contains a directory-access review snapshot before persistence.
type DirectoryAccessReviewRecordInput struct {
	Reviewer                string
	Title                   string
	Path                    string
	Preview                 bool
	Users                   int
	ReadAllowed             int
	ReadDenied              int
	WriteAllowed            int
	WriteDenied             int
	RelatedShares           int
	ActiveRelatedShares     int
	PasswordProtectedShares int
	ReportText              string
}

// Store manages activity log storage
type Store struct {
	root                         string
	entries                      []Entry
	reviewRecords                []ReviewRecord
	directoryAccessReviewRecords []DirectoryAccessReviewRecord
	mu                           sync.RWMutex
	writeMu                      sync.Mutex
	maxSize                      int // Maximum number of entries to keep in memory
	reviewMaxSize                int // Maximum number of review records to keep in memory
	directoryAccessReviewMaxSize int // Maximum number of directory-access review records to keep in memory
}

var activityLogWriter = writeActivityLogFile
var syncActivityLogDir = syncActivityDir
var activityRandomRead = rand.Read
var activityIDGenerator = generateID
var activityTimeNow = time.Now

func copyDetails(details map[string]string) map[string]string {
	if details == nil {
		return nil
	}
	clone := make(map[string]string, len(details))
	for key, value := range details {
		clone[key] = value
	}
	return clone
}

func normalizeActivityDetails(details map[string]string) map[string]string {
	if details == nil {
		return nil
	}
	normalized := make(map[string]string, len(details))
	for key, value := range details {
		if !DetailKeyMayContainPath(key) {
			normalized[key] = value
			continue
		}
		cleanValue, err := normalizeActivityEntryPath(value)
		if err != nil {
			normalized[key] = ""
			continue
		}
		normalized[key] = cleanValue
	}
	return normalized
}

func copyEntry(entry Entry) Entry {
	clone := entry
	clone.Details = copyDetails(entry.Details)
	return clone
}

func copyReviewRecord(record ReviewRecord) ReviewRecord {
	clone := record
	if record.ActionCounts != nil {
		clone.ActionCounts = make(map[ActionType]int, len(record.ActionCounts))
		for action, count := range record.ActionCounts {
			clone.ActionCounts[action] = count
		}
	}
	clone.PathSamples = append([]string(nil), record.PathSamples...)
	clone.UserSamples = append([]string(nil), record.UserSamples...)
	clone.ActivityEntryIDs = append([]string(nil), record.ActivityEntryIDs...)
	return clone
}

func copyDirectoryAccessReviewRecord(record DirectoryAccessReviewRecord) DirectoryAccessReviewRecord {
	return record
}

// DetailKeyMayContainPath reports whether an activity detail key conventionally stores a MnemoNAS path.
func DetailKeyMayContainPath(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "path", "from", "to", "source_path", "target_path", "destination_path", "original_path", "restore_path", "quota_path", "snapshot_path", "manifest_path", "config_path":
		return true
	default:
		return false
	}
}

// ActionsForGroup returns the actions included in a named activity group.
func ActionsForGroup(group ActionGroup) ([]ActionType, bool) {
	actions, ok := actionGroupActions[group]
	if !ok {
		return nil, false
	}
	clone := make([]ActionType, len(actions))
	copy(clone, actions)
	return clone, true
}

// NewStore creates a new activity store
func NewStore(root string) (*Store, error) {
	normalizedLogPath, err := ensureActivityLogDirRoot(filepath.Join(root, "activity.json"), true)
	if err != nil {
		return nil, err
	}

	s := &Store{
		root:                         filepath.Dir(normalizedLogPath),
		entries:                      make([]Entry, 0),
		reviewRecords:                make([]ReviewRecord, 0),
		directoryAccessReviewRecords: make([]DirectoryAccessReviewRecord, 0),
		maxSize:                      10000, // Keep last 10000 entries in memory
		reviewMaxSize:                maxActivityReviewRecords,
		directoryAccessReviewMaxSize: maxDirectoryAccessReviewRecords,
	}

	// Load existing entries
	if err := s.load(); err != nil {
		if recoverErr := s.recoverCorruptLog(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load activity log: %w", err),
				fmt.Errorf("recover corrupt activity log: %w", recoverErr),
			)
		}
	}
	if err := s.loadReviewRecords(); err != nil {
		if recoverErr := s.recoverCorruptReviewRecords(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load activity review records: %w", err),
				fmt.Errorf("recover corrupt activity review records: %w", recoverErr),
			)
		}
	}
	if err := s.loadDirectoryAccessReviewRecords(); err != nil {
		if recoverErr := s.recoverCorruptDirectoryAccessReviewRecords(err); recoverErr != nil {
			return nil, errors.Join(
				fmt.Errorf("load directory access review records: %w", err),
				fmt.Errorf("recover corrupt directory access review records: %w", recoverErr),
			)
		}
	}

	return s, nil
}

// logFilePath returns the path to the current log file
func (s *Store) logFilePath() string {
	return filepath.Join(s.root, "activity.json")
}

func (s *Store) reviewRecordsFilePath() string {
	return filepath.Join(s.root, "activity_reviews.json")
}

func (s *Store) directoryAccessReviewRecordsFilePath() string {
	return filepath.Join(s.root, "directory_access_reviews.json")
}

// load reads entries from disk
func (s *Store) load() error {
	file, err := openRegisteredActivityLogFile(s.logFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	entries, err := decodeActivityEntries(file, s.maxSize)
	if err != nil {
		return err
	}

	s.entries = entries
	return nil
}

func (s *Store) loadReviewRecords() error {
	file, err := openRegisteredActivityLogFile(s.reviewRecordsFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	records, err := decodeActivityReviewRecords(file, s.reviewMaxSize)
	if err != nil {
		return err
	}

	s.reviewRecords = records
	return nil
}

func decodeActivityEntries(reader io.Reader, maxSize int) ([]Entry, error) {
	decoder := json.NewDecoder(reader)

	startToken, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	startDelim, ok := startToken.(json.Delim)
	if !ok || startDelim != '[' {
		return nil, wrapActivityLogFormatError(errors.New("failed to parse activity log: expected JSON array"))
	}

	entries := make([]Entry, 0)
	seenIDs := make(map[string]struct{})
	for decoder.More() {
		var entry Entry
		if err := decoder.Decode(&entry); err != nil {
			return nil, err
		}
		if err := validateDecodedActivityEntry(&entry, seenIDs); err != nil {
			return nil, wrapActivityLogFormatError(err)
		}
		if maxSize <= 0 || len(entries) < maxSize {
			entries = append(entries, entry)
		}
	}

	endToken, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	endDelim, ok := endToken.(json.Delim)
	if !ok || endDelim != ']' {
		return nil, wrapActivityLogFormatError(errors.New("failed to parse activity log: expected closing array delimiter"))
	}

	extraToken, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}

	return nil, wrapActivityLogFormatError(fmt.Errorf("failed to parse activity log: trailing data after array (%v)", extraToken))
}

func decodeActivityReviewRecords(reader io.Reader, maxSize int) ([]ReviewRecord, error) {
	decoder := json.NewDecoder(reader)

	startToken, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	startDelim, ok := startToken.(json.Delim)
	if !ok || startDelim != '[' {
		return nil, wrapActivityLogFormatError(errors.New("failed to parse activity review records: expected JSON array"))
	}

	records := make([]ReviewRecord, 0)
	seenIDs := make(map[string]struct{})
	for decoder.More() {
		var record ReviewRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		if err := validateDecodedActivityReviewRecord(&record, seenIDs); err != nil {
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
		return nil, wrapActivityLogFormatError(errors.New("failed to parse activity review records: expected closing array delimiter"))
	}

	extraToken, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}

	return nil, wrapActivityLogFormatError(fmt.Errorf("failed to parse activity review records: trailing data after array (%v)", extraToken))
}

func validateDecodedActivityReviewRecord(record *ReviewRecord, seenIDs map[string]struct{}) error {
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
	normalized, err := normalizeActivityReviewRecordInput(ReviewRecordInput{
		Reviewer:          record.Reviewer,
		Note:              record.Note,
		ScopeLabel:        record.ScopeLabel,
		FilterSummary:     record.FilterSummary,
		DispositionStatus: record.DispositionStatus,
		ActionCounts:      record.ActionCounts,
		ReviewCount:       record.ReviewCount,
		TotalCount:        record.TotalCount,
		PathCount:         record.PathCount,
		UserCount:         record.UserCount,
		PathSamples:       record.PathSamples,
		UserSamples:       record.UserSamples,
		ActivityEntryIDs:  record.ActivityEntryIDs,
	})
	if err != nil {
		return err
	}
	record.Reviewer = normalized.Reviewer
	record.Note = normalized.Note
	record.ScopeLabel = normalized.ScopeLabel
	record.FilterSummary = normalized.FilterSummary
	record.DispositionStatus = normalized.DispositionStatus
	record.ActionCounts = normalized.ActionCounts
	record.ReviewCount = normalized.ReviewCount
	record.TotalCount = normalized.TotalCount
	record.PathCount = normalized.PathCount
	record.UserCount = normalized.UserCount
	record.PathSamples = normalized.PathSamples
	record.UserSamples = normalized.UserSamples
	record.ActivityEntryIDs = normalized.ActivityEntryIDs
	return nil
}

func (s *Store) recoverCorruptLog(loadErr error) error {
	if !isRecoverableActivityLogError(loadErr) {
		return loadErr
	}

	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.logFilePath(), time.Now().UnixNano())
	if err := renameRegisteredActivityLogFile(s.logFilePath(), corruptPath); err != nil {
		return fmt.Errorf("backup corrupt activity log: %w", err)
	}
	if err := syncRegisteredActivityLogDir(s.logFilePath()); err != nil {
		if rollbackErr := renameRegisteredActivityLogFile(corruptPath, s.logFilePath()); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity log directory: %w", err),
				fmt.Errorf("rollback corrupt activity log backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredActivityLogDir(s.logFilePath()); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity log directory: %w", err),
				fmt.Errorf("sync corrupt activity log rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt activity log directory: %w", err)
	}

	s.entries = make([]Entry, 0)
	return nil
}

func (s *Store) recoverCorruptReviewRecords(loadErr error) error {
	if !isRecoverableActivityLogError(loadErr) {
		return loadErr
	}

	reviewPath := s.reviewRecordsFilePath()
	corruptPath := fmt.Sprintf("%s.corrupt.%d", reviewPath, time.Now().UnixNano())
	if err := renameRegisteredActivityLogFile(reviewPath, corruptPath); err != nil {
		return fmt.Errorf("backup corrupt activity review records: %w", err)
	}
	if err := syncRegisteredActivityLogDir(reviewPath); err != nil {
		if rollbackErr := renameRegisteredActivityLogFile(corruptPath, reviewPath); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity review records directory: %w", err),
				fmt.Errorf("rollback corrupt activity review records backup: %w", rollbackErr),
			)
		}
		if rollbackSyncErr := syncRegisteredActivityLogDir(reviewPath); rollbackSyncErr != nil {
			return errors.Join(
				fmt.Errorf("sync corrupt activity review records directory: %w", err),
				fmt.Errorf("sync corrupt activity review records rollback: %w", rollbackSyncErr),
			)
		}
		return fmt.Errorf("sync corrupt activity review records directory: %w", err)
	}

	s.reviewRecords = make([]ReviewRecord, 0)
	return nil
}

func isRecoverableActivityLogError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return true
	}

	var formatErr *activityLogFormatError
	return errors.As(err, &formatErr)
}

// save writes entries to disk
func saveEntries(path string, entries []Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return activityLogWriter(path, data)
}

func saveReviewRecords(path string, records []ReviewRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return activityLogWriter(path, data)
}

func cloneEntries(entries []Entry) []Entry {
	return append([]Entry(nil), entries...)
}

func cloneReviewRecords(records []ReviewRecord) []ReviewRecord {
	cloned := make([]ReviewRecord, len(records))
	for i, record := range records {
		cloned[i] = copyReviewRecord(record)
	}
	return cloned
}

func (s *Store) updateEntries(mutator func([]Entry) ([]Entry, error)) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	currentEntries := cloneEntries(s.entries)
	logPath := s.logFilePath()
	s.mu.RUnlock()

	nextEntries, err := mutator(currentEntries)
	if err != nil {
		return err
	}
	if err := saveEntries(logPath, nextEntries); err != nil {
		return err
	}

	s.mu.Lock()
	s.entries = nextEntries
	s.mu.Unlock()
	return nil
}

func (s *Store) updateReviewRecords(mutator func([]ReviewRecord) ([]ReviewRecord, error)) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	currentRecords := cloneReviewRecords(s.reviewRecords)
	reviewPath := s.reviewRecordsFilePath()
	s.mu.RUnlock()

	nextRecords, err := mutator(currentRecords)
	if err != nil {
		return err
	}
	if err := saveReviewRecords(reviewPath, nextRecords); err != nil {
		return err
	}

	s.mu.Lock()
	s.reviewRecords = nextRecords
	s.mu.Unlock()
	return nil
}

func validateActivityLogPath(path string) error {
	cleaned, err := normalizeActivityLogPath(path)
	if err != nil {
		return err
	}

	root := filepath.VolumeName(cleaned) + string(filepath.Separator)
	current := root
	trimmed := strings.TrimPrefix(cleaned, root)
	if trimmed == "" {
		info, err := os.Lstat(cleaned)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errActivityLogSymlink
		}
		return nil
	}

	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errActivityLogSymlink
		}
	}
	return nil
}

func normalizeActivityLogPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve activity log path: %w", err)
	}
	return absPath, nil
}

func ensureActivityLogDirRoot(path string, create bool) (string, error) {
	normalizedPath, _, _, err := ensureActivityLogDirRootWithState(path, create)
	return normalizedPath, err
}

func ensureActivityLogDirRootWithState(path string, create bool) (string, *os.Root, []string, error) {
	normalizedPath, err := normalizeActivityLogPath(path)
	if err != nil {
		return "", nil, nil, err
	}
	dir := filepath.Dir(normalizedPath)

	activityLogDirRootsMu.RLock()
	root := activityLogDirRoots[dir]
	activityLogDirRootsMu.RUnlock()
	if root != nil {
		return normalizedPath, nil, nil, nil
	}

	if err := validateActivityLogPath(normalizedPath); err != nil {
		return "", nil, nil, err
	}

	createdDirs := []string(nil)
	if create {
		var err error
		createdDirs, err = ensureActivityDirTracked(dir, 0750)
		if err != nil {
			return "", nil, createdDirs, err
		}
	} else if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizedPath, nil, nil, nil
		}
		return "", nil, nil, err
	}

	root, err = os.OpenRoot(dir)
	if err != nil {
		return "", nil, createdDirs, mapActivityRootPathError(err)
	}

	activityLogDirRootsMu.Lock()
	if existing := activityLogDirRoots[dir]; existing != nil {
		activityLogDirRootsMu.Unlock()
		_ = root.Close()
		return normalizedPath, nil, createdDirs, nil
	}
	activityLogDirRoots[dir] = root
	activityLogDirRootsMu.Unlock()

	return normalizedPath, root, createdDirs, nil
}

func releaseRegisteredActivityLogDirRoot(dir string, root *os.Root) {
	if root == nil {
		return
	}
	activityLogDirRootsMu.Lock()
	if activityLogDirRoots[dir] == root {
		delete(activityLogDirRoots, dir)
	}
	activityLogDirRootsMu.Unlock()
	_ = root.Close()
}

func registeredActivityLogDirRoot(path string) (*os.Root, string, bool, error) {
	normalizedPath, err := normalizeActivityLogPath(path)
	if err != nil {
		return nil, "", false, err
	}
	dir := filepath.Dir(normalizedPath)
	activityLogDirRootsMu.RLock()
	root := activityLogDirRoots[dir]
	activityLogDirRootsMu.RUnlock()
	return root, normalizedPath, root != nil, nil
}

func openRegisteredActivityLogFile(path string) (io.ReadCloser, error) {
	root, normalizedPath, ok, err := registeredActivityLogDirRoot(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		normalizedPath, _, _, err = ensureActivityLogDirRootWithState(normalizedPath, false)
		if err != nil {
			return nil, err
		}
		root, normalizedPath, ok, err = registeredActivityLogDirRoot(normalizedPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			if err := validateActivityLogPath(normalizedPath); err != nil {
				return nil, err
			}
			return nil, &os.PathError{Op: "open", Path: normalizedPath, Err: os.ErrNotExist}
		}
	}
	return openActivityLogFileWithRoot(root, normalizedPath)
}

func writeRegisteredActivityLogFileAtomically(path string, data []byte) error {
	root, normalizedPath, ok, err := registeredActivityLogDirRoot(path)
	if err != nil {
		return err
	}
	if !ok {
		registeredRoot := (*os.Root)(nil)
		createdDirs := []string(nil)
		normalizedPath, registeredRoot, createdDirs, err = ensureActivityLogDirRootWithState(normalizedPath, true)
		if err != nil {
			releaseRegisteredActivityLogDirRoot(filepath.Dir(normalizedPath), registeredRoot)
			return cleanupCreatedActivityDirs(createdDirs, err)
		}
		releaseRootOnError := registeredRoot != nil
		if registeredRoot == nil {
			root, normalizedPath, ok, err = registeredActivityLogDirRoot(normalizedPath)
			if err != nil {
				return err
			}
			if !ok {
				return &os.PathError{Op: "open", Path: filepath.Dir(normalizedPath), Err: os.ErrNotExist}
			}
			registeredRoot = root
		}
		if err := writeActivityLogFileAtomicallyWithRoot(registeredRoot, normalizedPath, data); err != nil {
			if releaseRootOnError {
				releaseRegisteredActivityLogDirRoot(filepath.Dir(normalizedPath), registeredRoot)
				return cleanupCreatedActivityDirs(createdDirs, err)
			}
			return err
		}
		return nil
	}
	return writeActivityLogFileAtomicallyWithRoot(root, normalizedPath, data)
}

func renameRegisteredActivityLogFile(oldPath, newPath string) error {
	root, normalizedOldPath, ok, err := registeredActivityLogDirRoot(oldPath)
	if err != nil {
		return err
	}
	normalizedNewPath, err := normalizeActivityLogPath(newPath)
	if err != nil {
		return err
	}
	if filepath.Dir(normalizedOldPath) != filepath.Dir(normalizedNewPath) {
		return fmt.Errorf("activity log rename requires same parent directory")
	}
	if ok {
		afterValidateActivityLogPath()
		if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
			return mapActivityRootPathError(err)
		}
		return nil
	}
	if err := validateActivityLogPath(normalizedOldPath); err != nil {
		return err
	}
	if err := validateActivityLogPath(normalizedNewPath); err != nil {
		return err
	}
	normalizedOldPath, _, _, err = ensureActivityLogDirRootWithState(normalizedOldPath, false)
	if err != nil {
		return err
	}
	root, normalizedOldPath, ok, err = registeredActivityLogDirRoot(normalizedOldPath)
	if err != nil {
		return err
	}
	if !ok {
		return &os.PathError{Op: "rename", Path: normalizedOldPath, Err: os.ErrNotExist}
	}
	afterValidateActivityLogPath()
	if err := root.Rename(filepath.Base(normalizedOldPath), filepath.Base(normalizedNewPath)); err != nil {
		return mapActivityRootPathError(err)
	}
	return nil
}

func syncRegisteredActivityLogDir(path string) error {
	root, normalizedPath, ok, err := registeredActivityLogDirRoot(path)
	if err != nil {
		return err
	}
	if ok {
		return syncActivityLogRootDir(root)
	}
	return syncActivityLogDir(filepath.Dir(normalizedPath))
}

func openActivityLogFileWithRoot(root *os.Root, path string) (*os.File, error) {
	afterValidateActivityLogPath()

	file, err := rootio.OpenFileNoFollow(root, filepath.Base(path), os.O_RDONLY, 0)
	if err != nil {
		return nil, mapActivityRootPathError(err)
	}

	return file, nil
}

func writeActivityLogFileAtomicallyWithRoot(root *os.Root, path string, data []byte) error {
	afterValidateActivityLogPath()

	tmpFile, tmpName, err := createActivityTempFile(root, ".activity-*.tmp")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(0640); err != nil {
		_ = tmpFile.Close()
		return cleanupActivityTempPath(root, tmpName, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return cleanupActivityTempPath(root, tmpName, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return cleanupActivityTempPath(root, tmpName, err)
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupActivityTempPath(root, tmpName, err)
	}
	if err := root.Rename(tmpName, filepath.Base(path)); err != nil {
		return cleanupActivityTempPath(root, tmpName, mapActivityRootPathError(err))
	}
	cleanup = false
	if err := syncRegisteredActivityLogDir(path); err != nil {
		return fmt.Errorf("failed to sync activity log directory: %w", err)
	}
	return nil
}

func newActivityTempName(pattern string) (string, error) {
	randomPart, err := generateID()
	if err != nil {
		return "", err
	}
	if strings.Contains(pattern, "*") {
		return strings.Replace(pattern, "*", randomPart, 1), nil
	}
	return pattern + randomPart, nil
}

func createActivityTempFile(root *os.Root, pattern string) (*os.File, string, error) {
	for range 32 {
		tmpName, err := newActivityTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		tmpFile, err := rootio.OpenFileNoFollow(root, tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0640)
		if err == nil {
			return tmpFile, tmpName, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return nil, "", mapActivityRootPathError(err)
	}

	return nil, "", errors.New("failed to allocate unique activity temp file")
}

func cleanupActivityTempPath(root *os.Root, tmpPath string, operationErr error) error {
	if removeErr := root.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(operationErr, fmt.Errorf("cleanup temp activity file %s: %w", tmpPath, removeErr))
	}
	return operationErr
}

func syncActivityRootDir(root *os.Root) error {
	dirHandle, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func isActivityRootEscapeError(err error) bool {
	return err != nil && strings.Contains(err.Error(), activityRootEscapeError)
}

func mapActivityRootPathError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ELOOP) || rootio.IsSymlinkError(err) || isActivityRootEscapeError(err) {
		return errActivityLogSymlink
	}
	return err
}

func writeActivityLogFile(path string, data []byte) error {
	return writeRegisteredActivityLogFileAtomically(path, data)
}

func syncCreatedActivityDirs(createdDirs []string) error {
	for i := 0; i < len(createdDirs); i++ {
		if err := syncActivityLogDir(filepath.Dir(createdDirs[i])); err != nil {
			return fmt.Errorf("failed to sync activity directory tree: %w", err)
		}
	}
	return nil
}

func ensureActivityDirTracked(dir string, perm os.FileMode) ([]string, error) {
	createdDirs, err := rootio.MkdirAllPathNoFollowTracked(dir, perm)
	if err != nil {
		if rootio.IsSymlinkError(err) {
			return createdDirs, errActivityLogSymlink
		}
		return createdDirs, err
	}
	return createdDirs, syncCreatedActivityDirs(createdDirs)
}

func ensureActivityDir(dir string, perm os.FileMode) error {
	_, err := ensureActivityDirTracked(dir, perm)
	return err
}

func cleanupCreatedActivityDirs(createdDirs []string, operationErr error) error {
	rollbackErr := operationErr
	for _, dir := range createdDirs {
		if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("cleanup created activity directory %s: %w", dir, removeErr))
			break
		}
	}
	return rollbackErr
}

func syncActivityDir(dir string) error {
	dirHandle, err := rootio.OpenDirPathNoFollow(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()

	return dirHandle.Sync()
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := activityRandomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateUniqueActivityID(entries []Entry) (string, error) {
	existing := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		existing[entry.ID] = struct{}{}
	}

	var invalidIDErr error
	for attempt := 0; attempt < maxActivityIDAttempts; attempt++ {
		id, err := activityIDGenerator()
		if err != nil {
			return "", fmt.Errorf("generate activity ID: %w", err)
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
	return "", errors.New("generate unique activity ID: collision limit exceeded")
}

func generateUniqueActivityReviewID(records []ReviewRecord) (string, error) {
	existing := make(map[string]struct{}, len(records))
	for _, record := range records {
		existing[record.ID] = struct{}{}
	}

	var invalidIDErr error
	for attempt := 0; attempt < maxActivityIDAttempts; attempt++ {
		id, err := activityIDGenerator()
		if err != nil {
			return "", fmt.Errorf("generate activity review ID: %w", err)
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
	return "", errors.New("generate unique activity review ID: collision limit exceeded")
}

func normalizeActivityReviewText(value string, maxBytes int, field string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidReviewRecord, field)
	}
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: %s contains control characters", ErrInvalidReviewRecord, field)
	}
	if len(normalized) > maxBytes {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidReviewRecord, field)
	}
	return normalized, nil
}

func normalizeOptionalActivityReviewText(value string, maxBytes int, field string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", nil
	}
	if strings.IndexFunc(normalized, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: %s contains control characters", ErrInvalidReviewRecord, field)
	}
	if len(normalized) > maxBytes {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidReviewRecord, field)
	}
	return normalized, nil
}

func normalizeActivityReviewEntryIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: activity_entry_ids is required", ErrInvalidReviewRecord)
	}
	if len(values) > maxActivityReviewEntryIDs {
		return nil, fmt.Errorf("%w: too many activity_entry_ids", ErrInvalidReviewRecord)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if err := validateActivityID(id); err != nil {
			return nil, fmt.Errorf("%w: invalid activity_entry_ids", ErrInvalidReviewRecord)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: duplicate activity_entry_ids", ErrInvalidReviewRecord)
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

func normalizeActivityReviewActionCounts(values map[ActionType]int, reviewCount int) (map[ActionType]int, error) {
	if len(values) == 0 {
		return nil, nil
	}
	normalized := make(map[ActionType]int, len(values))
	total := 0
	for action, count := range values {
		if err := validateActivityAction(action); err != nil {
			return nil, fmt.Errorf("%w: action_counts contains an unknown action", ErrInvalidReviewRecord)
		}
		if count <= 0 {
			return nil, fmt.Errorf("%w: action_counts must be positive", ErrInvalidReviewRecord)
		}
		normalized[action] = count
		total += count
	}
	if total != reviewCount {
		return nil, fmt.Errorf("%w: action_counts must add up to review_count", ErrInvalidReviewRecord)
	}
	return normalized, nil
}

func normalizeActivityReviewPathSamples(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxActivityReviewSamples {
		return nil, fmt.Errorf("%w: too many path_samples", ErrInvalidReviewRecord)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: path_samples contains an empty path", ErrInvalidReviewRecord)
		}
		pathValue, err := normalizeActivityEntryPath(value)
		if err != nil || pathValue == "" {
			return nil, fmt.Errorf("%w: path_samples contains an invalid path", ErrInvalidReviewRecord)
		}
		if _, ok := seen[pathValue]; ok {
			return nil, fmt.Errorf("%w: duplicate path_samples", ErrInvalidReviewRecord)
		}
		seen[pathValue] = struct{}{}
		normalized = append(normalized, pathValue)
	}
	return normalized, nil
}

func normalizeActivityReviewUserSamples(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxActivityReviewSamples {
		return nil, fmt.Errorf("%w: too many user_samples", ErrInvalidReviewRecord)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		user, err := normalizeActivityReviewText(value, maxActivityReviewReviewerLength, "user_samples")
		if err != nil {
			return nil, err
		}
		if _, ok := seen[user]; ok {
			return nil, fmt.Errorf("%w: duplicate user_samples", ErrInvalidReviewRecord)
		}
		seen[user] = struct{}{}
		normalized = append(normalized, user)
	}
	return normalized, nil
}

func normalizeActivityReviewRecordInput(input ReviewRecordInput) (ReviewRecordInput, error) {
	reviewer, err := normalizeActivityReviewText(input.Reviewer, maxActivityReviewReviewerLength, "reviewer")
	if err != nil {
		return ReviewRecordInput{}, err
	}
	note, err := normalizeActivityReviewText(input.Note, maxActivityReviewNoteLength, "note")
	if err != nil {
		return ReviewRecordInput{}, err
	}
	scopeLabel, err := normalizeActivityReviewText(input.ScopeLabel, maxActivityReviewScopeLength, "scope_label")
	if err != nil {
		return ReviewRecordInput{}, err
	}
	filterSummary, err := normalizeOptionalActivityReviewText(input.FilterSummary, maxActivityReviewFilterSummaryLength, "filter_summary")
	if err != nil {
		return ReviewRecordInput{}, err
	}
	dispositionStatus, err := normalizeReviewDispositionStatus(input.DispositionStatus)
	if err != nil {
		return ReviewRecordInput{}, err
	}
	entryIDs, err := normalizeActivityReviewEntryIDs(input.ActivityEntryIDs)
	if err != nil {
		return ReviewRecordInput{}, err
	}
	if input.ReviewCount <= 0 {
		return ReviewRecordInput{}, fmt.Errorf("%w: review_count must be positive", ErrInvalidReviewRecord)
	}
	actionCounts, err := normalizeActivityReviewActionCounts(input.ActionCounts, input.ReviewCount)
	if err != nil {
		return ReviewRecordInput{}, err
	}
	if input.TotalCount < input.ReviewCount {
		return ReviewRecordInput{}, fmt.Errorf("%w: total_count must cover review_count", ErrInvalidReviewRecord)
	}
	if input.PathCount < 0 {
		return ReviewRecordInput{}, fmt.Errorf("%w: path_count must be non-negative", ErrInvalidReviewRecord)
	}
	if input.UserCount < 0 {
		return ReviewRecordInput{}, fmt.Errorf("%w: user_count must be non-negative", ErrInvalidReviewRecord)
	}
	pathSamples, err := normalizeActivityReviewPathSamples(input.PathSamples)
	if err != nil {
		return ReviewRecordInput{}, err
	}
	userSamples, err := normalizeActivityReviewUserSamples(input.UserSamples)
	if err != nil {
		return ReviewRecordInput{}, err
	}
	return ReviewRecordInput{
		Reviewer:          reviewer,
		Note:              note,
		ScopeLabel:        scopeLabel,
		FilterSummary:     filterSummary,
		DispositionStatus: dispositionStatus,
		ActionCounts:      actionCounts,
		ReviewCount:       input.ReviewCount,
		TotalCount:        input.TotalCount,
		PathCount:         input.PathCount,
		UserCount:         input.UserCount,
		PathSamples:       pathSamples,
		UserSamples:       userSamples,
		ActivityEntryIDs:  entryIDs,
	}, nil
}

// Log records a new activity entry
func (s *Store) Log(action ActionType, path, user, ip string, details map[string]string) error {
	if err := validateActivityAction(action); err != nil {
		return err
	}
	cleanPath, err := normalizeActivityEntryPath(path)
	if err != nil {
		return err
	}
	timestamp := activityTimeNow()
	if err := validateActivityTimestamp(timestamp); err != nil {
		return err
	}
	return s.updateEntries(func(entries []Entry) ([]Entry, error) {
		id, err := generateUniqueActivityID(entries)
		if err != nil {
			return nil, err
		}
		entry := Entry{
			ID:        id,
			Timestamp: timestamp,
			Action:    action,
			Path:      cleanPath,
			User:      user,
			IP:        ip,
			Details:   normalizeActivityDetails(details),
		}

		nextEntries := make([]Entry, 0, len(entries)+1)
		nextEntries = append(nextEntries, entry)
		nextEntries = append(nextEntries, entries...)
		if len(nextEntries) > s.maxSize {
			nextEntries = nextEntries[:s.maxSize]
		}
		return nextEntries, nil
	})
}

// RecordReview persists a review disposition for activity entries.
func (s *Store) RecordReview(input ReviewRecordInput) (ReviewRecord, error) {
	normalized, err := normalizeActivityReviewRecordInput(input)
	if err != nil {
		return ReviewRecord{}, err
	}
	reviewedAt := activityTimeNow()
	if err := validateActivityTimestamp(reviewedAt); err != nil {
		return ReviewRecord{}, err
	}

	var created ReviewRecord
	if err := s.updateReviewRecords(func(records []ReviewRecord) ([]ReviewRecord, error) {
		id, err := generateUniqueActivityReviewID(records)
		if err != nil {
			return nil, err
		}
		created = ReviewRecord{
			ID:                id,
			ReviewedAt:        reviewedAt,
			Reviewer:          normalized.Reviewer,
			Note:              normalized.Note,
			ScopeLabel:        normalized.ScopeLabel,
			FilterSummary:     normalized.FilterSummary,
			DispositionStatus: normalized.DispositionStatus,
			ActionCounts:      normalized.ActionCounts,
			ReviewCount:       normalized.ReviewCount,
			TotalCount:        normalized.TotalCount,
			PathCount:         normalized.PathCount,
			UserCount:         normalized.UserCount,
			PathSamples:       append([]string(nil), normalized.PathSamples...),
			UserSamples:       append([]string(nil), normalized.UserSamples...),
			ActivityEntryIDs:  append([]string(nil), normalized.ActivityEntryIDs...),
		}

		nextRecords := make([]ReviewRecord, 0, len(records)+1)
		nextRecords = append(nextRecords, created)
		nextRecords = append(nextRecords, records...)
		if len(nextRecords) > s.reviewMaxSize {
			nextRecords = nextRecords[:s.reviewMaxSize]
		}
		return nextRecords, nil
	}); err != nil {
		return ReviewRecord{}, err
	}

	return copyReviewRecord(created), nil
}

// UpdateReviewRecordDisposition updates the current disposition outcome for a persisted review record.
func (s *Store) UpdateReviewRecordDisposition(id, reviewer string, status ReviewDispositionStatus, note *string) (ReviewRecord, error) {
	if strings.TrimSpace(id) != id || id == "" {
		return ReviewRecord{}, fmt.Errorf("%w: id is invalid", ErrInvalidReviewRecord)
	}
	if err := validateActivityID(id); err != nil {
		return ReviewRecord{}, fmt.Errorf("%w: id is invalid", ErrInvalidReviewRecord)
	}
	normalizedReviewer, err := normalizeActivityReviewText(reviewer, maxActivityReviewReviewerLength, "reviewer")
	if err != nil {
		return ReviewRecord{}, err
	}
	if status == "" {
		return ReviewRecord{}, fmt.Errorf("%w: disposition_status is required", ErrInvalidReviewRecord)
	}
	normalizedStatus, err := normalizeReviewDispositionStatus(status)
	if err != nil {
		return ReviewRecord{}, err
	}
	reviewedAt := activityTimeNow()
	if err := validateActivityTimestamp(reviewedAt); err != nil {
		return ReviewRecord{}, err
	}
	var normalizedNote *string
	if note != nil {
		value, err := normalizeActivityReviewText(*note, maxActivityReviewNoteLength, "note")
		if err != nil {
			return ReviewRecord{}, err
		}
		normalizedNote = &value
	}

	var updated ReviewRecord
	if err := s.updateReviewRecords(func(records []ReviewRecord) ([]ReviewRecord, error) {
		for index := range records {
			if records[index].ID != id {
				continue
			}
			records[index].Reviewer = normalizedReviewer
			records[index].ReviewedAt = reviewedAt
			records[index].DispositionStatus = normalizedStatus
			if normalizedNote != nil {
				records[index].Note = *normalizedNote
			}
			updated = copyReviewRecord(records[index])
			return records, nil
		}
		return nil, ErrReviewRecordNotFound
	}); err != nil {
		return ReviewRecord{}, err
	}

	return copyReviewRecord(updated), nil
}

// List returns recent activity entries
func (s *Store) List(limit, offset int, actionFilter ActionType, userFilter string) ([]Entry, int) {
	return s.ListFiltered(limit, offset, ListFilter{
		Action: actionFilter,
		User:   userFilter,
	})
}

func (s *Store) ListReviewRecords(limit, offset int) ([]ReviewRecord, int) {
	return s.ListReviewRecordsFiltered(limit, offset, ReviewRecordFilter{})
}

// ListReviewRecordsFiltered returns persisted activity review dispositions matching all filters.
func (s *Store) ListReviewRecordsFiltered(limit, offset int, filter ReviewRecordFilter) ([]ReviewRecord, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if offset < 0 {
		offset = 0
	}
	filtered := make([]ReviewRecord, 0, len(s.reviewRecords))
	reviewer := strings.TrimSpace(filter.Reviewer)
	entryID := strings.TrimSpace(filter.ActivityEntryID)
	for _, record := range s.reviewRecords {
		if reviewer != "" && record.Reviewer != reviewer {
			continue
		}
		if entryID != "" && !reviewRecordContainsActivityEntryID(record, entryID) {
			continue
		}
		if filter.DispositionStatus != "" && record.DispositionStatus != filter.DispositionStatus {
			continue
		}
		if len(filter.Actions) > 0 && !reviewRecordMatchesAnyAction(record, filter.Actions) {
			continue
		}
		if filter.Since != nil && record.ReviewedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && record.ReviewedAt.After(*filter.Until) {
			continue
		}
		filtered = append(filtered, record)
	}

	total := len(filtered)
	if limit <= 0 {
		return []ReviewRecord{}, total
	}
	if offset >= total {
		return []ReviewRecord{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	records := make([]ReviewRecord, 0, end-offset)
	for _, record := range filtered[offset:end] {
		records = append(records, copyReviewRecord(record))
	}
	return records, total
}

func reviewRecordContainsActivityEntryID(record ReviewRecord, entryID string) bool {
	for _, candidate := range record.ActivityEntryIDs {
		if candidate == entryID {
			return true
		}
	}
	return false
}

func reviewRecordMatchesAnyAction(record ReviewRecord, actions []ActionType) bool {
	for _, action := range actions {
		if action == "" {
			continue
		}
		if record.ActionCounts[action] > 0 {
			return true
		}
	}
	return false
}

// ListFiltered returns recent activity entries matching all provided filters.
func (s *Store) ListFiltered(limit, offset int, filter ListFilter) ([]Entry, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if offset < 0 {
		offset = 0
	}
	filterPath := normalizeActivityFilterPath(filter.Path)

	// Filter entries
	var filtered []Entry
	for _, e := range s.entries {
		if filter.Action != "" && e.Action != filter.Action {
			continue
		}
		if len(filter.Actions) > 0 && !activityActionInList(e.Action, filter.Actions) {
			continue
		}
		if filter.User != "" && e.User != filter.User {
			continue
		}
		if filter.Path != "" && !entryMatchesActivityPathFilter(e, filterPath) {
			continue
		}
		if filter.Since != nil && e.Timestamp.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && e.Timestamp.After(*filter.Until) {
			continue
		}
		filtered = append(filtered, copyEntry(e))
	}

	total := len(filtered)

	// Apply pagination
	if limit <= 0 {
		return []Entry{}, total
	}
	if offset >= len(filtered) {
		return []Entry{}, total
	}

	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return filtered[offset:end], total
}

func activityActionInList(action ActionType, actions []ActionType) bool {
	for _, candidate := range actions {
		if action == candidate {
			return true
		}
	}
	return false
}

func entryMatchesActivityPathFilter(entry Entry, filterPath string) bool {
	if filterPath == "" {
		return false
	}
	if activityPathMatchesFilter(filterPath, entry.Path) {
		return true
	}
	for key, value := range entry.Details {
		if DetailKeyMayContainPath(key) && activityPathMatchesFilter(filterPath, value) {
			return true
		}
	}
	return false
}

func activityPathMatchesFilter(filterPath, candidate string) bool {
	cleanCandidate := normalizeActivityFilterPath(candidate)
	if cleanCandidate == "" {
		return false
	}
	if filterPath == "/" {
		return strings.HasPrefix(cleanCandidate, "/")
	}
	return cleanCandidate == filterPath || strings.HasPrefix(cleanCandidate, filterPath+"/")
}

func normalizeActivityFilterPath(value string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if normalized == "" || !strings.HasPrefix(normalized, "/") {
		return ""
	}
	return pathpkg.Clean(normalized)
}

// Count returns the total number of entries
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Clear removes all entries
func (s *Store) Clear() error {
	return s.updateEntries(func([]Entry) ([]Entry, error) {
		return make([]Entry, 0), nil
	})
}

// GetByID returns a specific entry by ID
func (s *Store) GetByID(id string) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries {
		if e.ID == id {
			entry := copyEntry(e)
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("entry not found: %s", id)
}

// Statistics returns activity statistics
func (s *Store) Statistics() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["total"] = len(s.entries)

	// Count by action type
	actionCounts := make(map[ActionType]int)
	userCounts := make(map[string]int)

	for _, e := range s.entries {
		actionCounts[e.Action]++
		if e.User != "" {
			userCounts[e.User]++
		}
	}

	stats["by_action"] = actionCounts
	stats["by_user"] = userCounts

	// Today's activity
	today := startOfLocalDay(activityTimeNow())
	todayCount := 0
	for _, e := range s.entries {
		if !e.Timestamp.Before(today) {
			todayCount++
		}
	}
	stats["today"] = todayCount

	return stats
}

func startOfLocalDay(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}
