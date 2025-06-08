package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/seanbao/mnemonas/internal/auth"
	"github.com/seanbao/mnemonas/internal/storage"
)

func pathWithinBase(basePath, targetPath string) bool {
	basePath = path.Clean(basePath)
	targetPath = path.Clean(targetPath)
	if basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func (s *Server) currentUserHomeDir(ctx context.Context) (string, bool, error) {
	if !s.authEnabled || auth.IsAdmin(ctx) {
		return "", false, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil {
		return "", false, nil
	}
	if strings.TrimSpace(user.HomeDir) == "" {
		return "", true, errPathOutsideHomeDir
	}

	homeDir, err := validatePath(user.HomeDir)
	if err != nil {
		return "", true, errPathOutsideHomeDir
	}

	return homeDir, true, nil
}

func (s *Server) authorizeUserPath(ctx context.Context, targetPath string) error {
	homeDir, scoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return err
	}
	if !scoped {
		return nil
	}
	if !pathWithinBase(homeDir, targetPath) {
		return errPathOutsideHomeDir
	}
	return nil
}

func (s *Server) currentUserQuota(ctx context.Context) (homeDir string, quotaBytes int64, scoped bool, err error) {
	homeDir, homeScoped, err := s.currentUserHomeDir(ctx)
	if err != nil {
		return "", 0, false, err
	}
	if !homeScoped {
		return "", 0, false, nil
	}

	user := auth.GetUserFromContext(ctx)
	if user == nil || user.QuotaBytes <= 0 {
		return "", 0, false, nil
	}

	return homeDir, user.QuotaBytes, true, nil
}

func (s *Server) resolveUserUsedBytes(ctx context.Context, user *auth.User) (int64, error) {
	if s.fs == nil || user == nil || strings.TrimSpace(user.HomeDir) == "" {
		return 0, nil
	}
	homeDir, err := validatePath(user.HomeDir)
	if err != nil {
		return user.UsedBytes, err
	}
	return s.pathLogicalSizeIfExists(ctx, homeDir)
}

func (s *Server) pathLogicalSizeIfExists(ctx context.Context, targetPath string) (int64, error) {
	size, err := s.pathLogicalSize(ctx, targetPath)
	if isStorageNotFound(err) {
		return 0, nil
	}
	return size, err
}

func (s *Server) pathLogicalSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		return 0, err
	}
	return s.fileInfoLogicalSize(ctx, info)
}

func (s *Server) fileInfoLogicalSize(ctx context.Context, info *storage.FileInfo) (int64, error) {
	if info == nil {
		return 0, nil
	}
	if !info.IsDir {
		return nonNegativeSize(info.Size), nil
	}

	children, err := s.fs.ReadDir(ctx, info.Path)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, child := range children {
		size, err := s.fileInfoLogicalSize(ctx, child)
		if err != nil {
			return 0, err
		}
		total, err = addQuotaSize(total, size)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func addQuotaSize(left, right int64) (int64, error) {
	if right <= 0 {
		return left, nil
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if left > maxInt64-right {
		return 0, errors.New("logical file size overflow")
	}
	return left + right, nil
}

func nonNegativeSize(size int64) int64 {
	if size < 0 {
		return 0
	}
	return size
}

func (s *Server) quotaCheckedUploadReader(ctx context.Context, targetPath string, reader io.Reader, contentLength int64) (io.Reader, func(), error) {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !quotaScoped {
		return reader, func() {}, nil
	}

	s.quotaMu.Lock()
	unlock := func() {
		s.quotaMu.Unlock()
	}

	usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
	if err != nil {
		unlock()
		return nil, nil, err
	}

	replacedBytes, err := s.existingUploadTargetSize(ctx, targetPath)
	if err != nil {
		unlock()
		return nil, nil, err
	}

	baseUsed := usedBytes - replacedBytes
	if baseUsed < 0 {
		baseUsed = 0
	}
	availableBytes := quotaBytes - baseUsed
	if availableBytes < 0 {
		availableBytes = 0
	}

	if contentLength >= 0 && contentLength > availableBytes {
		unlock()
		return nil, nil, newQuotaExceededError(usedBytes, quotaBytes, contentLength, availableBytes)
	}

	return &quotaLimitedReader{
		reader:    reader,
		remaining: availableBytes,
		err:       newQuotaExceededError(usedBytes, quotaBytes, quotaStreamRequiredBytes(availableBytes), availableBytes),
	}, unlock, nil
}

func (s *Server) existingUploadTargetSize(ctx context.Context, targetPath string) (int64, error) {
	info, err := s.fs.Stat(ctx, targetPath)
	if err != nil {
		if isStorageNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir {
		return 0, storage.ErrIsDir
	}
	return nonNegativeSize(info.Size), nil
}

func (s *Server) copyResourceWithQuota(ctx context.Context, srcPath, dstPath string) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	if !quotaScoped {
		return s.copyResource(ctx, srcPath, dstPath)
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
	if err != nil {
		return err
	}
	requiredBytes, err := s.pathLogicalSize(ctx, srcPath)
	if err != nil {
		return err
	}
	if err := ensureQuotaAvailable(usedBytes, quotaBytes, requiredBytes); err != nil {
		return err
	}

	return s.copyResource(ctx, srcPath, dstPath)
}

func (s *Server) restoreFromTrashWithQuota(ctx context.Context, item *storage.TrashItem, restore func() error) error {
	homeDir, quotaBytes, quotaScoped, err := s.currentUserQuota(ctx)
	if err != nil {
		return err
	}
	if !quotaScoped {
		return restore()
	}

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()

	usedBytes, err := s.pathLogicalSizeIfExists(ctx, homeDir)
	if err != nil {
		return err
	}
	requiredBytes := int64(0)
	if item != nil {
		requiredBytes = nonNegativeSize(item.Size)
	}
	if err := ensureQuotaAvailable(usedBytes, quotaBytes, requiredBytes); err != nil {
		return err
	}

	return restore()
}

func ensureQuotaAvailable(usedBytes, quotaBytes, requiredBytes int64) error {
	requiredBytes = nonNegativeSize(requiredBytes)
	availableBytes := quotaBytes - usedBytes
	if availableBytes < 0 {
		availableBytes = 0
	}
	if requiredBytes > availableBytes {
		return newQuotaExceededError(usedBytes, quotaBytes, requiredBytes, availableBytes)
	}
	return nil
}

func newQuotaExceededError(usedBytes, quotaBytes, requiredBytes, availableBytes int64) *quotaExceededError {
	if usedBytes < 0 {
		usedBytes = 0
	}
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	if requiredBytes < 0 {
		requiredBytes = 0
	}
	if availableBytes < 0 {
		availableBytes = 0
	}
	return &quotaExceededError{
		UsedBytes:      usedBytes,
		QuotaBytes:     quotaBytes,
		RequiredBytes:  requiredBytes,
		AvailableBytes: availableBytes,
	}
}

func quotaStreamRequiredBytes(availableBytes int64) int64 {
	const maxInt64 = int64(^uint64(0) >> 1)
	if availableBytes >= maxInt64 {
		return maxInt64
	}
	return availableBytes + 1
}

type quotaLimitedReader struct {
	reader    io.Reader
	remaining int64
	err       error
}

func (r *quotaLimitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, r.err
		}
		if err != nil {
			return 0, err
		}
		return 0, nil
	}

	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

type quotaExceededError struct {
	UsedBytes      int64 `json:"used_bytes"`
	QuotaBytes     int64 `json:"quota_bytes"`
	RequiredBytes  int64 `json:"required_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
}

func (e *quotaExceededError) Error() string {
	return "user quota exceeded"
}

func respondQuotaExceeded(w http.ResponseWriter, err error) {
	details := any(nil)
	var quotaErr *quotaExceededError
	if errors.As(err, &quotaErr) {
		details = quotaErr
	}
	apiErr := NewAPIError(ErrCodeQuotaExceeded, "user quota exceeded")
	if details != nil {
		apiErr = apiErr.WithDetails(details)
	}
	apiErr.Write(w, http.StatusInsufficientStorage)
}
