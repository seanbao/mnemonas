// Package quota coordinates in-flight logical-byte reservations.
package quota

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode"
)

var (
	// ErrReservationReleased reports an attempt to refresh an inactive reservation.
	ErrReservationReleased = errors.New("quota reservation is already released")
	// ErrMutationLeaseReleased reports an attempt to use an inactive mutation lease.
	ErrMutationLeaseReleased = errors.New("quota mutation lease is already released")
)

// PrepareFunc recomputes current usage and builds an atomic reservation batch.
//
// The coordinator invokes the callback inside the quota-coordination critical
// section. Callers may perform filesystem usage scans there, but must not read
// request bodies or execute storage mutations.
type PrepareFunc func(context.Context, View) ([]Claim, error)

// Claim describes the incremental bytes required in one quota scope.
type Claim struct {
	Key           string
	UsedBytes     int64
	LimitBytes    int64
	RequiredBytes int64
}

// View exposes outstanding reservations while a batch is being prepared.
type View struct {
	reserved map[string]int64
}

// ReservedBytes returns outstanding bytes for a normalized scope key.
func (v View) ReservedBytes(key string) int64 {
	return nonNegative(v.reserved[key])
}

// ExceededError reports the first scope that cannot admit a batch.
type ExceededError struct {
	ClaimIndex     int
	Key            string
	UsedBytes      int64
	ReservedBytes  int64
	LimitBytes     int64
	RequiredBytes  int64
	AvailableBytes int64
}

func (e *ExceededError) Error() string {
	if e == nil {
		return "quota exceeded"
	}
	return fmt.Sprintf("quota scope %q exceeded", e.Key)
}

// Coordinator serializes usage snapshots and tracks in-flight byte deltas.
type Coordinator struct {
	initOnce     sync.Once
	gate         chan struct{}
	mutationGate chan struct{}
	reserved     map[string]int64
}

// NewCoordinator creates an empty process-level coordinator.
func NewCoordinator() *Coordinator {
	coordinator := new(Coordinator)
	coordinator.initialize()
	return coordinator
}

// ScopeKey returns a process-stable key for a quota kind and logical path.
func ScopeKey(kind, scopePath string) string {
	if strings.IndexFunc(kind, unicode.IsControl) >= 0 ||
		strings.IndexFunc(scopePath, unicode.IsControl) >= 0 {
		return ""
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	scopePath = strings.TrimSpace(scopePath)
	if kind == "" ||
		scopePath == "" ||
		!strings.HasPrefix(scopePath, "/") ||
		strings.Contains(kind, "\x00") ||
		strings.Contains(scopePath, "\x00") ||
		strings.Contains(scopePath, "\\") {
		return ""
	}
	scopePath = path.Clean(scopePath)
	return kind + "\x00" + scopePath
}

// Reserve snapshots usage and atomically registers every claim returned by prepare.
//
// The mutation gate is held only while prepare runs and the reservation is
// registered. It is released before request-body reads or storage mutations.
func (c *Coordinator) Reserve(ctx context.Context, prepare PrepareFunc) (*Reservation, error) {
	if c == nil {
		return nil, errors.New("quota coordinator is unavailable")
	}
	if ctx == nil {
		return nil, errors.New("quota reservation context is unavailable")
	}
	if prepare == nil {
		return nil, errors.New("quota reservation prepare callback is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	mutation, err := c.AcquireMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer mutation.Release()
	return mutation.reserve(ctx, prepare)
}

// Reservation owns an admitted batch until Release is called.
type Reservation struct {
	coordinator *Coordinator
	bytes       map[string]int64
	released    bool
}

// Release returns all bytes in the batch. It is safe to call more than once.
func (r *Reservation) Release() {
	if r == nil || r.coordinator == nil {
		return
	}
	if err := r.coordinator.lock(context.Background()); err != nil {
		return
	}
	defer r.coordinator.unlock()
	if r.released {
		return
	}
	r.coordinator.subtractLocked(r.bytes)
	r.bytes = nil
	r.released = true
}

// MutationLease serializes cooperative HTTP mutation commits with usage scans.
type MutationLease struct {
	coordinator *Coordinator
	mu          sync.Mutex
	released    bool
}

// AcquireMutation waits for the process-level mutation gate.
func (c *Coordinator) AcquireMutation(ctx context.Context) (*MutationLease, error) {
	if c == nil {
		return nil, errors.New("quota coordinator is unavailable")
	}
	if ctx == nil {
		return nil, errors.New("quota mutation context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.initialize()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.mutationGate:
	}
	if err := ctx.Err(); err != nil {
		c.mutationGate <- struct{}{}
		return nil, err
	}
	return &MutationLease{coordinator: c}, nil
}

// Refresh atomically replaces reservation using a commit-time usage snapshot.
//
// The lease must remain active through the protected storage mutation.
func (l *MutationLease) Refresh(ctx context.Context, reservation *Reservation, prepare PrepareFunc) error {
	if l == nil || l.coordinator == nil {
		return errors.New("quota mutation lease is unavailable")
	}
	if ctx == nil {
		return errors.New("quota reservation context is unavailable")
	}
	if reservation == nil || reservation.coordinator != l.coordinator {
		return errors.New("quota reservation does not belong to mutation lease coordinator")
	}
	if prepare == nil {
		return errors.New("quota reservation prepare callback is unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return ErrMutationLeaseReleased
	}
	return l.coordinator.refreshUnderMutation(ctx, reservation, prepare)
}

// Release returns the process-level mutation gate. It is idempotent.
func (l *MutationLease) Release() {
	if l == nil || l.coordinator == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	l.coordinator.mutationGate <- struct{}{}
}

func (l *MutationLease) reserve(ctx context.Context, prepare PrepareFunc) (*Reservation, error) {
	if l == nil || l.coordinator == nil {
		return nil, errors.New("quota mutation lease is unavailable")
	}
	if ctx == nil {
		return nil, errors.New("quota reservation context is unavailable")
	}
	if prepare == nil {
		return nil, errors.New("quota reservation prepare callback is unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil, ErrMutationLeaseReleased
	}
	return l.coordinator.reserveUnderMutation(ctx, prepare)
}

func (c *Coordinator) reserveUnderMutation(ctx context.Context, prepare PrepareFunc) (*Reservation, error) {
	if err := c.lock(ctx); err != nil {
		return nil, err
	}
	defer c.unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pending, err := c.prepareLocked(ctx, nil, prepare)
	if err != nil {
		return nil, err
	}
	c.addLocked(pending)
	return &Reservation{coordinator: c, bytes: pending}, nil
}

func (c *Coordinator) refreshUnderMutation(ctx context.Context, reservation *Reservation, prepare PrepareFunc) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	defer c.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if reservation.released {
		return ErrReservationReleased
	}
	pending, err := c.prepareLocked(ctx, reservation, prepare)
	if err != nil {
		return err
	}
	c.subtractLocked(reservation.bytes)
	c.addLocked(pending)
	reservation.bytes = pending
	return nil
}

type aggregateClaim struct {
	firstIndex    int
	usedBytes     int64
	limitBytes    int64
	requiredBytes int64
}

func (c *Coordinator) prepareLocked(ctx context.Context, replaced *Reservation, prepare PrepareFunc) (map[string]int64, error) {
	if c.reserved == nil {
		c.reserved = make(map[string]int64)
	}
	viewReserved := c.reserved
	if replaced != nil {
		viewReserved = cloneReserved(c.reserved)
		subtractReserved(viewReserved, replaced.bytes)
	}
	claims, err := prepare(ctx, View{reserved: viewReserved})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	aggregated := make(map[string]aggregateClaim, len(claims))
	orderedKeys := make([]string, 0, len(claims))
	for index, claim := range claims {
		if claim.Key == "" {
			return nil, errors.New("quota reservation scope key is empty")
		}
		usedBytes := nonNegative(claim.UsedBytes)
		limitBytes := nonNegative(claim.LimitBytes)
		requiredBytes := nonNegative(claim.RequiredBytes)
		aggregate, exists := aggregated[claim.Key]
		if !exists {
			aggregated[claim.Key] = aggregateClaim{
				firstIndex:    index,
				usedBytes:     usedBytes,
				limitBytes:    limitBytes,
				requiredBytes: requiredBytes,
			}
			orderedKeys = append(orderedKeys, claim.Key)
			continue
		}
		if aggregate.requiredBytes > maxInt64-requiredBytes {
			return nil, errors.New("quota reservation required byte count overflow")
		}
		aggregate.requiredBytes += requiredBytes
		if usedBytes > aggregate.usedBytes {
			aggregate.usedBytes = usedBytes
		}
		if limitBytes < aggregate.limitBytes {
			aggregate.limitBytes = limitBytes
		}
		aggregated[claim.Key] = aggregate
	}

	pending := make(map[string]int64, len(aggregated))
	for _, key := range orderedKeys {
		claim := aggregated[key]
		reservedBytes := nonNegative(viewReserved[key])
		availableBytes := quotaAvailable(claim.limitBytes, claim.usedBytes, reservedBytes)
		if claim.requiredBytes > availableBytes {
			return nil, &ExceededError{
				ClaimIndex:     claim.firstIndex,
				Key:            key,
				UsedBytes:      claim.usedBytes,
				ReservedBytes:  reservedBytes,
				LimitBytes:     claim.limitBytes,
				RequiredBytes:  claim.requiredBytes,
				AvailableBytes: availableBytes,
			}
		}
		if claim.requiredBytes > 0 {
			pending[key] = claim.requiredBytes
		}
	}
	for key, requiredBytes := range pending {
		if viewReserved[key] > maxInt64-requiredBytes {
			return nil, errors.New("quota reservation byte count overflow")
		}
	}
	return pending, nil
}

func cloneReserved(source map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func subtractReserved(target, removed map[string]int64) {
	for key, bytes := range removed {
		remaining := target[key] - bytes
		if remaining > 0 {
			target[key] = remaining
			continue
		}
		delete(target, key)
	}
}

func (c *Coordinator) subtractLocked(removed map[string]int64) {
	subtractReserved(c.reserved, removed)
}

func (c *Coordinator) addLocked(added map[string]int64) {
	for key, bytes := range added {
		c.reserved[key] += bytes
	}
}

func (c *Coordinator) initialize() {
	c.initOnce.Do(func() {
		c.gate = make(chan struct{}, 1)
		c.gate <- struct{}{}
		c.mutationGate = make(chan struct{}, 1)
		c.mutationGate <- struct{}{}
		c.reserved = make(map[string]int64)
	})
}

func (c *Coordinator) lock(ctx context.Context) error {
	c.initialize()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.gate:
		return nil
	}
}

func (c *Coordinator) unlock() {
	c.gate <- struct{}{}
}

const maxInt64 = int64(^uint64(0) >> 1)

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func quotaAvailable(limitBytes, usedBytes, reservedBytes int64) int64 {
	if usedBytes >= limitBytes {
		return 0
	}
	availableBytes := limitBytes - usedBytes
	if reservedBytes >= availableBytes {
		return 0
	}
	return availableBytes - reservedBytes
}
