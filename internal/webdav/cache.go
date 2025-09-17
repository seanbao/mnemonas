// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

// PropfindCache caches PROPFIND results for large directories
type PropfindCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	maxSize int
	gen     uint64
}

type cacheEntry struct {
	responses []propfindResponse
	cachedAt  time.Time
}

func cloneResourceType(src *resourceType) *resourceType {
	if src == nil {
		return nil
	}
	cloned := &resourceType{}
	if src.Collection != nil {
		cloned.Collection = &struct{}{}
	}
	return cloned
}

func clonePropfindResponses(responses []propfindResponse) []propfindResponse {
	if len(responses) == 0 {
		return nil
	}

	cloned := make([]propfindResponse, len(responses))
	for i, response := range responses {
		cloned[i] = response
		cloned[i].Propstat.Prop.ResourceType = cloneResourceType(response.Propstat.Prop.ResourceType)
	}

	return cloned
}

// NewPropfindCache creates a new PROPFIND cache
func NewPropfindCache(ttl time.Duration, maxSize int) *PropfindCache {
	if ttl == 0 {
		ttl = 30 * time.Second // Default 30 seconds
	}
	if maxSize == 0 {
		maxSize = 1000 // Default 1000 entries
	}
	return &PropfindCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// cacheKey generates a cache key from path and depth
func cacheKey(path, depth string) string {
	return path + "|" + depth
}

// Get retrieves cached PROPFIND responses
func (c *PropfindCache) Get(path, depth string) ([]propfindResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(path, depth)
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	// Check if expired
	if time.Since(entry.cachedAt) > c.ttl {
		return nil, false
	}

	return clonePropfindResponses(entry.responses), true
}

// SnapshotGeneration returns the current invalidation generation.
func (c *PropfindCache) SnapshotGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// Set stores PROPFIND responses in cache
func (c *PropfindCache) Set(path, depth string, responses []propfindResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(path, depth, responses)
}

// SetIfUnchanged stores responses only if no invalidation happened since generation was observed.
func (c *PropfindCache) SetIfUnchanged(path, depth string, responses []propfindResponse, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.gen != generation {
		return false
	}

	c.setLocked(path, depth, responses)
	return true
}

func (c *PropfindCache) setLocked(path, depth string, responses []propfindResponse) {

	// Evict oldest entries if cache is full
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	key := cacheKey(path, depth)
	c.entries[key] = &cacheEntry{
		responses: clonePropfindResponses(responses),
		cachedAt:  time.Now(),
	}
}

// Invalidate removes a cached entry
func (c *PropfindCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++

	for key := range c.entries {
		cachedPath, _, ok := strings.Cut(key, "|")
		if !ok {
			cachedPath = key
		}

		if affectsCachedPropfind(path, cachedPath) {
			delete(c.entries, key)
		}
	}
}

func affectsCachedPropfind(changedPath, cachedPath string) bool {
	if changedPath == cachedPath {
		return true
	}

	return isDescendantPath(changedPath, cachedPath) || isDescendantPath(cachedPath, changedPath)
}

// InvalidateAll clears the entire cache
func (c *PropfindCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = make(map[string]*cacheEntry)
}

// evictOldest removes the oldest 10% of entries
func (c *PropfindCache) evictOldest() {
	if len(c.entries) == 0 {
		return
	}

	// Find and remove oldest entries
	toRemove := len(c.entries) / 10
	if toRemove < 1 {
		toRemove = 1
	}

	type kv struct {
		key      string
		cachedAt time.Time
	}

	var oldest []kv
	for k, v := range c.entries {
		oldest = append(oldest, kv{k, v.cachedAt})
	}

	sort.Slice(oldest, func(i, j int) bool {
		if oldest[i].cachedAt.Equal(oldest[j].cachedAt) {
			return oldest[i].key < oldest[j].key
		}
		return oldest[i].cachedAt.Before(oldest[j].cachedAt)
	})

	// Remove oldest entries
	for i := 0; i < toRemove && i < len(oldest); i++ {
		delete(c.entries, oldest[i].key)
	}
}

// Stats returns cache statistics
func (c *PropfindCache) Stats() (size int, expired int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	for _, entry := range c.entries {
		if now.Sub(entry.cachedAt) > c.ttl {
			expired++
		}
	}
	return len(c.entries), expired
}

// BuildPropfindResponses builds PROPFIND responses from file info
// Helper function for caching
func BuildPropfindResponses(prefix, filePath string, info *storage.FileInfo, children []*storage.FileInfo, depth string) []propfindResponse {
	var responses []propfindResponse

	// Add current resource
	responses = append(responses, buildPropResponse(prefix, filePath, info))

	// If directory and depth is not 0, add children
	if info.IsDir && depth != "0" {
		for _, child := range children {
			responses = append(responses, buildPropResponse(prefix, child.Path, child))
		}
	}

	return responses
}

func buildPropResponse(prefix, filePath string, info *storage.FileInfo) propfindResponse {
	href := filePath
	if prefix != "" {
		href = prefix + filePath
	}
	if info.IsDir && len(href) > 0 && href[len(href)-1] != '/' {
		href += "/"
	}

	props := propstat{
		Status: "HTTP/1.1 200 OK",
		Prop: prop{
			DisplayName:     baseName(filePath),
			GetLastModified: info.ModTime.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		},
	}

	if info.IsDir {
		props.Prop.ResourceType = &resourceType{Collection: &struct{}{}}
	} else {
		props.Prop.GetContentLength = info.Size
		if info.ContentHash != "" {
			props.Prop.GetETag = `"` + info.ContentHash + `"`
		}
	}

	return propfindResponse{
		Href:     href,
		Propstat: props,
	}
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
