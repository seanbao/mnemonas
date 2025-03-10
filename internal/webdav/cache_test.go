// Package webdav provides WebDAV protocol HTTP handler
package webdav

import (
	"testing"
	"time"

	"github.com/seanbao/mnemonas/internal/storage"
)

func TestPropfindCache_GetSet(t *testing.T) {
	cache := NewPropfindCache(time.Second, 100)

	responses := []propfindResponse{
		{Href: "/test", Propstat: propstat{Status: "HTTP/1.1 200 OK"}},
	}

	cache.Set("/test", "1", responses)

	got, ok := cache.Get("/test", "1")
	if !ok {
		t.Error("Get should return cached value")
	}
	if len(got) != 1 {
		t.Errorf("Got %d responses, want 1", len(got))
	}
}

func TestPropfindCache_SetDeepCopiesResponses(t *testing.T) {
	cache := NewPropfindCache(time.Second, 100)

	responses := []propfindResponse{
		{
			Href: "/dir/",
			Propstat: propstat{
				Status: "HTTP/1.1 200 OK",
				Prop: prop{
					DisplayName:  "dir",
					ResourceType: &resourceType{Collection: &struct{}{}},
				},
			},
		},
	}

	cache.Set("/dir", "1", responses)

	responses[0].Href = "/mutated/"
	responses[0].Propstat.Prop.DisplayName = "mutated"
	responses[0].Propstat.Prop.ResourceType.Collection = nil

	got, ok := cache.Get("/dir", "1")
	if !ok {
		t.Fatal("Get should return cached value")
	}
	if got[0].Href != "/dir/" {
		t.Fatalf("Href = %q, want /dir/", got[0].Href)
	}
	if got[0].Propstat.Prop.DisplayName != "dir" {
		t.Fatalf("DisplayName = %q, want dir", got[0].Propstat.Prop.DisplayName)
	}
	if got[0].Propstat.Prop.ResourceType == nil || got[0].Propstat.Prop.ResourceType.Collection == nil {
		t.Fatal("ResourceType collection should remain cached after caller mutation")
	}
}

func TestPropfindCache_GetReturnsDeepCopy(t *testing.T) {
	cache := NewPropfindCache(time.Second, 100)

	cache.Set("/dir", "1", []propfindResponse{
		{
			Href: "/dir/",
			Propstat: propstat{
				Status: "HTTP/1.1 200 OK",
				Prop: prop{
					DisplayName:  "dir",
					ResourceType: &resourceType{Collection: &struct{}{}},
				},
			},
		},
	})

	got, ok := cache.Get("/dir", "1")
	if !ok {
		t.Fatal("Get should return cached value")
	}

	got[0].Href = "/mutated/"
	got[0].Propstat.Prop.DisplayName = "mutated"
	got[0].Propstat.Prop.ResourceType.Collection = nil

	again, ok := cache.Get("/dir", "1")
	if !ok {
		t.Fatal("Get should still return cached value")
	}
	if again[0].Href != "/dir/" {
		t.Fatalf("Href after caller mutation = %q, want /dir/", again[0].Href)
	}
	if again[0].Propstat.Prop.DisplayName != "dir" {
		t.Fatalf("DisplayName after caller mutation = %q, want dir", again[0].Propstat.Prop.DisplayName)
	}
	if again[0].Propstat.Prop.ResourceType == nil || again[0].Propstat.Prop.ResourceType.Collection == nil {
		t.Fatal("ResourceType collection should remain cached after Get mutation")
	}
}

func TestPropfindCache_Miss(t *testing.T) {
	cache := NewPropfindCache(time.Second, 100)

	_, ok := cache.Get("/nonexistent", "1")
	if ok {
		t.Error("Get should return false for non-cached value")
	}
}

func TestPropfindCache_Expiration(t *testing.T) {
	cache := NewPropfindCache(50*time.Millisecond, 100)

	responses := []propfindResponse{
		{Href: "/test"},
	}

	cache.Set("/test", "1", responses)

	// Should be cached
	_, ok := cache.Get("/test", "1")
	if !ok {
		t.Error("Get should return cached value before expiration")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	_, ok = cache.Get("/test", "1")
	if ok {
		t.Error("Get should return false after expiration")
	}
}

func TestPropfindCache_Invalidate(t *testing.T) {
	cache := NewPropfindCache(time.Minute, 100)

	cache.Set("/parent", "1", []propfindResponse{{Href: "/parent"}})
	cache.Set("/parent/child", "1", []propfindResponse{{Href: "/parent/child"}})
	cache.Set("/other", "1", []propfindResponse{{Href: "/other"}})

	cache.Invalidate("/parent")

	// Parent and child should be invalidated
	if _, ok := cache.Get("/parent", "1"); ok {
		t.Error("Parent should be invalidated")
	}
	if _, ok := cache.Get("/parent/child", "1"); ok {
		t.Error("Child should be invalidated")
	}

	// Other should still exist
	if _, ok := cache.Get("/other", "1"); !ok {
		t.Error("Other should not be invalidated")
	}
}

func TestPropfindCache_InvalidateChildAlsoClearsAncestors(t *testing.T) {
	cache := NewPropfindCache(time.Minute, 100)

	cache.Set("/", "1", []propfindResponse{{Href: "/"}})
	cache.Set("/parent", "infinity", []propfindResponse{{Href: "/parent"}})
	cache.Set("/parent/child", "1", []propfindResponse{{Href: "/parent/child"}})
	cache.Set("/sibling", "1", []propfindResponse{{Href: "/sibling"}})

	cache.Invalidate("/parent/child")

	if _, ok := cache.Get("/", "1"); ok {
		t.Error("Ancestor root entry should be invalidated")
	}
	if _, ok := cache.Get("/parent", "infinity"); ok {
		t.Error("Ancestor directory entry should be invalidated")
	}
	if _, ok := cache.Get("/parent/child", "1"); ok {
		t.Error("Changed path entry should be invalidated")
	}
	if _, ok := cache.Get("/sibling", "1"); !ok {
		t.Error("Unrelated sibling entry should not be invalidated")
	}
}

func TestPropfindCache_InvalidateAll(t *testing.T) {
	cache := NewPropfindCache(time.Minute, 100)

	cache.Set("/a", "1", []propfindResponse{{Href: "/a"}})
	cache.Set("/b", "1", []propfindResponse{{Href: "/b"}})

	cache.InvalidateAll()

	if _, ok := cache.Get("/a", "1"); ok {
		t.Error("All entries should be invalidated")
	}
	if _, ok := cache.Get("/b", "1"); ok {
		t.Error("All entries should be invalidated")
	}
}

func TestPropfindCache_Eviction(t *testing.T) {
	cache := NewPropfindCache(time.Minute, 10)

	// Fill cache beyond capacity
	for i := 0; i < 15; i++ {
		path := "/test" + string(rune('a'+i))
		cache.Set(path, "1", []propfindResponse{{Href: path}})
	}

	size, _ := cache.Stats()
	if size > 10 {
		t.Errorf("Cache size = %d, should be <= 10", size)
	}
}

func TestPropfindCache_Stats(t *testing.T) {
	cache := NewPropfindCache(50*time.Millisecond, 100)

	cache.Set("/a", "1", []propfindResponse{{Href: "/a"}})
	cache.Set("/b", "1", []propfindResponse{{Href: "/b"}})

	size, expired := cache.Stats()
	if size != 2 {
		t.Errorf("Size = %d, want 2", size)
	}
	if expired != 0 {
		t.Errorf("Expired = %d, want 0", expired)
	}

	time.Sleep(60 * time.Millisecond)

	size, expired = cache.Stats()
	if size != 2 {
		t.Errorf("Size after expiry = %d, want 2 (entries remain until accessed)", size)
	}
	if expired != 2 {
		t.Errorf("Expired = %d, want 2", expired)
	}
}

func TestPropfindCache_DepthKey(t *testing.T) {
	cache := NewPropfindCache(time.Minute, 100)

	cache.Set("/test", "0", []propfindResponse{{Href: "/test-0"}})
	cache.Set("/test", "1", []propfindResponse{{Href: "/test-1"}})

	resp0, ok := cache.Get("/test", "0")
	if !ok || resp0[0].Href != "/test-0" {
		t.Error("Depth 0 cache should be separate")
	}

	resp1, ok := cache.Get("/test", "1")
	if !ok || resp1[0].Href != "/test-1" {
		t.Error("Depth 1 cache should be separate")
	}
}

func TestBuildPropfindResponses(t *testing.T) {
	info := &storage.FileInfo{
		Path:        "/testdir",
		IsDir:       true,
		ModTime:     time.Now(),
		ContentHash: "",
	}

	children := []*storage.FileInfo{
		{Path: "/testdir/file1.txt", IsDir: false, Size: 100, ContentHash: "abc123"},
		{Path: "/testdir/file2.txt", IsDir: false, Size: 200, ContentHash: "def456"},
	}

	responses := BuildPropfindResponses("/dav", "/testdir", info, children, "1")

	if len(responses) != 3 {
		t.Errorf("Got %d responses, want 3", len(responses))
	}

	// Check directory response
	if responses[0].Href != "/dav/testdir/" {
		t.Errorf("Dir href = %q, want '/dav/testdir/'", responses[0].Href)
	}

	// Check file responses
	if responses[1].Propstat.Prop.GetContentLength != 100 {
		t.Error("First file should have size 100")
	}
}

func TestBuildPropfindResponses_Depth0(t *testing.T) {
	info := &storage.FileInfo{
		Path:  "/testdir",
		IsDir: true,
	}

	children := []*storage.FileInfo{
		{Path: "/testdir/file.txt"},
	}

	responses := BuildPropfindResponses("", "/testdir", info, children, "0")

	if len(responses) != 1 {
		t.Errorf("Depth 0 should only return current resource, got %d", len(responses))
	}
}

func TestBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/foo/bar/baz", "baz"},
		{"/foo", "foo"},
		{"foo", "foo"},
		{"/", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := baseName(tt.input)
		if got != tt.want {
			t.Errorf("baseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
