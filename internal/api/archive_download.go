package api

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/storage"
)

const (
	maxDownloadArchiveEntries        = 10000
	defaultDownloadArchiveConcurrent = 4
	downloadArchiveRetryAfterSeconds = 1
)

var maxDownloadArchiveBytes = int64(20 * 1024 * 1024 * 1024)

var (
	errDownloadArchiveTooManyEntries  = errors.New("archive contains too many entries")
	errDownloadArchiveTooLarge        = errors.New("archive content is too large")
	errUnsupportedDownloadArchive     = errors.New("unsupported archive format")
	errDownloadArchiveDuplicateEntry  = errors.New("archive contains duplicate entry names")
	errDownloadArchiveMissingMetadata = errors.New("archive entry missing metadata")
	errDownloadArchiveSnapshotChanged = errors.New("archive entry snapshot changed")
)

var downloadArchiveWriter = func(s *Server, ctx context.Context, zipWriter *zip.Writer, entries []downloadArchiveEntry) error {
	return s.writeDownloadArchive(ctx, zipWriter, entries)
}

var downloadArchiveSnapshotOpener = func(s *Server, ctx context.Context, filePath string) (*os.File, *storage.FileInfo, error) {
	return s.fs.OpenFileSnapshotMetadata(ctx, filePath)
}

type downloadArchiveInternalError struct {
	operation string
	err       error
}

func (e downloadArchiveInternalError) Error() string {
	operation := strings.TrimSpace(e.operation)
	if operation == "" {
		return "download archive operation failed"
	}
	return operation + " failed"
}

func (e downloadArchiveInternalError) Unwrap() error {
	return e.err
}

func newDownloadArchiveInternalError(operation string, err error) error {
	if err == nil {
		err = errors.New("unknown download archive error")
	}
	return downloadArchiveInternalError{
		operation: operation,
		err:       err,
	}
}

type downloadArchiveEntry struct {
	sourcePath string
	zipName    string
	info       *storage.FileInfo
}

func downloadArchiveFormatFromRequest(r *http.Request) (string, error) {
	value, err := singleQueryValue(r.URL.Query(), "archive")
	if err != nil {
		return "", errUnsupportedDownloadArchive
	}
	archiveFormat := strings.TrimSpace(value)
	if archiveFormat == "" {
		return "", nil
	}
	if archiveFormat != "zip" {
		return "", errUnsupportedDownloadArchive
	}
	return archiveFormat, nil
}

func (s *Server) handleDownloadArchive(w http.ResponseWriter, r *http.Request, rootPath string) {
	release, ok := s.acquireDownloadArchive(w)
	if !ok {
		return
	}
	defer release()

	entries, err := s.collectDownloadArchiveEntries(r.Context(), rootPath)
	if err != nil {
		s.respondDownloadArchiveError(w, "collect download archive", err)
		return
	}
	if err := validateDownloadArchiveEntries(entries); err != nil {
		s.respondDownloadArchiveError(w, "validate download archive", err)
		return
	}
	if isDownloadProbeRequest(r) {
		if err := s.preflightDownloadArchiveSnapshots(r.Context(), entries); err != nil {
			s.respondDownloadArchiveError(w, "preflight download archive", err)
			return
		}
	}

	setUntrustedAttachmentDownloadHeaders(w)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", formatAttachmentHeader(downloadArchiveFilename(rootPath)))
	if isDownloadProbeRequest(r) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}

	trackingWriter := &apiDownloadResponseWriter{ResponseWriter: w}
	zipWriter := zip.NewWriter(trackingWriter)
	if err := downloadArchiveWriter(s, r.Context(), zipWriter, entries); err != nil {
		s.logger.Error().Err(err).Str("path", rootPath).Msg("write download archive failed")
		if !trackingWriter.started {
			clearDownloadArchiveHeaders(w)
			s.respondDownloadArchiveError(w, "write download archive", err)
			return
		}
		return
	}
	if err := zipWriter.Close(); err != nil {
		s.logger.Error().Err(err).Str("path", rootPath).Msg("close download archive failed")
		if !trackingWriter.started {
			clearDownloadArchiveHeaders(w)
			s.respondDownloadArchiveError(w, "close download archive", err)
			return
		}
		return
	}

	if trackingWriter.writeErr == nil && trackingWriter.statusCode < http.StatusBadRequest {
		s.LogActivity(r, activity.ActionDownload, rootPath, map[string]string{
			"archive": "zip",
			"entries": strconv.Itoa(len(entries)),
		})
	}
}

func (s *Server) acquireDownloadArchive(w http.ResponseWriter) (func(), bool) {
	if s == nil || s.downloadArchiveGate == nil {
		ServiceUnavailable(w, "archive downloads unavailable")
		return nil, false
	}

	select {
	case s.downloadArchiveGate <- struct{}{}:
		return func() { <-s.downloadArchiveGate }, true
	default:
		w.Header().Set("Retry-After", strconv.Itoa(downloadArchiveRetryAfterSeconds))
		_ = NewAPIError(ErrCodeArchiveDownloadRateLimited, "too many archive downloads, try later").Write(w, http.StatusTooManyRequests)
		return nil, false
	}
}

func (s *Server) collectDownloadArchiveEntries(ctx context.Context, rootPath string) ([]downloadArchiveEntry, error) {
	info, err := s.fs.StatMetadata(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rootName, err := safeDownloadArchiveEntryName(downloadArchiveRootName(rootPath))
	if err != nil {
		return nil, err
	}

	collector := &downloadArchiveCollector{
		server:     s,
		ctx:        ctx,
		discovered: 1,
		maxEntries: maxDownloadArchiveEntries,
	}
	if info.IsDir {
		if err := collector.walkDirectory(rootPath, rootName, info); err != nil {
			return nil, err
		}
		return collector.entries, nil
	}

	if err := collector.addFile(rootPath, rootName, info); err != nil {
		return nil, err
	}
	return collector.entries, nil
}

type downloadArchiveCollector struct {
	server     *Server
	ctx        context.Context
	entries    []downloadArchiveEntry
	totalBytes int64
	discovered int
	maxEntries int
}

func (c *downloadArchiveCollector) walkDirectory(sourcePath, zipName string, info *storage.FileInfo) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := c.addDirectory(sourcePath, zipName, info); err != nil {
		return err
	}

	remaining := c.maxEntries - c.discovered
	if remaining < 0 {
		return errDownloadArchiveTooManyEntries
	}
	children, err := c.server.fs.ReadDirLimit(c.ctx, sourcePath, remaining+1)
	if err != nil {
		return err
	}
	if len(children) > remaining {
		return errDownloadArchiveTooManyEntries
	}
	c.discovered += len(children)
	for _, child := range children {
		if child == nil {
			continue
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		childPath, childBaseName, err := downloadArchiveChildPath(sourcePath, child)
		if err != nil {
			return err
		}
		if err := c.server.authorizeUserConcreteReadPath(c.ctx, childPath); err != nil {
			return err
		}
		childName, err := safeDownloadArchiveEntryName(path.Join(zipName, childBaseName))
		if err != nil {
			return err
		}
		if child.IsDir {
			if err := c.walkDirectory(childPath, childName, child); err != nil {
				return err
			}
			continue
		}
		if err := c.addFile(childPath, childName, child); err != nil {
			return err
		}
	}
	return nil
}

func downloadArchiveChildPath(sourcePath string, child *storage.FileInfo) (string, string, error) {
	if child == nil {
		return "", "", storage.ErrNotFound
	}
	cleanSource := path.Clean(sourcePath)
	childPath := child.Path
	if childPath == "" {
		childName, err := safeDownloadArchiveFallbackChildName(child.Name)
		if err != nil {
			return "", "", err
		}
		if cleanSource == "/" {
			childPath = "/" + childName
		} else {
			childPath = cleanSource + "/" + childName
		}
	}
	if strings.Contains(childPath, "\\") || containsDownloadArchivePathControlCharacter(childPath) || hasDotSegment(childPath) {
		return "", "", errInvalidPath
	}
	cleanChild := path.Clean(childPath)
	if cleanChild == cleanSource || path.Dir(cleanChild) != cleanSource {
		return "", "", errInvalidPath
	}
	return cleanChild, path.Base(cleanChild), nil
}

func safeDownloadArchiveFallbackChildName(name string) (string, error) {
	childName := strings.ReplaceAll(name, "\\", "/")
	if childName == "" || strings.Contains(childName, "/") || containsDownloadArchivePathControlCharacter(childName) || hasDotSegment(childName) {
		return "", errInvalidPath
	}
	return childName, nil
}

func (c *downloadArchiveCollector) addDirectory(sourcePath, zipName string, info *storage.FileInfo) error {
	if len(c.entries)+1 > maxDownloadArchiveEntries {
		return errDownloadArchiveTooManyEntries
	}
	c.entries = append(c.entries, downloadArchiveEntry{
		sourcePath: sourcePath,
		zipName:    strings.TrimRight(zipName, "/") + "/",
		info:       info,
	})
	return nil
}

func (c *downloadArchiveCollector) addFile(sourcePath, zipName string, info *storage.FileInfo) error {
	if len(c.entries)+1 > maxDownloadArchiveEntries {
		return errDownloadArchiveTooManyEntries
	}
	if info.Size < 0 || c.totalBytes > maxDownloadArchiveBytes-info.Size {
		return errDownloadArchiveTooLarge
	}
	c.totalBytes += info.Size
	c.entries = append(c.entries, downloadArchiveEntry{
		sourcePath: sourcePath,
		zipName:    zipName,
		info:       info,
	})
	return nil
}

func (s *Server) writeDownloadArchive(ctx context.Context, zipWriter *zip.Writer, entries []downloadArchiveEntry) error {
	var totalBytes int64
	seenNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info == nil {
			return newDownloadArchiveInternalError("prepare archive entry", errDownloadArchiveMissingMetadata)
		}
		zipName, err := safeDownloadArchiveHeaderName(entry.zipName, entry.info.IsDir)
		if err != nil {
			return err
		}
		if err := rememberDownloadArchiveHeaderName(seenNames, zipName); err != nil {
			return err
		}
		if entry.info.IsDir {
			header := &zip.FileHeader{
				Name:   zipName,
				Method: zip.Store,
			}
			header.SetModTime(entry.info.ModTime)
			header.SetMode(os.ModeDir | 0o755)
			if _, err := zipWriter.CreateHeader(header); err != nil {
				return newDownloadArchiveInternalError("create archive directory", err)
			}
			continue
		}

		file, snapshotInfo, err := downloadArchiveSnapshotOpener(s, ctx, entry.sourcePath)
		if err != nil {
			if errors.Is(err, storage.ErrIsDir) {
				err = errDownloadArchiveSnapshotChanged
			}
			return newDownloadArchiveInternalError("open archive file", err)
		}
		if file == nil || snapshotInfo == nil {
			if file != nil {
				_ = file.Close()
			}
			return newDownloadArchiveInternalError("open archive file", errDownloadArchiveMissingMetadata)
		}
		if snapshotInfo.IsDir {
			_ = file.Close()
			return newDownloadArchiveInternalError("open archive file", errDownloadArchiveSnapshotChanged)
		}
		if snapshotInfo.Size < 0 || totalBytes > maxDownloadArchiveBytes-snapshotInfo.Size {
			_ = file.Close()
			return errDownloadArchiveTooLarge
		}

		header := &zip.FileHeader{
			Name:               zipName,
			Method:             zip.Deflate,
			UncompressedSize64: uint64(snapshotInfo.Size),
		}
		header.SetModTime(snapshotInfo.ModTime)
		header.SetMode(0o644)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			_ = file.Close()
			return newDownloadArchiveInternalError("create archive file", err)
		}
		remaining := maxDownloadArchiveBytes - totalBytes
		written, err := io.Copy(writer, io.LimitReader(file, remaining+1))
		if err != nil {
			_ = file.Close()
			return newDownloadArchiveInternalError("write archive file", err)
		}
		if err := file.Close(); err != nil {
			return newDownloadArchiveInternalError("close archive file", err)
		}
		if written > remaining {
			return errDownloadArchiveTooLarge
		}
		if written != snapshotInfo.Size {
			return errDownloadArchiveSnapshotChanged
		}
		totalBytes += written
	}
	return nil
}

func (s *Server) preflightDownloadArchiveSnapshots(ctx context.Context, entries []downloadArchiveEntry) error {
	var totalBytes int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info == nil {
			return newDownloadArchiveInternalError("prepare archive entry", errDownloadArchiveMissingMetadata)
		}
		if entry.info.IsDir {
			continue
		}

		file, snapshotInfo, err := downloadArchiveSnapshotOpener(s, ctx, entry.sourcePath)
		if err != nil {
			if errors.Is(err, storage.ErrIsDir) {
				err = errDownloadArchiveSnapshotChanged
			}
			return newDownloadArchiveInternalError("open archive preflight file", err)
		}
		if file == nil || snapshotInfo == nil {
			if file != nil {
				_ = file.Close()
			}
			return newDownloadArchiveInternalError("open archive preflight file", errDownloadArchiveMissingMetadata)
		}
		if err := file.Close(); err != nil {
			return newDownloadArchiveInternalError("close archive preflight file", err)
		}
		if snapshotInfo.IsDir {
			return errDownloadArchiveSnapshotChanged
		}
		if !snapshotInfo.Mode.IsRegular() {
			return storage.ErrNotRegular
		}
		if snapshotInfo.Size != entry.info.Size {
			return errDownloadArchiveSnapshotChanged
		}
		if snapshotInfo.Size < 0 || totalBytes > maxDownloadArchiveBytes-snapshotInfo.Size {
			return errDownloadArchiveTooLarge
		}
		totalBytes += snapshotInfo.Size
	}
	return nil
}

func validateDownloadArchiveEntries(entries []downloadArchiveEntry) error {
	seenNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.info == nil {
			return newDownloadArchiveInternalError("prepare archive entry", errDownloadArchiveMissingMetadata)
		}
		if !entry.info.IsDir && !entry.info.Mode.IsRegular() {
			return storage.ErrNotRegular
		}
		zipName, err := safeDownloadArchiveHeaderName(entry.zipName, entry.info.IsDir)
		if err != nil {
			return err
		}
		if err := rememberDownloadArchiveHeaderName(seenNames, zipName); err != nil {
			return err
		}
	}
	return nil
}

func rememberDownloadArchiveHeaderName(seenNames map[string]struct{}, zipName string) error {
	if _, ok := seenNames[zipName]; ok {
		return errDownloadArchiveDuplicateEntry
	}
	seenNames[zipName] = struct{}{}
	return nil
}

func (s *Server) respondDownloadArchiveError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, errDownloadArchiveTooManyEntries):
		NewAPIError(ErrCodePayloadTooLarge, "archive contains too many entries").Write(w, http.StatusRequestEntityTooLarge)
	case errors.Is(err, errDownloadArchiveTooLarge):
		NewAPIError(ErrCodePayloadTooLarge, "archive content is too large").Write(w, http.StatusRequestEntityTooLarge)
	case errors.Is(err, errDownloadArchiveDuplicateEntry):
		Conflict(w, "archive contains duplicate entries")
	case errors.Is(err, errDownloadArchiveSnapshotChanged):
		Conflict(w, "archive entry changed during download")
	case errors.Is(err, errInvalidPath):
		badRequestInvalidPath(w)
	case errors.Is(err, storage.ErrNotDir):
		Conflict(w, "parent path is not a directory")
	case errors.Is(err, storage.ErrNotRegular):
		Conflict(w, "resource is not a regular file")
	case isStorageNotFound(err):
		s.respondNotFound(w, operation, err)
	case errors.Is(err, errPathAccessDenied), errors.Is(err, errPathOutsideHomeDir):
		respondPathAccessError(w, err)
	default:
		s.respondInternalError(w, operation, err)
	}
}

func clearDownloadArchiveHeaders(w http.ResponseWriter) {
	w.Header().Del("Content-Disposition")
	w.Header().Del("Content-Type")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", untrustedEmbeddedErrorContentSecurityPolicy)
}

func safeDownloadArchiveEntryName(name string) (string, error) {
	if strings.Contains(name, "\\") {
		return "", errInvalidPath
	}
	normalized := name
	if normalized == "" || containsDownloadArchivePathControlCharacter(normalized) || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") || hasDotSegment(normalized) {
		return "", errInvalidPath
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errInvalidPath
	}
	return cleaned, nil
}

func containsDownloadArchivePathControlCharacter(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func safeDownloadArchiveHeaderName(name string, isDir bool) (string, error) {
	if strings.Contains(name, "\\") {
		return "", errInvalidPath
	}
	normalized := name
	if isDir {
		trimmed := strings.TrimRight(normalized, "/")
		cleaned, err := safeDownloadArchiveEntryName(trimmed)
		if err != nil {
			return "", err
		}
		return cleaned + "/", nil
	}
	if strings.HasSuffix(normalized, "/") {
		return "", errInvalidPath
	}
	return safeDownloadArchiveEntryName(normalized)
}

func downloadArchiveRootName(rootPath string) string {
	cleaned := path.Clean(rootPath)
	if cleaned == "/" || cleaned == "." {
		return "mnemonas-files"
	}
	return path.Base(cleaned)
}

func downloadArchiveFilename(rootPath string) string {
	rootName := downloadArchiveRootName(rootPath)
	if strings.HasSuffix(strings.ToLower(rootName), ".zip") {
		return rootName
	}
	return rootName + ".zip"
}
