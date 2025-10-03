package api

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/seanbao/mnemonas/internal/activity"
	"github.com/seanbao/mnemonas/internal/storage"
)

const maxDownloadArchiveEntries = 10000

var maxDownloadArchiveBytes = int64(20 * 1024 * 1024 * 1024)

var (
	errDownloadArchiveTooManyEntries = errors.New("archive contains too many entries")
	errDownloadArchiveTooLarge       = errors.New("archive content is too large")
	errUnsupportedDownloadArchive    = errors.New("unsupported archive format")
)

var downloadArchiveWriter = func(s *Server, ctx context.Context, zipWriter *zip.Writer, entries []downloadArchiveEntry) error {
	return s.writeDownloadArchive(ctx, zipWriter, entries)
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
	entries, err := s.collectDownloadArchiveEntries(r.Context(), rootPath)
	if err != nil {
		s.respondDownloadArchiveError(w, "collect download archive", err)
		return
	}

	setUntrustedDownloadHeaders(w)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", formatAttachmentHeader(downloadArchiveFilename(rootPath)))

	trackingWriter := &apiDownloadResponseWriter{ResponseWriter: w}
	zipWriter := zip.NewWriter(trackingWriter)
	if err := downloadArchiveWriter(s, r.Context(), zipWriter, entries); err != nil {
		s.logger.Error().Err(err).Str("path", rootPath).Msg("write download archive failed")
		if !trackingWriter.started {
			clearDownloadArchiveHeaders(w)
			s.respondDownloadArchiveError(w, "write download archive", err)
			return
		}
		_ = zipWriter.Close()
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

func (s *Server) collectDownloadArchiveEntries(ctx context.Context, rootPath string) ([]downloadArchiveEntry, error) {
	info, err := s.fs.Stat(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	rootName, err := safeDownloadArchiveEntryName(downloadArchiveRootName(rootPath))
	if err != nil {
		return nil, err
	}

	collector := &downloadArchiveCollector{
		server: s,
		ctx:    ctx,
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
}

func (c *downloadArchiveCollector) walkDirectory(sourcePath, zipName string, info *storage.FileInfo) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if err := c.addDirectory(sourcePath, zipName, info); err != nil {
		return err
	}

	children, err := c.server.fs.ReadDir(c.ctx, sourcePath)
	if err != nil {
		return err
	}
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
	if strings.TrimSpace(childPath) == "" {
		childName := strings.ReplaceAll(child.Name, "\\", "/")
		if strings.ContainsRune(childName, '\x00') || hasDotSegment(childName) {
			return "", "", errInvalidPath
		}
		if cleanSource == "/" {
			childPath = "/" + childName
		} else {
			childPath = cleanSource + "/" + childName
		}
	}
	normalizedChildPath := strings.ReplaceAll(childPath, "\\", "/")
	if strings.ContainsRune(normalizedChildPath, '\x00') || hasDotSegment(normalizedChildPath) {
		return "", "", errInvalidPath
	}
	cleanChild := path.Clean(normalizedChildPath)
	if cleanChild == cleanSource || path.Dir(cleanChild) != cleanSource {
		return "", "", errInvalidPath
	}
	return cleanChild, path.Base(cleanChild), nil
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
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info == nil {
			return fmt.Errorf("archive entry %s missing metadata", entry.sourcePath)
		}
		if entry.info.IsDir {
			header := &zip.FileHeader{
				Name:   entry.zipName,
				Method: zip.Store,
			}
			header.SetModTime(entry.info.ModTime)
			header.SetMode(os.ModeDir | 0o755)
			if _, err := zipWriter.CreateHeader(header); err != nil {
				return fmt.Errorf("create archive directory %s: %w", entry.zipName, err)
			}
			continue
		}

		file, snapshotInfo, err := s.fs.OpenFileSnapshot(ctx, entry.sourcePath)
		if err != nil {
			return fmt.Errorf("open archive file %s: %w", entry.sourcePath, err)
		}
		if snapshotInfo.IsDir {
			_ = file.Close()
			return fmt.Errorf("archive file became a directory: %s", entry.sourcePath)
		}
		if snapshotInfo.Size < 0 || totalBytes > maxDownloadArchiveBytes-snapshotInfo.Size {
			_ = file.Close()
			return errDownloadArchiveTooLarge
		}

		header := &zip.FileHeader{
			Name:               entry.zipName,
			Method:             zip.Deflate,
			UncompressedSize64: uint64(snapshotInfo.Size),
		}
		header.SetModTime(snapshotInfo.ModTime)
		header.SetMode(0o644)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("create archive file %s: %w", entry.zipName, err)
		}
		remaining := maxDownloadArchiveBytes - totalBytes
		written, err := io.Copy(writer, io.LimitReader(file, remaining+1))
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("write archive file %s: %w", entry.zipName, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close archive file %s: %w", entry.sourcePath, err)
		}
		if written > remaining {
			return errDownloadArchiveTooLarge
		}
		totalBytes += written
	}
	return nil
}

func (s *Server) respondDownloadArchiveError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, errDownloadArchiveTooManyEntries):
		NewAPIError(ErrCodePayloadTooLarge, "archive contains too many entries").Write(w, http.StatusRequestEntityTooLarge)
	case errors.Is(err, errDownloadArchiveTooLarge):
		NewAPIError(ErrCodePayloadTooLarge, "archive content is too large").Write(w, http.StatusRequestEntityTooLarge)
	case errors.Is(err, errInvalidPath):
		badRequestInvalidPath(w)
	case errors.Is(err, storage.ErrNotDir):
		Conflict(w, "parent path is not a directory")
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
}

func safeDownloadArchiveEntryName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if normalized == "" || strings.ContainsRune(normalized, '\x00') || strings.HasPrefix(normalized, "/") || hasDotSegment(normalized) {
		return "", errInvalidPath
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errInvalidPath
	}
	return cleaned, nil
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
