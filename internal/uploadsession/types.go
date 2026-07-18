// Package uploadsession provides crash-recoverable, sequential upload staging.
package uploadsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	defaultTTL                          = 72 * time.Hour
	defaultMaxChunkBytes          int64 = 8 * 1024 * 1024
	defaultMaxPerOwner                  = 32
	defaultMaxSessions                  = 512
	defaultMaxStagedBytesPerOwner int64 = 20 << 30
	defaultMaxStagedBytes         int64 = 100 << 30
	defaultCloseTimeout                 = 30 * time.Second
)

var (
	// ErrNotFound reports that a session does not exist for the requested owner.
	ErrNotFound = errors.New("upload session not found")
	// ErrConflict reports an idempotency-key or immutable-session conflict.
	ErrConflict = errors.New("upload session conflict")
	// ErrInvalidState reports that an operation is not valid in the current state.
	ErrInvalidState = errors.New("upload session state does not allow this operation")
	// ErrOffsetMismatch reports a non-sequential chunk offset.
	ErrOffsetMismatch = errors.New("upload chunk offset does not match durable offset")
	// ErrChunkTooLarge reports a chunk larger than the configured bound.
	ErrChunkTooLarge = errors.New("upload chunk exceeds the configured limit")
	// ErrChunkLength reports a body that does not match its declared chunk length.
	ErrChunkLength = errors.New("upload chunk length mismatch")
	// ErrChunkDigest reports a chunk whose SHA-256 digest does not match.
	ErrChunkDigest = errors.New("upload chunk SHA-256 mismatch")
	// ErrLimitExceeded reports a per-owner or global retained-session limit.
	ErrLimitExceeded = errors.New("upload session limit exceeded")
	// ErrStagingLimit reports that staged payload bytes reached a configured bound.
	ErrStagingLimit = errors.New("upload session staging byte limit exceeded")
	// ErrExpired reports that a non-committing session reached its hard expiry.
	ErrExpired = errors.New("upload session expired")
	// ErrRecoveryRequired reports ambiguous or corrupt durable state.
	ErrRecoveryRequired = errors.New("upload session recovery requires intervention")
	// ErrClosed reports use of a closed store.
	ErrClosed = errors.New("upload session store is closed")
	// ErrCloseTimeout reports that Close is still draining admitted operations.
	ErrCloseTimeout = errors.New("upload session store close timed out")
)

// State is the durable upload-session state.
type State string

const (
	StateUploading  State = "uploading"
	StateReady      State = "ready"
	StateCommitting State = "committing"
	StateCommitted  State = "committed"
	StateConflict   State = "conflict"
	StateCancelled  State = "cancelled"
)

// MaxChunkBytes returns the safe default maximum size of one upload chunk.
func MaxChunkBytes() int64 {
	return defaultMaxChunkBytes
}

// OriginalCondition binds a session to the destination observed at creation.
type OriginalCondition struct {
	ExpectedExists      bool   `json:"expected_exists"`
	DeleteIdentityToken string `json:"delete_identity_token"`
}

// Session is the public immutable snapshot of one durable session.
type Session struct {
	ID                 string
	Owner              string
	ClientRequestID    string
	Path               string
	TotalBytes         int64
	DurableOffset      int64
	OriginalCondition  OriginalCondition
	State              State
	Revision           uint64
	LastChunkOffset    int64
	LastChunkBytes     int64
	LastChunkID        string
	LastChunkSHA256    string
	ContentBLAKE3      string
	PublicationStarted bool
	PersistenceWarning bool
	ConflictReason     string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          time.Time
}

// Options controls store bounds. Zero values select safe defaults.
type Options struct {
	TTL                    time.Duration
	MaxChunkBytes          int64
	MaxSessionsPerOwner    int
	MaxSessions            int
	MaxStagedBytesPerOwner int64
	MaxStagedBytes         int64
	// CheckStagingCapacity runs after the store atomically reserves logical and
	// physical staging bytes, but without holding the staging accounting lock.
	// additionalBytes includes every admitted chunk that has not finished
	// materializing plus the current chunk. Concurrent checks can therefore
	// conservatively include reservations that later roll back. A returned
	// error rolls back the current reservation and is joined with
	// ErrStagingLimit.
	CheckStagingCapacity func(ctx context.Context, additionalBytes int64) error
	CloseTimeout         time.Duration
	Now                  func() time.Time
	Random               io.Reader
}

// CreateRequest contains immutable session creation input.
type CreateRequest struct {
	Owner             string
	ClientRequestID   string
	Path              string
	TotalBytes        int64
	OriginalCondition OriginalCondition
}

// CreateResult distinguishes a new session from an idempotent replay.
type CreateResult struct {
	Session Session
	Created bool
}

// AppendChunkRequest appends or idempotently replays one sequential chunk.
type AppendChunkRequest struct {
	Owner       string
	ID          string
	Offset      int64
	Length      int64
	ChunkID     string
	ChunkSHA256 string
	Body        io.Reader
}

// AppendResult reports the new durable offset and replay classification.
type AppendResult struct {
	Session  Session
	Replayed bool
}

// CommitResult records a confirmed target commit.
type CommitResult struct {
	ContentBLAKE3      string
	PersistenceWarning bool
}

// OffsetMismatchError includes the server-authoritative offset.
type OffsetMismatchError struct {
	Expected int64
	Actual   int64
}

func (e *OffsetMismatchError) Error() string {
	if e == nil {
		return ErrOffsetMismatch.Error()
	}
	return fmt.Sprintf("%s: expected %d, got %d", ErrOffsetMismatch, e.Expected, e.Actual)
}

func (e *OffsetMismatchError) Unwrap() error {
	return ErrOffsetMismatch
}
