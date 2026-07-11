package share

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

const (
	downloadTicketCookiePrefix       = "mnemonas_share_download_"
	downloadTicketSecureCookiePrefix = "__Host-mnemonas_share_download_"
	downloadTicketCookiePath         = "/"
	defaultDownloadTicketTTL         = 24 * time.Hour
	maxDownloadTicketCookies         = 32
	downloadTicketIDSize             = 16
	downloadTicketBinderIDSize       = 16
	downloadTicketClientNonceSize    = 32
	downloadTicketBindingSize        = sha256.Size
	downloadTicketHashSize           = sha256.Size
	downloadTicketGrantVersion       = byte(2)
	downloadTicketGrantSize          = 1 + 8 + downloadTicketIDSize + downloadTicketBinderIDSize + downloadTicketHashSize + downloadTicketHashSize + downloadTicketHashSize + sha256.Size
	downloadTicketKeySize            = sha256.Size
	downloadTicketKeyDomain          = "mnemonas/share-download-ticket/signing-key/v1"
	downloadTicketBinderIDDomain     = "mnemonas/share-download-ticket/binder-id/v1"
	downloadTicketBinderValueDomain  = "mnemonas/share-download-ticket/binder-value/v1"
	downloadTicketStateDomain        = "mnemonas/share-download-ticket/share-state/v1"
	downloadTicketOwnerDomain        = "mnemonas/share-download-ticket/owner-state/v1"
	downloadTicketTargetDomain       = "mnemonas/share-download-ticket/target/v1"
)

var (
	defaultDownloadTicketRandomReader = io.Reader(rand.Reader)
	errDownloadTicketRequired         = errors.New("download ticket required")
	errDownloadTicketInvalid          = errors.New("invalid download ticket")
	errDownloadTicketExpired          = errors.New("download ticket expired")
	errDownloadTicketStale            = errors.New("download ticket stale")
	errDownloadTicketKeyUnavailable   = errors.New("download ticket signing key unavailable")
	errDownloadTicketShareChanged     = errors.New("share changed while issuing download ticket")
	errDownloadTicketRootFolder       = errors.New("folder root requires an archive")
	errDownloadTicketDirectory        = errors.New("download target is a directory")
	errDownloadTicketWrongShareType   = errors.New("download target does not match share type")
	errDownloadTicketNotRegular       = errors.New("download target is not a regular file")
)

type downloadTicketRequest struct {
	Path        json.RawMessage `json:"path"`
	Archive     json.RawMessage `json:"archive"`
	ClientNonce json.RawMessage `json:"client_nonce"`
}

// DownloadTicketResponse is returned after one logical download has been
// atomically reserved.
type DownloadTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

type downloadTicketTarget struct {
	fullPath string
	archive  string
}

type downloadTicketGrant struct {
	expiresAt   int64
	ticketID    [downloadTicketIDSize]byte
	binderID    [downloadTicketBinderIDSize]byte
	bindingHash [downloadTicketHashSize]byte
	targetHash  [downloadTicketHashSize]byte
	stateHash   [downloadTicketHashSize]byte
}

func newEphemeralDownloadTicketKey() []byte {
	key := make([]byte, downloadTicketKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil
	}
	return key
}

// DeriveDownloadTicketSigningKey derives a purpose-specific key from the
// server authentication secret.
func DeriveDownloadTicketSigningKey(secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(downloadTicketKeyDomain))
	return mac.Sum(nil)
}

// SetDownloadTicketSigningKey replaces the signing key used for public
// download tickets. A stable key makes issued tickets survive process restarts.
func (h *Handler) SetDownloadTicketSigningKey(key []byte) error {
	if len(key) < downloadTicketKeySize {
		return errors.New("download ticket signing key must be at least 32 bytes")
	}
	h.downloadTicketKeyMu.Lock()
	h.downloadTicketKey = append(h.downloadTicketKey[:0], key...)
	h.downloadTicketKeyMu.Unlock()
	return nil
}

func (h *Handler) currentDownloadTicketKey() []byte {
	h.downloadTicketKeyMu.RLock()
	defer h.downloadTicketKeyMu.RUnlock()
	return append([]byte(nil), h.downloadTicketKey...)
}

// CreateDownloadTicket validates a concrete target and atomically reserves one
// logical access before issuing a stateless, signed grant.
func (h *Handler) CreateDownloadTicket(w http.ResponseWriter, r *http.Request) {
	setPublicShareJSONHeaders(w)
	if rejectNonPOSTPublicShareAccess(w, r) {
		return
	}

	var req downloadTicketRequest
	if err := decodeJSONBodyStrict(r, &req); err != nil {
		writeShareJSONBodyError(w, err)
		return
	}
	clientNonce, err := decodeDownloadTicketClientNonce(req.ClientNonce)
	if err != nil {
		writeShareJSONBodyError(w, err)
		return
	}
	relPath, _, err := decodeOptionalDownloadTicketString(req.Path)
	if err != nil {
		writeShareJSONBodyError(w, err)
		return
	}
	archive, archivePresent, err := decodeOptionalDownloadTicketString(req.Archive)
	if err != nil {
		writeShareJSONBodyError(w, err)
		return
	}
	if archivePresent && archive != "zip" {
		writeShareError(w, http.StatusBadRequest, "unsupported archive format", "INVALID_ARCHIVE_FORMAT")
		return
	}

	id := chi.URLParam(r, "id")
	share, err := h.authorizeShare(r, id)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	releaseTicket, acquired := h.acquireDownloadTicketIssuance(w)
	if !acquired {
		return
	}
	defer releaseTicket()
	key := h.currentDownloadTicketKey()
	if len(key) < downloadTicketKeySize {
		writeShareError(w, http.StatusServiceUnavailable, "download tickets unavailable", "DOWNLOAD_TICKET_UNAVAILABLE")
		return
	}
	binderID, binding, err := deriveDownloadTicketBinder(key, share.ID, clientNonce)
	if err != nil {
		writeShareError(w, http.StatusServiceUnavailable, "download tickets unavailable", "DOWNLOAD_TICKET_UNAVAILABLE")
		return
	}
	bindingValue := base64.RawURLEncoding.EncodeToString(binding[:])
	cookieName := downloadTicketBinderCookieName(r, binderID)
	cookieCount, targetValues := inspectDownloadTicketCookies(r, cookieName)
	validTargetCookie := len(targetValues) == 1 && downloadTicketBindingMatches(targetValues[0], binding)
	if len(targetValues) > 1 || (len(targetValues) == 1 && !validTargetCookie) || (cookieCount >= maxDownloadTicketCookies && !validTargetCookie) {
		writeDownloadTicketRateLimit(w)
		return
	}
	if archive == "zip" {
		release, acquired := h.acquirePublicArchive(w)
		if !acquired {
			return
		}
		defer release()
	}
	target, err := h.preflightDownloadTicketTarget(r.Context(), share, relPath, archive)
	if err != nil {
		writeDownloadTicketTargetError(w, err)
		return
	}

	now := h.downloadTicketClock().UTC()
	expiresAt := now.Add(defaultDownloadTicketTTL)
	if share.ExpiresAt != nil && share.ExpiresAt.Before(expiresAt) {
		expiresAt = share.ExpiresAt.UTC()
	}
	expiresAt = time.Unix(expiresAt.Unix(), 0).UTC()
	if !expiresAt.After(now) {
		writePublicShareAccessError(w, ErrShareExpired)
		return
	}

	var ticketID [downloadTicketIDSize]byte
	if h.downloadTicketRandom == nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_CREATE_FAILED")
		return
	}
	if _, err := io.ReadFull(h.downloadTicketRandom, ticketID[:]); err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_CREATE_FAILED")
		return
	}
	bindingHash := sha256.Sum256(binding[:])
	shareStateHash := hashDownloadTicketShareState(share)
	stateHash, err := h.hashDownloadTicketState(share)
	if err != nil {
		writePublicShareAccessError(w, err)
		return
	}
	grant := downloadTicketGrant{
		expiresAt:   expiresAt.Unix(),
		ticketID:    ticketID,
		binderID:    binderID,
		bindingHash: bindingHash,
		targetHash:  hashDownloadTicketTarget(target.fullPath, target.archive),
		stateHash:   stateHash,
	}
	grantValue, err := sealDownloadTicketGrant(grant, key)
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_CREATE_FAILED")
		return
	}
	payload, err := marshalShareJSON(&DownloadTicketResponse{
		Ticket:    grantValue,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
	if err != nil {
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_CREATE_FAILED")
		return
	}

	updatedShare, reservation, reserveErr := h.store.reserveAuthorizedAccessMatching(id, func(current *Share) error {
		currentHash := hashDownloadTicketShareState(current)
		if subtle.ConstantTimeCompare(currentHash[:], shareStateHash[:]) != 1 {
			return errDownloadTicketShareChanged
		}
		return nil
	})
	if reserveErr != nil && !IsPersistenceWarning(reserveErr) {
		if errors.Is(reserveErr, errDownloadTicketShareChanged) {
			writeShareError(w, http.StatusConflict, "share changed, retry", "SHARE_CHANGED")
			return
		}
		writePublicShareAccessError(w, reserveErr)
		return
	}
	if IsPersistenceWarning(reserveErr) {
		markSharePersistenceWarningHeaders(w)
	}
	if err := h.ensureShareOwnerActive(updatedShare); err != nil {
		if !h.rollbackDownloadTicketReservation(w, reservation) {
			return
		}
		writePublicShareAccessError(w, err)
		return
	}
	if err := h.authorizeSharePath(r.Context(), updatedShare, target.fullPath); err != nil {
		if !h.rollbackDownloadTicketReservation(w, reservation) {
			return
		}
		writePublicSharePathError(w, err, "DOWNLOAD_TICKET_CREATE_FAILED")
		return
	}
	currentStateHash, err := h.hashDownloadTicketState(updatedShare)
	if err != nil {
		if !h.rollbackDownloadTicketReservation(w, reservation) {
			return
		}
		writePublicShareAccessError(w, err)
		return
	}
	if subtle.ConstantTimeCompare(currentStateHash[:], stateHash[:]) != 1 {
		if !h.rollbackDownloadTicketReservation(w, reservation) {
			return
		}
		writeShareError(w, http.StatusConflict, "share changed, retry", "SHARE_CHANGED")
		return
	}

	h.setDownloadTicketBindingCookie(w, r, binderID, bindingValue, h.downloadTicketClock().UTC(), expiresAt)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func decodeDownloadTicketClientNonce(raw json.RawMessage) ([downloadTicketClientNonceSize]byte, error) {
	var nonce [downloadTicketClientNonceSize]byte
	if len(raw) == 0 || string(raw) == "null" {
		return nonce, errors.New("client_nonce is required")
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nonce, err
	}
	if len(encoded) != base64.RawURLEncoding.EncodedLen(downloadTicketClientNonceSize) {
		return nonce, errors.New("client_nonce must encode exactly 32 bytes")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != downloadTicketClientNonceSize || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nonce, errors.New("client_nonce must be canonical base64url without padding")
	}
	copy(nonce[:], decoded)
	return nonce, nil
}

func decodeOptionalDownloadTicketString(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if string(raw) == "null" {
		return "", false, errors.New("null is not a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (h *Handler) preflightDownloadTicketTarget(ctx context.Context, share *Share, relPath, archive string) (*downloadTicketTarget, error) {
	if share == nil {
		return nil, ErrShareNotFound
	}
	cleanPath, err := normalizeShareRelativePath(relPath)
	if err != nil {
		return nil, errInvalidShareArchivePath
	}

	fullPath := share.Path
	switch share.Type {
	case ShareTypeFile:
		if cleanPath != "" {
			return nil, errDownloadTicketWrongShareType
		}
	case ShareTypeFolder:
		if cleanPath != "" {
			fullPath = path.Join(share.Path, cleanPath)
			if !isWithinSharePath(share.Path, fullPath) {
				return nil, errInvalidShareArchivePath
			}
		}
	default:
		return nil, errDownloadTicketWrongShareType
	}
	if h.fs == nil {
		return nil, errDownloadTicketKeyUnavailable
	}
	if err := h.authorizeSharePath(ctx, share, fullPath); err != nil {
		return nil, err
	}

	statProvider, ok := h.fs.(FileStatProvider)
	if !ok {
		return nil, errDownloadTicketKeyUnavailable
	}
	if archive == "zip" {
		entries, err := h.collectShareArchiveEntries(ctx, share, statProvider, fullPath)
		if err != nil {
			return nil, err
		}
		if err := validateShareArchiveEntries(entries); err != nil {
			return nil, err
		}
		if err := h.preflightShareArchiveSnapshots(ctx, entries); err != nil {
			return nil, err
		}
		return &downloadTicketTarget{fullPath: fullPath, archive: archive}, nil
	}

	info, err := statProvider.Stat(ctx, fullPath)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, storage.ErrNotFound
	}
	if info.IsDir {
		if share.Type == ShareTypeFolder && cleanPath == "" {
			return nil, errDownloadTicketRootFolder
		}
		return nil, errDownloadTicketDirectory
	}
	if !info.Mode.IsRegular() {
		return nil, errDownloadTicketNotRegular
	}
	return &downloadTicketTarget{fullPath: fullPath}, nil
}

func (h *Handler) preflightShareArchiveSnapshots(ctx context.Context, entries []shareArchiveEntry) error {
	if _, ok := h.fs.(FileSnapshotOpener); !ok {
		return nil
	}
	var totalBytes int64
	for _, entry := range entries {
		if entry.info == nil {
			return errShareArchiveMissingMetadata
		}
		if entry.info.IsDir {
			continue
		}
		reader, snapshotInfo, err := h.openShareArchiveFile(ctx, entry)
		if err != nil {
			return err
		}
		if reader == nil {
			return storage.ErrNotFound
		}
		closeErr := reader.Close()
		if closeErr != nil {
			return newShareArchiveInternalError("close share archive preflight file", closeErr)
		}
		if snapshotInfo == nil {
			snapshotInfo = entry.info
		}
		if snapshotInfo.IsDir {
			return errShareArchiveSnapshotChanged
		}
		if !snapshotInfo.Mode.IsRegular() {
			return storage.ErrNotRegular
		}
		if snapshotInfo.Size < 0 || totalBytes > maxShareArchiveBytes-snapshotInfo.Size {
			return errShareArchiveContentTooLarge
		}
		if snapshotInfo.Size != entry.info.Size {
			return errShareArchiveSnapshotChanged
		}
		totalBytes += snapshotInfo.Size
	}
	return nil
}

func writeDownloadTicketTargetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDownloadTicketKeyUnavailable):
		writeShareError(w, http.StatusServiceUnavailable, "filesystem not available", "FILESYSTEM_UNAVAILABLE")
	case errors.Is(err, errDownloadTicketRootFolder), errors.Is(err, errDownloadTicketWrongShareType):
		writeShareError(w, http.StatusBadRequest, "invalid share type", "INVALID_SHARE_TYPE")
	case errors.Is(err, errDownloadTicketDirectory), errors.Is(err, errInvalidShareArchivePath):
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
	case errors.Is(err, errDownloadTicketNotRegular):
		writeShareError(w, http.StatusConflict, "download target is not a regular file", "FILE_NOT_REGULAR")
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, ErrShareNotFound):
		writeShareError(w, http.StatusNotFound, "file not found", "FILE_NOT_FOUND")
	case errors.Is(err, storage.ErrNotDir), errors.Is(err, storage.ErrIsDir):
		writeShareError(w, http.StatusBadRequest, "invalid path", "INVALID_PATH")
	case isPublicShareArchiveResponseError(err):
		writePublicShareArchiveError(w, err)
	default:
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_CREATE_FAILED")
	}
}

func (h *Handler) rollbackDownloadTicketReservation(w http.ResponseWriter, reservation *authorizedAccessReservation) bool {
	if err := h.store.rollbackAuthorizedAccess(reservation); err != nil {
		if IsPersistenceWarning(err) {
			markSharePersistenceWarningHeaders(w)
			return true
		}
		writeShareError(w, http.StatusInternalServerError, "internal server error", "DOWNLOAD_TICKET_ROLLBACK_FAILED")
		return false
	}
	return true
}

func downloadTicketBinderCookiePrefix(r *http.Request) string {
	if requestIsHTTPS(r) {
		return downloadTicketSecureCookiePrefix
	}
	return downloadTicketCookiePrefix
}

func downloadTicketBinderCookieName(r *http.Request, binderID [downloadTicketBinderIDSize]byte) string {
	return downloadTicketBinderCookiePrefix(r) + hex.EncodeToString(binderID[:])
}

func inspectDownloadTicketCookies(r *http.Request, targetName string) (int, []string) {
	prefixes := []string{downloadTicketCookiePrefix}
	if requestIsHTTPS(r) {
		// An HTTP-origin cookie can accompany a request after an HTTPS upgrade, so
		// both namespaces count toward the request-local ticket bound.
		prefixes = append(prefixes, downloadTicketSecureCookiePrefix)
	}
	count := 0
	var targetValues []string
	for _, cookie := range r.Cookies() {
		if cookie.Name == targetName {
			targetValues = append(targetValues, cookie.Value)
		}
		for _, prefix := range prefixes {
			if !strings.HasPrefix(cookie.Name, prefix) {
				continue
			}
			suffix := strings.TrimPrefix(cookie.Name, prefix)
			if len(suffix) != downloadTicketBinderIDSize*2 || suffix != strings.ToLower(suffix) {
				break
			}
			decoded, err := hex.DecodeString(suffix)
			if err == nil && len(decoded) == downloadTicketBinderIDSize {
				count++
			}
			break
		}
	}
	return count, targetValues
}

func downloadTicketBindingMatches(encoded string, expected [downloadTicketBindingSize]byte) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != downloadTicketBindingSize || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return false
	}
	return subtle.ConstantTimeCompare(decoded, expected[:]) == 1
}

func deriveDownloadTicketBinder(key []byte, shareID string, clientNonce [downloadTicketClientNonceSize]byte) ([downloadTicketBinderIDSize]byte, [downloadTicketBindingSize]byte, error) {
	var binderID [downloadTicketBinderIDSize]byte
	var binding [downloadTicketBindingSize]byte
	if len(key) < downloadTicketKeySize || shareID == "" {
		return binderID, binding, errDownloadTicketKeyUnavailable
	}

	idMAC := hmac.New(sha256.New, key)
	writeDownloadTicketHashField(idMAC, downloadTicketBinderIDDomain)
	writeDownloadTicketHashField(idMAC, shareID)
	writeDownloadTicketHashField(idMAC, string(clientNonce[:]))
	copy(binderID[:], idMAC.Sum(nil))

	valueMAC := hmac.New(sha256.New, key)
	writeDownloadTicketHashField(valueMAC, downloadTicketBinderValueDomain)
	writeDownloadTicketHashField(valueMAC, shareID)
	writeDownloadTicketHashField(valueMAC, string(clientNonce[:]))
	copy(binding[:], valueMAC.Sum(nil))
	return binderID, binding, nil
}

func writeDownloadTicketRateLimit(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeShareError(w, http.StatusTooManyRequests, "too many active download tickets, try later", "DOWNLOAD_TICKET_RATE_LIMITED")
}

func (h *Handler) setDownloadTicketBindingCookie(w http.ResponseWriter, r *http.Request, binderID [downloadTicketBinderIDSize]byte, bindingValue string, now, expiresAt time.Time) {
	maxAge := int((expiresAt.Sub(now) + time.Second - 1) / time.Second)
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     downloadTicketBinderCookieName(r, binderID),
		Value:    bindingValue,
		Path:     downloadTicketCookiePath,
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) downloadTicketClock() time.Time {
	if h.downloadTicketNow == nil {
		return time.Now()
	}
	return h.downloadTicketNow()
}

func sealDownloadTicketGrant(grant downloadTicketGrant, key []byte) (string, error) {
	if len(key) < downloadTicketKeySize {
		return "", errDownloadTicketKeyUnavailable
	}
	payload := make([]byte, downloadTicketGrantSize-sha256.Size)
	payload[0] = downloadTicketGrantVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(grant.expiresAt))
	offset := 9
	copy(payload[offset:offset+downloadTicketIDSize], grant.ticketID[:])
	offset += downloadTicketIDSize
	copy(payload[offset:offset+downloadTicketBinderIDSize], grant.binderID[:])
	offset += downloadTicketBinderIDSize
	copy(payload[offset:offset+downloadTicketHashSize], grant.bindingHash[:])
	offset += downloadTicketHashSize
	copy(payload[offset:offset+downloadTicketHashSize], grant.targetHash[:])
	offset += downloadTicketHashSize
	copy(payload[offset:offset+downloadTicketHashSize], grant.stateHash[:])

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	sealed := append(payload, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (h *Handler) readDownloadTicketGrant(r *http.Request) (*downloadTicketGrant, error) {
	values, ok := r.URL.Query()["ticket"]
	if !ok || len(values) != 1 || values[0] == "" {
		return nil, errDownloadTicketRequired
	}
	ticket := values[0]
	if len(ticket) > 256 {
		return nil, errDownloadTicketInvalid
	}
	sealed, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(sealed) != downloadTicketGrantSize || base64.RawURLEncoding.EncodeToString(sealed) != ticket {
		return nil, errDownloadTicketInvalid
	}
	payload := sealed[:downloadTicketGrantSize-sha256.Size]
	key := h.currentDownloadTicketKey()
	if len(key) < downloadTicketKeySize {
		return nil, errDownloadTicketInvalid
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(sealed[len(payload):], mac.Sum(nil)) {
		return nil, errDownloadTicketInvalid
	}
	if payload[0] != downloadTicketGrantVersion {
		return nil, errDownloadTicketInvalid
	}

	grant := &downloadTicketGrant{expiresAt: int64(binary.BigEndian.Uint64(payload[1:9]))}
	offset := 9
	copy(grant.ticketID[:], payload[offset:offset+downloadTicketIDSize])
	offset += downloadTicketIDSize
	copy(grant.binderID[:], payload[offset:offset+downloadTicketBinderIDSize])
	offset += downloadTicketBinderIDSize
	copy(grant.bindingHash[:], payload[offset:offset+downloadTicketHashSize])
	offset += downloadTicketHashSize
	copy(grant.targetHash[:], payload[offset:offset+downloadTicketHashSize])
	offset += downloadTicketHashSize
	copy(grant.stateHash[:], payload[offset:offset+downloadTicketHashSize])
	if h.downloadTicketClock().Unix() >= grant.expiresAt {
		return nil, errDownloadTicketExpired
	}
	cookieName := downloadTicketBinderCookieName(r, grant.binderID)
	var cookieValue string
	cookieCount := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name != cookieName {
			continue
		}
		cookieValue = cookie.Value
		cookieCount++
	}
	if cookieCount != 1 {
		return nil, errDownloadTicketInvalid
	}
	binding, err := base64.RawURLEncoding.DecodeString(cookieValue)
	if err != nil || len(binding) != downloadTicketBindingSize || base64.RawURLEncoding.EncodeToString(binding) != cookieValue {
		return nil, errDownloadTicketInvalid
	}
	bindingHash := sha256.Sum256(binding)
	if subtle.ConstantTimeCompare(grant.bindingHash[:], bindingHash[:]) != 1 {
		return nil, errDownloadTicketInvalid
	}
	return grant, nil
}

func (h *Handler) loadShareForDownloadTicket(id string) (*Share, error) {
	share, err := h.store.Get(id)
	if err != nil {
		return nil, err
	}
	if !share.Enabled {
		return nil, ErrShareDisabled
	}
	if share.IsExpired() {
		return nil, ErrShareExpired
	}
	if err := h.ensureShareOwnerActive(share); err != nil {
		return nil, err
	}
	return share, nil
}

func (h *Handler) validateDownloadTicketTarget(grant *downloadTicketGrant, share *Share, fullPath, archive string) error {
	if grant == nil || share == nil {
		return errDownloadTicketInvalid
	}
	stateHash, err := h.hashDownloadTicketState(share)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(grant.stateHash[:], stateHash[:]) != 1 {
		return errDownloadTicketStale
	}
	targetHash := hashDownloadTicketTarget(fullPath, archive)
	if subtle.ConstantTimeCompare(grant.targetHash[:], targetHash[:]) != 1 {
		return errDownloadTicketInvalid
	}
	return nil
}

func (h *Handler) hashDownloadTicketState(share *Share) ([downloadTicketHashSize]byte, error) {
	shareHash := hashDownloadTicketShareState(share)
	hasher := sha256.New()
	writeDownloadTicketHashField(hasher, downloadTicketOwnerDomain)
	writeDownloadTicketHashField(hasher, string(shareHash[:]))
	if h == nil || h.userStore == nil || share == nil || share.CreatedBy == "" {
		writeDownloadTicketHashField(hasher, "")
		var result [downloadTicketHashSize]byte
		copy(result[:], hasher.Sum(nil))
		return result, nil
	}

	owner, err := resolveShareOwner(h.userStore, share.CreatedBy)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return [downloadTicketHashSize]byte{}, ErrShareDisabled
		}
		return [downloadTicketHashSize]byte{}, err
	}
	if owner.Disabled {
		return [downloadTicketHashSize]byte{}, ErrShareDisabled
	}
	writeDownloadTicketHashField(hasher, owner.ID)
	writeDownloadTicketHashField(hasher, owner.Username)
	writeDownloadTicketHashField(hasher, owner.UpdatedAt.UTC().Format(time.RFC3339Nano))
	writeDownloadTicketHashField(hasher, string(binary.BigEndian.AppendUint64(nil, owner.CredentialVersion)))
	writeDownloadTicketHashField(hasher, string(owner.Role))
	writeDownloadTicketHashField(hasher, path.Clean(owner.HomeDir))
	writeDownloadTicketHashField(hasher, string(binary.BigEndian.AppendUint64(nil, uint64(len(owner.Groups)))))
	for _, group := range owner.Groups {
		writeDownloadTicketHashField(hasher, group)
	}
	var result [downloadTicketHashSize]byte
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func writeDownloadTicketError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDownloadTicketRequired):
		writeShareError(w, http.StatusUnauthorized, "download ticket required", "DOWNLOAD_TICKET_REQUIRED")
	case errors.Is(err, errDownloadTicketExpired):
		writeShareError(w, http.StatusGone, "download ticket expired", "DOWNLOAD_TICKET_EXPIRED")
	case errors.Is(err, errDownloadTicketStale):
		writeShareError(w, http.StatusUnauthorized, "download ticket is stale", "DOWNLOAD_TICKET_STALE")
	default:
		writeShareError(w, http.StatusUnauthorized, "invalid download ticket", "DOWNLOAD_TICKET_INVALID")
	}
}

func writeDownloadTicketValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrShareDisabled), errors.Is(err, ErrShareExpired), errors.Is(err, ErrShareNotFound):
		writePublicShareAccessError(w, err)
	default:
		writeDownloadTicketError(w, err)
	}
}

func hashDownloadTicketTarget(fullPath, archive string) [downloadTicketHashSize]byte {
	hasher := sha256.New()
	writeDownloadTicketHashField(hasher, downloadTicketTargetDomain)
	writeDownloadTicketHashField(hasher, path.Clean(fullPath))
	writeDownloadTicketHashField(hasher, archive)
	var result [downloadTicketHashSize]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func hashDownloadTicketShareState(share *Share) [downloadTicketHashSize]byte {
	hasher := sha256.New()
	writeDownloadTicketHashField(hasher, downloadTicketStateDomain)
	if share == nil {
		var result [downloadTicketHashSize]byte
		copy(result[:], hasher.Sum(nil))
		return result
	}
	writeDownloadTicketHashField(hasher, share.ID)
	writeDownloadTicketHashField(hasher, path.Clean(share.Path))
	writeDownloadTicketHashField(hasher, string(share.Type))
	writeDownloadTicketHashField(hasher, share.CreatedBy)
	writeDownloadTicketHashField(hasher, share.CreatedAt.UTC().Format(time.RFC3339Nano))
	if share.ExpiresAt == nil {
		writeDownloadTicketHashField(hasher, "")
	} else {
		writeDownloadTicketHashField(hasher, share.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	writeDownloadTicketHashField(hasher, share.PasswordHash)
	writeDownloadTicketHashField(hasher, string(share.Permission))
	if share.Enabled {
		writeDownloadTicketHashField(hasher, "1")
	} else {
		writeDownloadTicketHashField(hasher, "0")
	}
	writeDownloadTicketHashField(hasher, string(binary.BigEndian.AppendUint64(nil, uint64(share.MaxAccess))))
	writeDownloadTicketHashField(hasher, string(binary.BigEndian.AppendUint64(nil, share.ticketRevision)))
	var result [downloadTicketHashSize]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func writeDownloadTicketHashField(w io.Writer, value string) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = w.Write(size[:])
	_, _ = io.WriteString(w, value)
}
