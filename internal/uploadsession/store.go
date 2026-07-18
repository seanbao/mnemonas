package uploadsession

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zeebo/blake3"

	"github.com/seanbao/mnemonas/internal/rootio"
	"github.com/seanbao/mnemonas/internal/workspace"
)

const (
	stateSchemaVersion = 3
	lockFileName       = ".store.lock"
	payloadFileName    = "payload.bin"
	maxStateFileBytes  = 64 * 1024
	blake3HexLength    = 64
)

var errIncompleteCreate = errors.New("incomplete upload session creation")

type diskState struct {
	SchemaVersion       int               `json:"schema_version"`
	ID                  string            `json:"id"`
	Owner               string            `json:"owner"`
	ClientRequestID     string            `json:"client_request_id"`
	Path                string            `json:"path"`
	TotalBytes          int64             `json:"total_bytes"`
	DurableOffset       int64             `json:"durable_offset"`
	OriginalCondition   OriginalCondition `json:"original_condition"`
	State               State             `json:"state"`
	Revision            uint64            `json:"revision"`
	PreviousRevision    uint64            `json:"previous_revision"`
	PreviousStateSHA256 string            `json:"previous_state_sha256"`
	PayloadIdentity     string            `json:"payload_identity"`
	LastChunkOffset     int64             `json:"last_chunk_offset"`
	LastChunkBytes      int64             `json:"last_chunk_bytes"`
	LastChunkID         string            `json:"last_chunk_id"`
	LastChunkSHA256     string            `json:"last_chunk_sha256"`
	ContentBLAKE3       string            `json:"content_blake3"`
	PublicationStarted  bool              `json:"publication_started"`
	PersistenceWarning  bool              `json:"persistence_warning"`
	ConflictReason      string            `json:"conflict_reason"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	ExpiresAt           time.Time         `json:"expires_at"`
}

type loadedSession struct {
	state  diskState
	raw    []byte
	digest string
}

type requestKey struct {
	owner           string
	clientRequestID string
}

type payloadReader struct {
	store     *Store
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

// Store owns one exclusive durable upload-session root.
type Store struct {
	rootPath string
	rootInfo os.FileInfo
	lockFile *os.File
	opts     Options

	createMu              sync.Mutex
	randomMu              sync.Mutex
	gateMu                sync.Mutex
	activeOps             sync.WaitGroup
	closing               bool
	closeDone             chan struct{}
	closeErr              error
	lifetimeContext       context.Context
	lifetimeCancel        context.CancelFunc
	openPayloads          map[*payloadReader]struct{}
	stagingMu             sync.Mutex
	stagedBytes           int64
	physicalReservedBytes int64
	stagedByOwner         map[string]int64
	stagedBySession       map[string]int64
	mu                    sync.RWMutex
	closed                bool
	sessions              map[string]Session
	requestIndex          map[requestKey]string
	sessionLocks          sync.Map
}

// Open creates or opens a durable store and performs startup recovery.
func Open(root string, options Options) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("upload session root is required")
	}
	root = filepath.Clean(root)
	if err := normalizeOptions(&options); err != nil {
		return nil, err
	}
	if err := rootio.MkdirAllPathNoFollow(root, 0o700); err != nil {
		return nil, fmt.Errorf("create upload session root: %w", err)
	}
	rootDir, err := rootio.OpenDirPathNoFollow(root)
	if err != nil {
		return nil, fmt.Errorf("open upload session root: %w", err)
	}
	if err := rootDir.Chmod(0o700); err != nil {
		_ = rootDir.Close()
		return nil, fmt.Errorf("set upload session root permissions: %w", err)
	}
	if err := rootDir.Sync(); err != nil {
		_ = rootDir.Close()
		return nil, fmt.Errorf("sync upload session root: %w", err)
	}
	rootInfo, err := rootDir.Stat()
	if err != nil {
		_ = rootDir.Close()
		return nil, fmt.Errorf("inspect upload session root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
		_ = rootDir.Close()
		return nil, errors.New("upload session root must be a 0700 directory")
	}
	if err := rootDir.Close(); err != nil {
		return nil, fmt.Errorf("close upload session root: %w", err)
	}

	lockPath := filepath.Join(root, lockFileName)
	lockFile, err := rootio.OpenFilePathNoFollow(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open upload session store lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("set upload session lock permissions: %w", err)
	}
	lockInfo, err := lockFile.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0o600 {
		_ = lockFile.Close()
		return nil, errors.Join(errors.New("upload session lock must be a 0600 regular file"), err)
	}
	if err := lockFile.Sync(); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("sync upload session store lock: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("sync upload session store root: %w", err)
	}
	if err := lockStoreFile(lockFile); err != nil {
		_ = lockFile.Close()
		return nil, err
	}

	lifetimeContext, lifetimeCancel := context.WithCancel(context.Background())
	store := &Store{
		rootPath:        root,
		rootInfo:        rootInfo,
		lockFile:        lockFile,
		opts:            options,
		closeDone:       make(chan struct{}),
		lifetimeContext: lifetimeContext,
		lifetimeCancel:  lifetimeCancel,
		openPayloads:    make(map[*payloadReader]struct{}),
		stagedByOwner:   make(map[string]int64),
		stagedBySession: make(map[string]int64),
		sessions:        make(map[string]Session),
		requestIndex:    make(map[requestKey]string),
	}
	if err := store.recoverAll(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func normalizeOptions(options *Options) error {
	if options == nil {
		return errors.New("upload session options are unavailable")
	}
	if options.TTL == 0 {
		options.TTL = defaultTTL
	}
	if options.MaxChunkBytes == 0 {
		options.MaxChunkBytes = defaultMaxChunkBytes
	}
	if options.MaxSessionsPerOwner == 0 {
		options.MaxSessionsPerOwner = defaultMaxPerOwner
	}
	if options.MaxSessions == 0 {
		options.MaxSessions = defaultMaxSessions
	}
	if options.MaxStagedBytesPerOwner == 0 {
		options.MaxStagedBytesPerOwner = defaultMaxStagedBytesPerOwner
	}
	if options.MaxStagedBytes == 0 {
		options.MaxStagedBytes = defaultMaxStagedBytes
	}
	if options.CloseTimeout == 0 {
		options.CloseTimeout = defaultCloseTimeout
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.TTL <= 0 || options.MaxChunkBytes <= 0 ||
		options.MaxSessionsPerOwner <= 0 || options.MaxSessions <= 0 ||
		options.MaxSessionsPerOwner > options.MaxSessions ||
		options.MaxStagedBytesPerOwner <= 0 || options.MaxStagedBytes <= 0 ||
		options.MaxStagedBytesPerOwner > options.MaxStagedBytes ||
		options.CloseTimeout <= 0 {
		return errors.New("invalid upload session options")
	}
	return nil
}

// Close stops accepting operations, waits for admitted operations, and then
// releases the exclusive store lock.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.gateMu.Lock()
	if s.closeDone == nil {
		s.closeDone = make(chan struct{})
	}
	done := s.closeDone
	if !s.closing {
		s.closing = true
		cancel := s.lifetimeCancel
		tracked := make([]*payloadReader, 0, len(s.openPayloads))
		for reader := range s.openPayloads {
			tracked = append(tracked, reader)
		}
		s.gateMu.Unlock()

		if cancel != nil {
			cancel()
		}
		go s.closePayloadsAndFinalize(tracked)
	} else {
		s.gateMu.Unlock()
	}

	timer := time.NewTimer(s.opts.CloseTimeout)
	defer timer.Stop()
	select {
	case <-done:
		s.gateMu.Lock()
		err := s.closeErr
		s.gateMu.Unlock()
		return err
	case <-timer.C:
		select {
		case <-done:
			s.gateMu.Lock()
			err := s.closeErr
			s.gateMu.Unlock()
			return err
		default:
		}
		return fmt.Errorf("%w after %s", ErrCloseTimeout, s.opts.CloseTimeout)
	}
}

func (s *Store) closePayloadsAndFinalize(readers []*payloadReader) {
	var resultErr error
	for _, reader := range readers {
		if err := reader.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close upload payload reader: %w", err),
			)
		}
	}
	s.finalizeClose(resultErr)
}

func (s *Store) finalizeClose(initialErr error) {
	s.activeOps.Wait()
	s.mu.Lock()
	s.closed = true
	lockFile := s.lockFile
	s.lockFile = nil
	s.mu.Unlock()

	unlockErr := unlockStoreFile(lockFile)
	closeErr := error(nil)
	if lockFile != nil {
		closeErr = lockFile.Close()
	}
	result := errors.Join(initialErr, unlockErr, closeErr)

	s.gateMu.Lock()
	s.closeErr = result
	close(s.closeDone)
	s.gateMu.Unlock()
}

func (s *Store) admitOperation(ctx context.Context) (context.Context, func(), error) {
	if s == nil {
		return nil, nil, ErrClosed
	}
	if err := ctxError(ctx); err != nil {
		return nil, nil, err
	}
	s.gateMu.Lock()
	if s.closing {
		s.gateMu.Unlock()
		return nil, nil, ErrClosed
	}
	s.activeOps.Add(1)
	lifetimeContext := s.lifetimeContext
	s.gateMu.Unlock()
	if lifetimeContext == nil {
		lifetimeContext = context.Background()
	}

	operationContext, cancel := context.WithCancel(ctx)
	stopLifetime := context.AfterFunc(lifetimeContext, cancel)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			stopLifetime()
			cancel()
			s.activeOps.Done()
		})
	}
	return operationContext, release, nil
}

func (s *Store) trackOpenPayload(file *os.File) (*payloadReader, bool) {
	if file == nil {
		return nil, false
	}
	reader := &payloadReader{store: s, file: file}
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	if s.closing {
		return nil, false
	}
	if s.openPayloads == nil {
		s.openPayloads = make(map[*payloadReader]struct{})
	}
	s.activeOps.Add(1)
	s.openPayloads[reader] = struct{}{}
	return reader, true
}

func (r *payloadReader) Read(buffer []byte) (int, error) {
	if r == nil || r.file == nil {
		return 0, os.ErrClosed
	}
	return r.file.Read(buffer)
}

func (r *payloadReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.file != nil {
			r.closeErr = r.file.Close()
		}
		if r.store != nil {
			r.store.releaseOpenPayload(r)
		}
	})
	return r.closeErr
}

func (s *Store) releaseOpenPayload(reader *payloadReader) {
	if s == nil || reader == nil {
		return
	}
	s.gateMu.Lock()
	_, tracked := s.openPayloads[reader]
	if tracked {
		delete(s.openPayloads, reader)
	}
	s.gateMu.Unlock()
	if tracked {
		s.activeOps.Done()
	}
}

// Create creates a session or returns the owner-scoped idempotent replay.
func (s *Store) Create(ctx context.Context, request CreateRequest) (CreateResult, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer release()
	if err := validateCreateRequest(request); err != nil {
		return CreateResult{}, err
	}
	if request.TotalBytes > s.opts.MaxStagedBytesPerOwner ||
		request.TotalBytes > s.opts.MaxStagedBytes {
		return CreateResult{}, ErrStagingLimit
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()
	if err := ctxError(operationContext); err != nil {
		return CreateResult{}, err
	}
	if err := s.validateRoot(); err != nil {
		return CreateResult{}, err
	}

	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return CreateResult{}, ErrClosed
	}
	key := requestKey{owner: request.Owner, clientRequestID: request.ClientRequestID}
	if existingID := s.requestIndex[key]; existingID != "" {
		existing := s.sessions[existingID]
		s.mu.RUnlock()
		if existing.Path != request.Path ||
			existing.TotalBytes != request.TotalBytes {
			return CreateResult{}, fmt.Errorf("%w: client_request_id already belongs to different immutable input", ErrConflict)
		}
		if sessionExpired(existing.State, existing.ExpiresAt, s.now()) {
			return CreateResult{}, ErrExpired
		}
		return CreateResult{Session: existing, Created: false}, nil
	}
	global, owner := s.sessionCountsLocked(request.Owner)
	s.mu.RUnlock()
	if global >= s.opts.MaxSessions || owner >= s.opts.MaxSessionsPerOwner {
		return CreateResult{}, ErrLimitExceeded
	}

	var (
		id         string
		sessionDir string
	)
	for attempt := 0; attempt < 32; attempt++ {
		candidate, err := s.newID()
		if err != nil {
			return CreateResult{}, err
		}
		s.mu.RLock()
		_, known := s.sessions[candidate]
		s.mu.RUnlock()
		if known {
			continue
		}
		candidateDir := s.sessionDir(candidate)
		if err := rootio.MkdirPathNoFollow(candidateDir, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return CreateResult{}, fmt.Errorf("create upload session directory: %w", err)
		}
		id = candidate
		sessionDir = candidateDir
		break
	}
	if id == "" {
		return CreateResult{}, errors.New("could not allocate a unique upload session id")
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = rootio.RemoveAllPathNoFollow(sessionDir)
			_ = s.syncRoot()
		}
	}()
	dir, err := rootio.OpenDirPathNoFollow(sessionDir)
	if err != nil {
		return CreateResult{}, fmt.Errorf("open upload session directory: %w", err)
	}
	if err := dir.Chmod(0o700); err != nil {
		_ = dir.Close()
		return CreateResult{}, fmt.Errorf("set upload session directory permissions: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return CreateResult{}, fmt.Errorf("sync upload session directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return CreateResult{}, fmt.Errorf("close upload session directory: %w", err)
	}
	if err := s.syncRoot(); err != nil {
		return CreateResult{}, err
	}

	payload, err := rootio.OpenFilePathNoFollow(
		filepath.Join(sessionDir, payloadFileName),
		os.O_CREATE|os.O_EXCL|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return CreateResult{}, fmt.Errorf("create upload session payload: %w", err)
	}
	if err := payload.Chmod(0o600); err != nil {
		_ = payload.Close()
		return CreateResult{}, fmt.Errorf("set upload payload permissions: %w", err)
	}
	if err := payload.Sync(); err != nil {
		_ = payload.Close()
		return CreateResult{}, fmt.Errorf("sync upload session payload: %w", err)
	}
	payloadInfo, err := payload.Stat()
	closeErr := payload.Close()
	if err != nil || closeErr != nil {
		return CreateResult{}, errors.Join(fmt.Errorf("inspect upload session payload: %w", err), closeErr)
	}
	payloadIdentity := workspace.PersistentIdentityTokenForFileInfo(payloadInfo)
	if payloadIdentity == "" {
		return CreateResult{}, fmt.Errorf("%w: payload persistent identity is unavailable", ErrRecoveryRequired)
	}

	now := s.now()
	initialState := StateUploading
	contentBLAKE3 := ""
	if request.TotalBytes == 0 {
		initialState = StateReady
		contentBLAKE3 = emptyBLAKE3()
	}
	record := diskState{
		SchemaVersion:     stateSchemaVersion,
		ID:                id,
		Owner:             request.Owner,
		ClientRequestID:   request.ClientRequestID,
		Path:              request.Path,
		TotalBytes:        request.TotalBytes,
		OriginalCondition: request.OriginalCondition,
		State:             initialState,
		Revision:          1,
		PayloadIdentity:   payloadIdentity,
		LastChunkOffset:   -1,
		ContentBLAKE3:     contentBLAKE3,
		CreatedAt:         now,
		UpdatedAt:         now,
		ExpiresAt:         now.Add(s.opts.TTL),
	}
	_, err = s.writeInitialState(record)
	if err != nil {
		return CreateResult{}, err
	}
	cleanup = false
	session := publicSession(record)
	s.publishSnapshot(record)
	return CreateResult{Session: session, Created: true}, nil
}

// Get returns the current owner-bound durable snapshot.
func (s *Store) Get(ctx context.Context, owner, id string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	return s.get(ctx, owner, id)
}

func (s *Store) get(ctx context.Context, owner, id string) (Session, error) {
	if err := ctxError(ctx); err != nil {
		return Session{}, err
	}
	lock := s.sessionLock(id)
	lock.Lock()
	defer lock.Unlock()
	loaded, err := s.loadOwnedSession(owner, id)
	if err != nil {
		return Session{}, err
	}
	s.publishSnapshot(loaded.state)
	return publicSession(loaded.state), nil
}

// GetByClientRequestID returns the owner-scoped idempotency-key mapping.
func (s *Store) GetByClientRequestID(
	ctx context.Context,
	owner, clientRequestID string,
) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	if !validOwner(owner) || !validClientRequestID(clientRequestID) {
		return Session{}, ErrNotFound
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return Session{}, ErrClosed
	}
	id := s.requestIndex[requestKey{owner: owner, clientRequestID: clientRequestID}]
	s.mu.RUnlock()
	if id == "" {
		return Session{}, ErrNotFound
	}
	return s.get(ctx, owner, id)
}

// AppendChunk appends one sequential chunk or verifies the immediately previous
// chunk as an idempotent replay.
func (s *Store) AppendChunk(
	ctx context.Context,
	request AppendChunkRequest,
) (result AppendResult, resultErr error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return AppendResult{}, err
	}
	defer release()
	ctx = operationContext
	if !validID(request.ID) || !validOwner(request.Owner) {
		return AppendResult{}, ErrNotFound
	}
	if request.Length <= 0 {
		return AppendResult{}, ErrChunkLength
	}
	if request.Length > s.opts.MaxChunkBytes {
		return AppendResult{}, ErrChunkTooLarge
	}
	if !validChunkID(request.ChunkID) {
		return AppendResult{}, errors.New("invalid upload chunk ID")
	}
	if !validLowerHex(request.ChunkSHA256, sha256.Size*2) {
		return AppendResult{}, ErrChunkDigest
	}
	if request.Body == nil {
		return AppendResult{}, ErrChunkLength
	}

	lock := s.sessionLock(request.ID)
	lock.Lock()
	defer lock.Unlock()
	loaded, err := s.loadOwnedSession(request.Owner, request.ID)
	if err != nil {
		return AppendResult{}, err
	}
	record := loaded.state
	if record.LastChunkID == request.ChunkID &&
		(record.LastChunkOffset != request.Offset ||
			record.LastChunkBytes != request.Length ||
			record.LastChunkSHA256 != request.ChunkSHA256) {
		return AppendResult{}, fmt.Errorf(
			"%w: chunk ID already belongs to different immutable input",
			ErrConflict,
		)
	}
	if record.State != StateUploading {
		if record.LastChunkOffset == request.Offset &&
			record.LastChunkBytes == request.Length &&
			record.LastChunkID == request.ChunkID &&
			record.LastChunkSHA256 == request.ChunkSHA256 {
			if err := s.verifyReplayBody(ctx, record, request); err != nil {
				return AppendResult{}, err
			}
			return AppendResult{Session: publicSession(record), Replayed: true}, nil
		}
		return AppendResult{}, ErrInvalidState
	}
	if request.Offset != record.DurableOffset {
		if record.LastChunkOffset == request.Offset &&
			record.LastChunkBytes == request.Length &&
			record.LastChunkID == request.ChunkID &&
			record.LastChunkSHA256 == request.ChunkSHA256 &&
			request.Offset+request.Length == record.DurableOffset {
			if err := s.verifyReplayBody(ctx, record, request); err != nil {
				return AppendResult{}, err
			}
			return AppendResult{Session: publicSession(record), Replayed: true}, nil
		}
		return AppendResult{}, &OffsetMismatchError{Expected: record.DurableOffset, Actual: request.Offset}
	}
	if request.Length > record.TotalBytes-record.DurableOffset {
		return AppendResult{}, ErrChunkLength
	}
	if err := s.reserveStagedBytes(
		ctx,
		record.ID,
		record.Owner,
		record.DurableOffset,
		request.Length,
	); err != nil {
		return AppendResult{}, err
	}
	reservationCommitted := false
	defer func() {
		var err error
		if reservationCommitted {
			err = s.finishStagedReservation(request.Length)
		} else {
			err = s.releaseStagedReservation(
				record.ID,
				record.Owner,
				request.Length,
			)
		}
		if err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()

	payload, err := s.openVerifiedPayload(record, os.O_RDWR)
	if err != nil {
		return AppendResult{}, err
	}
	defer payload.Close()
	if _, err := payload.Seek(record.DurableOffset, io.SeekStart); err != nil {
		return AppendResult{}, fmt.Errorf("seek upload payload: %w", err)
	}
	hasher := sha256.New()
	reader := &contextReader{ctx: ctx, reader: request.Body}
	written, copyErr := io.CopyN(io.MultiWriter(payload, hasher), reader, request.Length)
	if errors.Is(copyErr, io.EOF) || errors.Is(copyErr, io.ErrUnexpectedEOF) {
		copyErr = errors.Join(ErrChunkLength, copyErr)
	}
	if copyErr == nil {
		var extra [1]byte
		n, extraErr := request.Body.Read(extra[:])
		if n != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
			copyErr = ErrChunkLength
		}
	}
	if copyErr == nil && written != request.Length {
		copyErr = ErrChunkLength
	}
	actualDigest := hex.EncodeToString(hasher.Sum(nil))
	if copyErr == nil && actualDigest != request.ChunkSHA256 {
		copyErr = ErrChunkDigest
	}
	if copyErr != nil {
		rollbackErr := rollbackPayload(payload, record.DurableOffset)
		return AppendResult{}, errors.Join(copyErr, rollbackErr)
	}
	if err := payload.Sync(); err != nil {
		rollbackErr := rollbackPayload(payload, record.DurableOffset)
		return AppendResult{}, errors.Join(fmt.Errorf("sync upload payload: %w", err), rollbackErr)
	}
	after, err := payload.Stat()
	if err != nil ||
		workspace.PersistentIdentityTokenForFileInfo(after) != record.PayloadIdentity ||
		after.Size() != record.DurableOffset+request.Length ||
		!after.Mode().IsRegular() || after.Mode().Perm() != 0o600 {
		return AppendResult{}, errors.Join(
			fmt.Errorf("%w: upload payload changed after append", ErrRecoveryRequired),
			err,
		)
	}

	next := record
	next.DurableOffset += request.Length
	next.LastChunkOffset = request.Offset
	next.LastChunkBytes = request.Length
	next.LastChunkID = request.ChunkID
	next.LastChunkSHA256 = request.ChunkSHA256
	next.UpdatedAt = s.now()
	next.ExpiresAt = next.UpdatedAt.Add(s.opts.TTL)
	if next.DurableOffset == next.TotalBytes {
		contentHash, hashErr := hashOpenPayload(ctx, payload, next)
		if hashErr != nil {
			return AppendResult{}, hashErr
		}
		next.State = StateReady
		next.ContentBLAKE3 = contentHash
	}
	persisted, err := s.writeNextState(loaded, next)
	if err != nil {
		recovered, recoveryErr := s.loadSession(record.ID)
		if recoveryErr == nil &&
			recovered.state.DurableOffset == next.DurableOffset &&
			recovered.state.LastChunkID == request.ChunkID &&
			recovered.state.LastChunkSHA256 == request.ChunkSHA256 {
			reservationCommitted = true
			s.publishSnapshot(recovered.state)
		}
		return AppendResult{}, errors.Join(err, recoveryErr)
	}
	reservationCommitted = true
	s.publishSnapshot(persisted.state)
	return AppendResult{Session: publicSession(persisted.state)}, nil
}

// OpenPayload returns a verified read handle for a ready or committing session.
func (s *Store) OpenPayload(ctx context.Context, owner, id string) (io.ReadCloser, Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return nil, Session{}, err
	}
	defer release()
	ctx = operationContext
	lock := s.sessionLock(id)
	lock.Lock()
	defer lock.Unlock()
	loaded, err := s.loadOwnedSession(owner, id)
	if err != nil {
		return nil, Session{}, err
	}
	if loaded.state.State != StateReady && loaded.state.State != StateCommitting {
		return nil, Session{}, ErrInvalidState
	}
	payload, err := s.openVerifiedPayload(loaded.state, os.O_RDONLY)
	if err != nil {
		return nil, Session{}, err
	}
	hash, err := hashOpenPayload(ctx, payload, loaded.state)
	if err != nil {
		_ = payload.Close()
		return nil, Session{}, err
	}
	if hash != loaded.state.ContentBLAKE3 {
		_ = payload.Close()
		return nil, Session{}, fmt.Errorf("%w: upload payload digest changed", ErrRecoveryRequired)
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		_ = payload.Close()
		return nil, Session{}, fmt.Errorf("rewind upload payload: %w", err)
	}
	reader, tracked := s.trackOpenPayload(payload)
	if !tracked {
		_ = payload.Close()
		return nil, Session{}, ErrClosed
	}
	return reader, publicSession(loaded.state), nil
}

// BeginCommit durably enters committing. Repeated calls are idempotent.
func (s *Store) BeginCommit(ctx context.Context, owner, id string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	return s.transition(ctx, owner, id, func(record diskState) (diskState, bool, error) {
		switch record.State {
		case StateCommitting:
			return record, false, nil
		case StateReady:
			record.State = StateCommitting
			record.PublicationStarted = false
			return record, true, nil
		default:
			return diskState{}, false, ErrInvalidState
		}
	})
}

// MarkPublicationStarted records that target publication may have begun.
// Repeated calls while committing are idempotent.
func (s *Store) MarkPublicationStarted(ctx context.Context, owner, id string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	return s.transition(ctx, owner, id, func(record diskState) (diskState, bool, error) {
		if record.State != StateCommitting {
			return diskState{}, false, ErrInvalidState
		}
		if record.PublicationStarted {
			return record, false, nil
		}
		record.PublicationStarted = true
		return record, true, nil
	})
}

// MarkReady moves an interrupted pre-publication commit back to ready.
func (s *Store) MarkReady(ctx context.Context, owner, id string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	return s.transition(ctx, owner, id, func(record diskState) (diskState, bool, error) {
		switch record.State {
		case StateReady:
			return record, false, nil
		case StateCommitting:
			record.State = StateReady
			record.PublicationStarted = false
			return record, true, nil
		default:
			return diskState{}, false, ErrInvalidState
		}
	})
}

// MarkCommitted records a confirmed commit. Repeated matching calls are
// idempotent.
func (s *Store) MarkCommitted(ctx context.Context, owner, id string, result CommitResult) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	if !validLowerHex(result.ContentBLAKE3, blake3HexLength) {
		return Session{}, errors.New("invalid committed payload BLAKE3")
	}
	return s.transition(ctx, owner, id, func(record diskState) (diskState, bool, error) {
		if result.ContentBLAKE3 != record.ContentBLAKE3 {
			return diskState{}, false, fmt.Errorf("%w: committed target digest differs from upload payload", ErrConflict)
		}
		switch record.State {
		case StateCommitted:
			if record.PersistenceWarning != result.PersistenceWarning {
				return diskState{}, false, fmt.Errorf("%w: committed result differs", ErrConflict)
			}
			return record, false, nil
		case StateCommitting:
			record.State = StateCommitted
			record.PersistenceWarning = result.PersistenceWarning
			return record, true, nil
		default:
			return diskState{}, false, ErrInvalidState
		}
	})
}

// MarkConflict records a terminal commit conflict.
func (s *Store) MarkConflict(ctx context.Context, owner, id, reason string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ctx = operationContext
	reason = strings.TrimSpace(reason)
	if !validCleanText(reason, 1024) {
		return Session{}, errors.New("invalid upload conflict reason")
	}
	return s.transition(ctx, owner, id, func(record diskState) (diskState, bool, error) {
		switch record.State {
		case StateConflict:
			if record.ConflictReason != reason {
				return diskState{}, false, fmt.Errorf("%w: conflict result differs", ErrConflict)
			}
			return record, false, nil
		case StateReady, StateCommitting:
			record.State = StateConflict
			record.ConflictReason = reason
			return record, true, nil
		default:
			return diskState{}, false, ErrInvalidState
		}
	})
}

// Cancel durably records a cancellation and removes its staged payload.
func (s *Store) Cancel(ctx context.Context, owner, id string) (Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
	lock := s.sessionLock(id)
	lock.Lock()
	defer lock.Unlock()
	if err := ctxError(operationContext); err != nil {
		return Session{}, err
	}
	loaded, err := s.loadOwnedSession(owner, id)
	if err != nil {
		return Session{}, err
	}
	switch loaded.state.State {
	case StateCancelled:
		if err := s.removeTerminalPayload(loaded.state); err != nil {
			return Session{}, err
		}
		s.publishSnapshot(loaded.state)
		return publicSession(loaded.state), nil
	case StateCommitting, StateCommitted, StateConflict:
		return Session{}, ErrInvalidState
	}
	next := loaded.state
	next.State = StateCancelled
	now := s.now()
	next.UpdatedAt = now
	next.ExpiresAt = now.Add(s.opts.TTL)
	persisted, err := s.writeNextState(loaded, next)
	if err != nil {
		return Session{}, err
	}
	s.publishSnapshot(persisted.state)
	if err := s.removeTerminalPayload(persisted.state); err != nil {
		return Session{}, err
	}
	return publicSession(persisted.state), nil
}

// CleanupExpired removes expired sessions except those with an unresolved
// committing decision.
func (s *Store) CleanupExpired(ctx context.Context, now time.Time) (int, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return 0, err
	}
	defer release()
	ctx = operationContext
	now = now.UTC()
	s.mu.RLock()
	ids := make([]string, 0, len(s.sessions))
	for id, session := range s.sessions {
		if session.State != StateCommitting && !session.ExpiresAt.After(now) {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()

	cleaned := 0
	var resultErr error
	for _, id := range ids {
		if err := ctxError(ctx); err != nil {
			return cleaned, errors.Join(resultErr, err)
		}
		lock := s.sessionLock(id)
		lock.Lock()
		loaded, err := s.loadSession(id)
		if err == nil && loaded.state.State != StateCommitting && !loaded.state.ExpiresAt.After(now) {
			session := publicSession(loaded.state)
			err = s.removeSessionDir(id)
			if err == nil {
				err = s.releaseSessionStaging(session.ID, session.Owner)
				s.removeSnapshot(session)
				cleaned++
			}
		}
		lock.Unlock()
		if err != nil && !errors.Is(err, ErrNotFound) {
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup expired upload session %s: %w", id, err))
		}
	}
	return cleaned, resultErr
}

// ListCommitting returns a durable snapshot of sessions whose publication
// decision is unresolved. Committing sessions remain visible after expiry.
func (s *Store) ListCommitting(ctx context.Context) ([]Session, error) {
	operationContext, release, err := s.admitOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx = operationContext

	s.mu.RLock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	sort.Strings(ids)

	result := make([]Session, 0)
	for _, id := range ids {
		if err := ctxError(ctx); err != nil {
			return nil, err
		}
		lock := s.sessionLock(id)
		lock.Lock()
		loaded, err := s.loadSession(id)
		lock.Unlock()
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list committing upload session %s: %w", id, err)
		}
		if loaded.state.State == StateCommitting {
			result = append(result, publicSession(loaded.state))
		}
	}
	return result, nil
}

func (s *Store) transition(
	ctx context.Context,
	owner, id string,
	change func(diskState) (diskState, bool, error),
) (Session, error) {
	if err := ctxError(ctx); err != nil {
		return Session{}, err
	}
	lock := s.sessionLock(id)
	lock.Lock()
	defer lock.Unlock()
	loaded, err := s.loadOwnedSession(owner, id)
	if err != nil {
		return Session{}, err
	}
	next, changed, err := change(loaded.state)
	if err != nil {
		return Session{}, err
	}
	if !changed {
		if isTerminalState(loaded.state.State) {
			if err := s.removeTerminalPayload(loaded.state); err != nil {
				return Session{}, err
			}
		}
		return publicSession(loaded.state), nil
	}
	now := s.now()
	next.UpdatedAt = now
	next.ExpiresAt = now.Add(s.opts.TTL)
	persisted, err := s.writeNextState(loaded, next)
	if err != nil {
		return Session{}, err
	}
	s.publishSnapshot(persisted.state)
	if isTerminalState(persisted.state.State) {
		if err := s.removeTerminalPayload(persisted.state); err != nil {
			return Session{}, err
		}
	}
	return publicSession(persisted.state), nil
}

func (s *Store) verifyReplayBody(ctx context.Context, record diskState, request AppendChunkRequest) error {
	if request.Offset < 0 || request.Length <= 0 ||
		request.Offset+request.Length > record.DurableOffset {
		return ErrOffsetMismatch
	}
	payload, err := s.openVerifiedPayload(record, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer payload.Close()
	storedHasher := sha256.New()
	if _, err := io.CopyN(storedHasher, io.NewSectionReader(payload, request.Offset, request.Length), request.Length); err != nil {
		return fmt.Errorf("%w: hash durable replay range: %v", ErrRecoveryRequired, err)
	}
	if hex.EncodeToString(storedHasher.Sum(nil)) != request.ChunkSHA256 {
		return fmt.Errorf("%w: durable replay range changed", ErrRecoveryRequired)
	}
	incomingHasher := sha256.New()
	reader := &contextReader{ctx: ctx, reader: request.Body}
	written, err := io.CopyN(incomingHasher, reader, request.Length)
	if err != nil || written != request.Length {
		return errors.Join(ErrChunkLength, err)
	}
	var extra [1]byte
	n, extraErr := request.Body.Read(extra[:])
	if n != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
		return errors.Join(ErrChunkLength, extraErr)
	}
	if hex.EncodeToString(incomingHasher.Sum(nil)) != request.ChunkSHA256 {
		return ErrChunkDigest
	}
	return nil
}

func (s *Store) recoverAll(ctx context.Context) error {
	if err := s.validateRoot(); err != nil {
		return err
	}
	rootDir, err := rootio.OpenDirPathNoFollow(s.rootPath)
	if err != nil {
		return err
	}
	entries, err := rootDir.ReadDir(-1)
	closeErr := rootDir.Close()
	if err != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("read upload session root: %w", err), closeErr)
	}
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			return err
		}
		if entry.Name() == lockFileName {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: invalid upload session lock entry", ErrRecoveryRequired)
			}
			continue
		}
		if !validID(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: unexpected upload session root entry %q", ErrRecoveryRequired, entry.Name())
		}
		loaded, loadErr := s.loadSession(entry.Name())
		if errors.Is(loadErr, errIncompleteCreate) {
			if removeErr := s.removeSessionDirUnchecked(entry.Name()); removeErr != nil {
				return errors.Join(loadErr, removeErr)
			}
			continue
		}
		if loadErr != nil {
			return fmt.Errorf("recover upload session %s: %w", entry.Name(), loadErr)
		}
		session := publicSession(loaded.state)
		if !isTerminalState(session.State) {
			if err := s.restoreStagedBytes(session); err != nil {
				return fmt.Errorf("recover upload session %s staging usage: %w", entry.Name(), err)
			}
		}
		key := requestKey{owner: session.Owner, clientRequestID: session.ClientRequestID}
		if other := s.requestIndex[key]; other != "" && other != session.ID {
			return fmt.Errorf("%w: duplicate owner-scoped client_request_id", ErrRecoveryRequired)
		}
		s.sessions[session.ID] = session
		s.requestIndex[key] = session.ID
	}
	return nil
}

func (s *Store) loadOwnedSession(owner, id string) (loadedSession, error) {
	if !validID(id) || !validOwner(owner) {
		return loadedSession{}, ErrNotFound
	}
	s.mu.RLock()
	closed := s.closed
	known := s.sessions[id]
	s.mu.RUnlock()
	if closed {
		return loadedSession{}, ErrClosed
	}
	if known.ID == "" || known.Owner != owner {
		return loadedSession{}, ErrNotFound
	}
	loaded, err := s.loadSession(id)
	if errors.Is(err, os.ErrNotExist) {
		return loadedSession{}, ErrNotFound
	}
	if err != nil {
		return loadedSession{}, err
	}
	if loaded.state.Owner != owner {
		return loadedSession{}, ErrNotFound
	}
	if sessionExpired(loaded.state.State, loaded.state.ExpiresAt, s.now()) {
		return loadedSession{}, ErrExpired
	}
	return loaded, nil
}

func (s *Store) loadSession(id string) (loadedSession, error) {
	if !validID(id) {
		return loadedSession{}, ErrNotFound
	}
	if err := s.validateRoot(); err != nil {
		return loadedSession{}, err
	}
	dirPath := s.sessionDir(id)
	dir, err := rootio.OpenDirPathNoFollow(dirPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return loadedSession{}, ErrNotFound
		}
		return loadedSession{}, fmt.Errorf("%w: open upload session directory: %v", ErrRecoveryRequired, err)
	}
	info, err := dir.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		_ = dir.Close()
		return loadedSession{}, errors.Join(
			fmt.Errorf("%w: upload session directory is not 0700", ErrRecoveryRequired),
			err,
		)
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil || closeErr != nil {
		return loadedSession{}, errors.Join(fmt.Errorf("read upload session directory: %w", err), closeErr)
	}
	if err := s.recoverPendingStates(id, entries); err != nil {
		return loadedSession{}, err
	}
	dir, err = rootio.OpenDirPathNoFollow(dirPath)
	if err != nil {
		return loadedSession{}, err
	}
	entries, err = dir.ReadDir(-1)
	closeErr = dir.Close()
	if err != nil || closeErr != nil {
		return loadedSession{}, errors.Join(err, closeErr)
	}

	stateFiles := make(map[uint64]string)
	payloadSeen := false
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case name == payloadFileName:
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return loadedSession{}, fmt.Errorf("%w: upload payload is not a regular file", ErrRecoveryRequired)
			}
			payloadSeen = true
		case isPendingStateName(name):
			return loadedSession{}, fmt.Errorf("%w: upload state pending recovery was not resolved", ErrRecoveryRequired)
		default:
			revision, ok := parseStateFileName(name)
			if !ok || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return loadedSession{}, fmt.Errorf("%w: unexpected upload session entry %q", ErrRecoveryRequired, name)
			}
			if stateFiles[revision] != "" {
				return loadedSession{}, fmt.Errorf("%w: duplicate upload state revision", ErrRecoveryRequired)
			}
			stateFiles[revision] = name
		}
	}
	if len(stateFiles) == 0 {
		if !payloadSeen || len(entries) == 0 {
			return loadedSession{}, errIncompleteCreate
		}
		return loadedSession{}, errIncompleteCreate
	}

	firstRevision := ^uint64(0)
	var lastRevision uint64
	for revision := range stateFiles {
		if revision < firstRevision {
			firstRevision = revision
		}
		if revision > lastRevision {
			lastRevision = revision
		}
	}
	if lastRevision-firstRevision+1 != uint64(len(stateFiles)) {
		return loadedSession{}, fmt.Errorf("%w: upload state revision gap", ErrRecoveryRequired)
	}

	var previousRaw []byte
	var latest loadedSession
	for revision := firstRevision; ; revision++ {
		name := stateFiles[revision]
		if name == "" {
			return loadedSession{}, fmt.Errorf("%w: upload state revision gap", ErrRecoveryRequired)
		}
		raw, record, err := readStateFile(filepath.Join(dirPath, name))
		if err != nil {
			return loadedSession{}, err
		}
		if record.ID != id || record.Revision != revision {
			return loadedSession{}, fmt.Errorf("%w: upload state identity mismatch", ErrRecoveryRequired)
		}
		if err := validateDiskState(record); err != nil {
			return loadedSession{}, fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
		}
		if revision == firstRevision {
			// A compacted tail cannot verify a removed predecessor's digest,
			// but validateDiskState still binds revisions greater than one.
			if revision == 1 &&
				(record.PreviousRevision != 0 || record.PreviousStateSHA256 != "") {
				return loadedSession{}, fmt.Errorf("%w: invalid initial upload state chain", ErrRecoveryRequired)
			}
		} else {
			if record.PreviousRevision != revision-1 ||
				record.PreviousStateSHA256 != digestBytes(previousRaw) {
				return loadedSession{}, fmt.Errorf("%w: upload state chain mismatch", ErrRecoveryRequired)
			}
		}
		previousRaw = raw
		latest = loadedSession{state: record, raw: raw, digest: digestBytes(raw)}
		if revision == lastRevision {
			break
		}
	}
	if !isTerminalState(latest.state.State) && !payloadSeen {
		return loadedSession{}, fmt.Errorf("%w: active upload payload is missing", ErrRecoveryRequired)
	}
	if payloadSeen {
		if isTerminalState(latest.state.State) {
			if err := s.removeTerminalPayload(latest.state); err != nil {
				return loadedSession{}, err
			}
		} else if err := s.recoverPayload(latest.state); err != nil {
			return loadedSession{}, err
		}
	}
	keepFrom := latest.state.Revision
	if keepFrom > 1 {
		keepFrom--
	}
	if err := s.compactStateFiles(id, keepFrom); err != nil {
		return loadedSession{}, err
	}
	return latest, nil
}

func (s *Store) recoverPendingStates(id string, entries []os.DirEntry) error {
	dirPath := s.sessionDir(id)
	changed := false
	for _, entry := range entries {
		revision, ok := parsePendingStateName(entry.Name())
		if !ok {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: pending upload state is not a regular file", ErrRecoveryRequired)
		}
		pendingPath := filepath.Join(dirPath, entry.Name())
		raw, record, err := readStateFile(pendingPath)
		if err != nil {
			if removeErr := removeVerifiedRegularFile(pendingPath); removeErr != nil {
				return errors.Join(err, removeErr)
			}
			changed = true
			continue
		}
		if record.ID != id || record.Revision != revision {
			return fmt.Errorf("%w: pending upload state identity mismatch", ErrRecoveryRequired)
		}
		finalPath := filepath.Join(dirPath, stateFileName(revision))
		finalRaw, _, finalErr := readStateFile(finalPath)
		switch {
		case finalErr == nil:
			if !bytes.Equal(raw, finalRaw) {
				return fmt.Errorf("%w: competing upload state revisions", ErrRecoveryRequired)
			}
			if err := removeVerifiedRegularFile(pendingPath); err != nil {
				return err
			}
			changed = true
		case errors.Is(finalErr, os.ErrNotExist):
			if err := rootio.RenamePathIntoDirNoFollow(pendingPath, dirPath, stateFileName(revision)); err != nil {
				return fmt.Errorf("publish recovered upload state: %w", err)
			}
			changed = true
		default:
			return finalErr
		}
	}
	if changed {
		return syncDirectory(dirPath)
	}
	return nil
}

func (s *Store) recoverPayload(record diskState) error {
	payload, err := s.openPayloadFile(record, os.O_RDWR)
	if err != nil {
		return err
	}
	defer payload.Close()
	info, err := payload.Stat()
	if err != nil {
		return fmt.Errorf("%w: inspect upload payload: %v", ErrRecoveryRequired, err)
	}
	if info.Size() < record.DurableOffset {
		return fmt.Errorf(
			"%w: upload payload size %d is below durable offset %d",
			ErrRecoveryRequired,
			info.Size(),
			record.DurableOffset,
		)
	}
	if info.Size() > record.DurableOffset {
		if err := payload.Truncate(record.DurableOffset); err != nil {
			return fmt.Errorf("%w: truncate uncommitted upload bytes: %v", ErrRecoveryRequired, err)
		}
		if err := payload.Sync(); err != nil {
			return fmt.Errorf("%w: sync truncated upload payload: %v", ErrRecoveryRequired, err)
		}
		if err := syncDirectory(s.sessionDir(record.ID)); err != nil {
			return fmt.Errorf("%w: sync recovered upload session: %v", ErrRecoveryRequired, err)
		}
	}
	return nil
}

func (s *Store) openVerifiedPayload(record diskState, flag int) (*os.File, error) {
	payload, err := s.openPayloadFile(record, flag)
	if err != nil {
		return nil, err
	}
	info, err := payload.Stat()
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() != record.DurableOffset ||
		workspace.PersistentIdentityTokenForFileInfo(info) != record.PayloadIdentity {
		_ = payload.Close()
		return nil, errors.Join(
			fmt.Errorf("%w: upload payload evidence mismatch", ErrRecoveryRequired),
			err,
		)
	}
	return payload, nil
}

func (s *Store) openPayloadFile(record diskState, flag int) (*os.File, error) {
	payload, err := rootio.OpenFilePathNoFollow(
		filepath.Join(s.sessionDir(record.ID), payloadFileName),
		flag,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: open upload payload: %v", ErrRecoveryRequired, err)
	}
	return payload, nil
}

func (s *Store) writeInitialState(record diskState) ([]byte, error) {
	if record.Revision != 1 {
		return nil, errors.New("initial upload state revision must be one")
	}
	if err := validateDiskState(record); err != nil {
		return nil, fmt.Errorf("validate initial upload state: %w", err)
	}
	raw, err := encodeState(record)
	if err != nil {
		return nil, err
	}
	if err := s.publishStateFile(record.ID, record.Revision, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Store) writeNextState(current loadedSession, next diskState) (loadedSession, error) {
	next.Revision = current.state.Revision + 1
	next.PreviousRevision = current.state.Revision
	next.PreviousStateSHA256 = current.digest
	if err := validateDiskState(next); err != nil {
		return loadedSession{}, fmt.Errorf("validate next upload state: %w", err)
	}
	raw, err := encodeState(next)
	if err != nil {
		return loadedSession{}, err
	}
	if err := s.publishStateFile(next.ID, next.Revision, raw); err != nil {
		return loadedSession{}, err
	}
	if err := s.compactStateFiles(next.ID, current.state.Revision); err != nil {
		return loadedSession{}, err
	}
	return loadedSession{state: next, raw: raw, digest: digestBytes(raw)}, nil
}

func (s *Store) compactStateFiles(id string, keepFrom uint64) error {
	if !validID(id) || keepFrom == 0 {
		return ErrRecoveryRequired
	}
	dirPath := s.sessionDir(id)
	dir, err := rootio.OpenDirPathNoFollow(dirPath)
	if err != nil {
		return fmt.Errorf("open upload session for state compaction: %w", err)
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(
			fmt.Errorf("read upload session for state compaction: %w", readErr),
			closeErr,
		)
	}
	type stateFile struct {
		revision uint64
		name     string
	}
	stale := make([]stateFile, 0)
	for _, entry := range entries {
		revision, ok := parseStateFileName(entry.Name())
		if !ok || revision >= keepFrom {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: stale upload state is not a regular file", ErrRecoveryRequired)
		}
		stale = append(stale, stateFile{revision: revision, name: entry.Name()})
	}
	sort.Slice(stale, func(i, j int) bool {
		return stale[i].revision < stale[j].revision
	})
	for _, candidate := range stale {
		if err := removeVerifiedRegularFile(filepath.Join(dirPath, candidate.name)); err != nil {
			return fmt.Errorf("compact upload state revision %d: %w", candidate.revision, err)
		}
		// Persist each oldest-first removal so every crash-visible subset is a
		// consecutive tail of the state chain.
		if err := syncDirectory(dirPath); err != nil {
			return fmt.Errorf("sync compacted upload state revision %d: %w", candidate.revision, err)
		}
	}
	return nil
}

func (s *Store) publishStateFile(id string, revision uint64, raw []byte) error {
	nonce, err := s.randomHex(16)
	if err != nil {
		return err
	}
	dirPath := s.sessionDir(id)
	pendingName := pendingStateFileName(revision, nonce)
	pendingPath := filepath.Join(dirPath, pendingName)
	file, err := rootio.OpenFilePathNoFollow(
		pendingPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("create pending upload state: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = removeVerifiedRegularFile(pendingPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set upload state permissions: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("write upload state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync upload state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close upload state: %w", err)
	}
	if err := rootio.RenamePathIntoDirNoFollow(
		pendingPath,
		dirPath,
		stateFileName(revision),
	); err != nil {
		return fmt.Errorf("publish upload state: %w", err)
	}
	cleanup = false
	if err := syncDirectory(dirPath); err != nil {
		return fmt.Errorf("sync upload session state directory: %w", err)
	}
	return nil
}

func readStateFile(filePath string) ([]byte, diskState, error) {
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
	if err != nil {
		return nil, diskState{}, err
	}
	info, err := file.Stat()
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() < 0 ||
		info.Size() > maxStateFileBytes {
		_ = file.Close()
		return nil, diskState{}, errors.Join(errors.New("upload state must be a bounded 0600 regular file"), err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxStateFileBytes+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return nil, diskState{}, errors.Join(err, closeErr)
	}
	var record diskState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, diskState{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("upload state contains trailing JSON")
		}
		return nil, diskState{}, err
	}
	canonical, err := encodeState(record)
	if err != nil {
		return nil, diskState{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, diskState{}, errors.New("upload state JSON is not canonical")
	}
	return raw, record, nil
}

func encodeState(record diskState) ([]byte, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > maxStateFileBytes {
		return nil, errors.New("upload state exceeds its size bound")
	}
	return raw, nil
}

func validateDiskState(record diskState) error {
	if record.SchemaVersion != stateSchemaVersion || !validID(record.ID) ||
		!validOwner(record.Owner) || !validClientRequestID(record.ClientRequestID) ||
		!validLogicalPath(record.Path) || record.TotalBytes < 0 ||
		record.DurableOffset < 0 || record.DurableOffset > record.TotalBytes ||
		!validLowerHex(record.PayloadIdentity, sha256.Size*2) ||
		record.Revision == 0 ||
		!validCondition(record.OriginalCondition) ||
		record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() || record.ExpiresAt.IsZero() ||
		record.CreatedAt.Location() != time.UTC ||
		record.UpdatedAt.Location() != time.UTC ||
		record.ExpiresAt.Location() != time.UTC ||
		record.UpdatedAt.Before(record.CreatedAt) ||
		record.ExpiresAt.Before(record.UpdatedAt) {
		return errors.New("invalid upload state fields")
	}
	if !validState(record.State) {
		return errors.New("invalid upload state value")
	}
	if record.Revision == 1 {
		if record.PreviousRevision != 0 || record.PreviousStateSHA256 != "" {
			return errors.New("invalid initial upload state predecessor")
		}
	} else if record.PreviousRevision+1 != record.Revision ||
		!validLowerHex(record.PreviousStateSHA256, sha256.Size*2) {
		return errors.New("invalid upload state predecessor")
	}
	if record.LastChunkOffset == -1 {
		if record.LastChunkBytes != 0 || record.LastChunkID != "" ||
			record.LastChunkSHA256 != "" {
			return errors.New("invalid empty last chunk evidence")
		}
	} else if record.LastChunkOffset < 0 || record.LastChunkBytes <= 0 ||
		record.LastChunkOffset+record.LastChunkBytes != record.DurableOffset ||
		!validChunkID(record.LastChunkID) ||
		!validLowerHex(record.LastChunkSHA256, sha256.Size*2) {
		return errors.New("invalid last chunk evidence")
	}
	complete := record.DurableOffset == record.TotalBytes
	if complete != (record.ContentBLAKE3 != "") ||
		(record.ContentBLAKE3 != "" && !validLowerHex(record.ContentBLAKE3, blake3HexLength)) {
		return errors.New("invalid complete payload evidence")
	}
	if record.State == StateUploading && complete {
		return errors.New("complete upload cannot remain uploading")
	}
	if record.State != StateUploading && record.State != StateCancelled && !complete {
		return errors.New("non-uploading session must contain a complete payload")
	}
	if record.PersistenceWarning && record.State != StateCommitted {
		return errors.New("persistence warning requires committed state")
	}
	if record.PublicationStarted {
		switch record.State {
		case StateCommitting, StateCommitted, StateConflict:
		default:
			return errors.New("publication evidence requires committing or terminal commit state")
		}
	}
	if record.State == StateConflict {
		if !validCleanText(record.ConflictReason, 1024) {
			return errors.New("conflict state requires a reason")
		}
	} else if record.ConflictReason != "" {
		return errors.New("conflict reason requires conflict state")
	}
	return nil
}

func validateCreateRequest(request CreateRequest) error {
	if !validOwner(request.Owner) {
		return errors.New("invalid upload session owner")
	}
	if !validClientRequestID(request.ClientRequestID) {
		return errors.New("invalid upload client_request_id")
	}
	if !validLogicalPath(request.Path) {
		return errors.New("invalid upload target path")
	}
	if request.TotalBytes < 0 {
		return errors.New("invalid upload total byte count")
	}
	if !validCondition(request.OriginalCondition) {
		return errors.New("invalid upload original condition")
	}
	return nil
}

func validCondition(condition OriginalCondition) bool {
	if !condition.ExpectedExists {
		return condition.DeleteIdentityToken == ""
	}
	return validCleanText(condition.DeleteIdentityToken, 512)
}

func validOwner(owner string) bool {
	return validCleanText(owner, 256) && strings.TrimSpace(owner) == owner
}

func validClientRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("._:-", char):
		default:
			return false
		}
	}
	return true
}

func validChunkID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		isAlphaNumeric := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9'
		if isAlphaNumeric {
			continue
		}
		if index == 0 || !strings.ContainsRune("._:-", char) {
			return false
		}
	}
	return true
}

func validLogicalPath(value string) bool {
	if value == "" || value == "/" || len(value) > 4096 ||
		!strings.HasPrefix(value, "/") || strings.Contains(value, "\\") ||
		strings.ContainsAny(value, "?#") || path.Clean(value) != value ||
		!utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validCleanText(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validState(state State) bool {
	switch state {
	case StateUploading, StateReady, StateCommitting, StateCommitted, StateConflict, StateCancelled:
		return true
	default:
		return false
	}
}

func validID(id string) bool {
	return validLowerHex(id, 32)
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func publicSession(record diskState) Session {
	return Session{
		ID:                 record.ID,
		Owner:              record.Owner,
		ClientRequestID:    record.ClientRequestID,
		Path:               record.Path,
		TotalBytes:         record.TotalBytes,
		DurableOffset:      record.DurableOffset,
		OriginalCondition:  record.OriginalCondition,
		State:              record.State,
		Revision:           record.Revision,
		LastChunkOffset:    record.LastChunkOffset,
		LastChunkBytes:     record.LastChunkBytes,
		LastChunkID:        record.LastChunkID,
		LastChunkSHA256:    record.LastChunkSHA256,
		ContentBLAKE3:      record.ContentBLAKE3,
		PublicationStarted: record.PublicationStarted,
		PersistenceWarning: record.PersistenceWarning,
		ConflictReason:     record.ConflictReason,
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
		ExpiresAt:          record.ExpiresAt,
	}
}

func (s *Store) sessionCountsLocked(owner string) (global, ownerCount int) {
	for _, session := range s.sessions {
		global++
		if session.Owner == owner {
			ownerCount++
		}
	}
	return global, ownerCount
}

func (s *Store) restoreStagedBytes(session Session) error {
	if session.DurableOffset < 0 {
		return ErrRecoveryRequired
	}
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	if _, exists := s.stagedBySession[session.ID]; exists {
		return fmt.Errorf("%w: duplicate staged upload accounting", ErrRecoveryRequired)
	}
	ownerBytes := s.stagedByOwner[session.Owner]
	if session.DurableOffset > math.MaxInt64-s.stagedBytes ||
		session.DurableOffset > math.MaxInt64-ownerBytes {
		return fmt.Errorf("%w: staged upload accounting overflow", ErrRecoveryRequired)
	}
	s.stagedBytes += session.DurableOffset
	s.stagedByOwner[session.Owner] = ownerBytes + session.DurableOffset
	s.stagedBySession[session.ID] = session.DurableOffset
	return nil
}

func (s *Store) reserveStagedBytes(
	ctx context.Context,
	id, owner string,
	durableOffset, bytes int64,
) error {
	if bytes <= 0 || durableOffset < 0 {
		return ErrRecoveryRequired
	}
	s.stagingMu.Lock()
	if s.stagedBySession[id] != durableOffset {
		s.stagingMu.Unlock()
		return fmt.Errorf("%w: staged upload accounting differs from durable offset", ErrRecoveryRequired)
	}
	ownerBytes := s.stagedByOwner[owner]
	if bytes > s.opts.MaxStagedBytes-s.stagedBytes ||
		bytes > s.opts.MaxStagedBytesPerOwner-ownerBytes {
		s.stagingMu.Unlock()
		return ErrStagingLimit
	}
	if bytes > math.MaxInt64-s.physicalReservedBytes {
		s.stagingMu.Unlock()
		return fmt.Errorf("%w: physical staging reservation overflow", ErrRecoveryRequired)
	}
	requiredPhysicalBytes := s.physicalReservedBytes + bytes
	s.stagedBytes += bytes
	s.physicalReservedBytes = requiredPhysicalBytes
	s.stagedByOwner[owner] = ownerBytes + bytes
	s.stagedBySession[id] = durableOffset + bytes
	s.stagingMu.Unlock()

	if s.opts.CheckStagingCapacity != nil {
		if err := s.opts.CheckStagingCapacity(ctx, requiredPhysicalBytes); err != nil {
			rollbackErr := s.releaseStagedReservation(id, owner, bytes)
			return errors.Join(ErrStagingLimit, err, rollbackErr)
		}
	}
	return nil
}

func (s *Store) releaseStagedReservation(id, owner string, bytes int64) error {
	if bytes <= 0 {
		return ErrRecoveryRequired
	}
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	sessionBytes := s.stagedBySession[id]
	ownerBytes := s.stagedByOwner[owner]
	if sessionBytes < bytes || ownerBytes < bytes || s.stagedBytes < bytes ||
		s.physicalReservedBytes < bytes {
		return fmt.Errorf("%w: staged upload accounting underflow", ErrRecoveryRequired)
	}
	s.stagedBySession[id] = sessionBytes - bytes
	s.stagedByOwner[owner] = ownerBytes - bytes
	s.stagedBytes -= bytes
	s.physicalReservedBytes -= bytes
	return nil
}

func (s *Store) finishStagedReservation(bytes int64) error {
	if bytes <= 0 {
		return ErrRecoveryRequired
	}
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	if s.physicalReservedBytes < bytes {
		return fmt.Errorf("%w: physical staging reservation underflow", ErrRecoveryRequired)
	}
	s.physicalReservedBytes -= bytes
	return nil
}

func (s *Store) releaseSessionStaging(id, owner string) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	sessionBytes, exists := s.stagedBySession[id]
	if !exists {
		return nil
	}
	ownerBytes := s.stagedByOwner[owner]
	if ownerBytes < sessionBytes || s.stagedBytes < sessionBytes {
		return fmt.Errorf("%w: staged upload accounting underflow", ErrRecoveryRequired)
	}
	delete(s.stagedBySession, id)
	if ownerBytes == sessionBytes {
		delete(s.stagedByOwner, owner)
	} else {
		s.stagedByOwner[owner] = ownerBytes - sessionBytes
	}
	s.stagedBytes -= sessionBytes
	return nil
}

func isTerminalState(state State) bool {
	return state == StateCommitted || state == StateConflict || state == StateCancelled
}

func sessionExpired(state State, expiresAt, now time.Time) bool {
	return state != StateCommitting && !expiresAt.After(now)
}

func (s *Store) publishSnapshot(record diskState) {
	session := publicSession(record)
	s.mu.Lock()
	if !s.closed {
		s.sessions[session.ID] = session
		s.requestIndex[requestKey{owner: session.Owner, clientRequestID: session.ClientRequestID}] = session.ID
	}
	s.mu.Unlock()
}

func (s *Store) removeSnapshot(session Session) {
	s.mu.Lock()
	delete(s.sessions, session.ID)
	key := requestKey{owner: session.Owner, clientRequestID: session.ClientRequestID}
	if s.requestIndex[key] == session.ID {
		delete(s.requestIndex, key)
	}
	s.mu.Unlock()
	s.sessionLocks.Delete(session.ID)
}

func (s *Store) sessionLock(id string) *sync.Mutex {
	value, _ := s.sessionLocks.LoadOrStore(id, new(sync.Mutex))
	return value.(*sync.Mutex)
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.rootPath, id)
}

func (s *Store) removeSessionDir(id string) error {
	if err := s.validateRoot(); err != nil {
		return err
	}
	if _, err := s.loadSession(id); err != nil {
		return err
	}
	return s.removeSessionDirUnchecked(id)
}

func (s *Store) removeSessionDirUnchecked(id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	if err := s.validateRoot(); err != nil {
		return err
	}
	if err := rootio.RemoveAllPathNoFollow(s.sessionDir(id)); err != nil {
		return fmt.Errorf("remove upload session: %w", err)
	}
	return s.syncRoot()
}

func (s *Store) removeTerminalPayload(record diskState) error {
	if !isTerminalState(record.State) {
		return ErrInvalidState
	}
	payloadPath := filepath.Join(s.sessionDir(record.ID), payloadFileName)
	if _, err := os.Lstat(payloadPath); errors.Is(err, os.ErrNotExist) {
		return s.releaseSessionStaging(record.ID, record.Owner)
	} else if err != nil {
		return fmt.Errorf("inspect terminal upload payload: %w", err)
	}
	payload, err := s.openVerifiedPayload(record, os.O_RDONLY)
	if err != nil {
		return err
	}
	if err := payload.Close(); err != nil {
		return fmt.Errorf("close terminal upload payload: %w", err)
	}
	if err := removeVerifiedRegularFile(payloadPath); err != nil {
		return fmt.Errorf("remove terminal upload payload: %w", err)
	}
	if err := syncDirectory(s.sessionDir(record.ID)); err != nil {
		return fmt.Errorf("sync terminal upload session: %w", err)
	}
	return s.releaseSessionStaging(record.ID, record.Owner)
}

func (s *Store) validateRoot() error {
	s.mu.RLock()
	closed := s.closed
	expected := s.rootInfo
	s.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	dir, err := rootio.OpenDirPathNoFollow(s.rootPath)
	if err != nil {
		return fmt.Errorf("%w: open upload session root: %v", ErrRecoveryRequired, err)
	}
	info, statErr := dir.Stat()
	closeErr := dir.Close()
	if statErr != nil || closeErr != nil ||
		expected == nil || !os.SameFile(expected, info) ||
		!info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.Join(
			fmt.Errorf("%w: upload session root identity or mode changed", ErrRecoveryRequired),
			statErr,
			closeErr,
		)
	}
	return nil
}

func (s *Store) syncRoot() error {
	if err := s.validateRoot(); err != nil {
		return err
	}
	return syncDirectory(s.rootPath)
}

func syncDirectory(dirPath string) error {
	dir, err := rootio.OpenDirPathNoFollow(dirPath)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func removeVerifiedRegularFile(filePath string) error {
	file, err := rootio.OpenRegularFilePathNoFollow(filePath)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.Join(errors.New("refusing to remove unverified upload state file"), statErr, closeErr)
	}
	return rootio.RemoveAllPathNoFollow(filePath)
}

func rollbackPayload(file *os.File, offset int64) error {
	if file == nil {
		return fmt.Errorf("%w: upload payload rollback handle is unavailable", ErrRecoveryRequired)
	}
	truncateErr := file.Truncate(offset)
	syncErr := error(nil)
	if truncateErr == nil {
		syncErr = file.Sync()
	}
	if truncateErr != nil || syncErr != nil {
		return errors.Join(
			ErrRecoveryRequired,
			fmt.Errorf("rollback partial upload chunk: %w", truncateErr),
			fmt.Errorf("sync rolled-back upload payload: %w", syncErr),
		)
	}
	return nil
}

func hashOpenPayload(ctx context.Context, file *os.File, record diskState) (string, error) {
	if file == nil {
		return "", errors.New("upload payload is unavailable")
	}
	before, err := file.Stat()
	if err != nil ||
		workspace.PersistentIdentityTokenForFileInfo(before) != record.PayloadIdentity ||
		before.Size() != record.TotalBytes {
		return "", errors.Join(fmt.Errorf("%w: upload payload evidence changed before hashing", ErrRecoveryRequired), err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := blake3.New()
	written, hashErr := io.Copy(hasher, &contextReader{ctx: ctx, reader: file})
	after, statErr := file.Stat()
	if hashErr != nil &&
		(errors.Is(hashErr, context.Canceled) ||
			errors.Is(hashErr, context.DeadlineExceeded)) {
		return "", hashErr
	}
	if hashErr != nil || statErr != nil || written != record.TotalBytes ||
		!os.SameFile(before, after) ||
		workspace.PersistentIdentityTokenForFileInfo(after) != record.PayloadIdentity ||
		after.Size() != record.TotalBytes {
		return "", errors.Join(
			fmt.Errorf("%w: upload payload changed while hashing", ErrRecoveryRequired),
			hashErr,
			statErr,
		)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := ctxError(r.ctx); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(buffer)
	if err == nil {
		err = ctxError(r.ctx)
	}
	return n, err
}

func ctxError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}

func (s *Store) now() time.Time {
	return s.opts.Now().UTC()
}

func (s *Store) newID() (string, error) {
	return s.randomHex(16)
}

func (s *Store) randomHex(bytesCount int) (string, error) {
	s.randomMu.Lock()
	defer s.randomMu.Unlock()
	return randomHex(s.opts.Random, bytesCount)
}

func randomHex(reader io.Reader, bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", fmt.Errorf("generate upload session entropy: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func emptyBLAKE3() string {
	sum := blake3.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

func stateFileName(revision uint64) string {
	return fmt.Sprintf("state-%020d.json", revision)
}

func pendingStateFileName(revision uint64, nonce string) string {
	return fmt.Sprintf("pending-%020d-%s.json", revision, nonce)
}

func parseStateFileName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "state-") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(name, "state-"), ".json")
	if len(number) != 20 {
		return 0, false
	}
	value, err := strconv.ParseUint(number, 10, 64)
	return value, err == nil && value > 0 && stateFileName(value) == name
}

func isPendingStateName(name string) bool {
	_, ok := parsePendingStateName(name)
	return ok
}

func parsePendingStateName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "pending-") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, "pending-"), ".json")
	parts := strings.Split(value, "-")
	if len(parts) != 2 || len(parts[0]) != 20 || !validLowerHex(parts[1], 32) {
		return 0, false
	}
	revision, err := strconv.ParseUint(parts[0], 10, 64)
	return revision, err == nil && revision > 0
}
