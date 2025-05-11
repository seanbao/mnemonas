package api

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/share"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestRelocateDeletedPathRestorePath(t *testing.T) {
	tests := []struct {
		name         string
		sourcePath   string
		restoredPath string
		currentPath  string
		wantPath     string
		wantOK       bool
	}{
		{
			name:         "exact deleted path moves to restore target",
			sourcePath:   "/docs",
			restoredPath: "/restore/docs",
			currentPath:  "/docs",
			wantPath:     "/restore/docs",
			wantOK:       true,
		},
		{
			name:         "descendant keeps relative suffix",
			sourcePath:   "/docs",
			restoredPath: "/restore/docs",
			currentPath:  "/docs/reports/q1.txt",
			wantPath:     "/restore/docs/reports/q1.txt",
			wantOK:       true,
		},
		{
			name:         "sibling prefix is not treated as descendant",
			sourcePath:   "/docs",
			restoredPath: "/restore/docs",
			currentPath:  "/docs-archive/q1.txt",
			wantOK:       false,
		},
		{
			name:         "root restore keeps child paths under target",
			sourcePath:   "/",
			restoredPath: "/restore/root",
			currentPath:  "/photos/img.jpg",
			wantPath:     "/restore/root/photos/img.jpg",
			wantOK:       true,
		},
		{
			name:         "windows separators are normalized",
			sourcePath:   `\docs`,
			restoredPath: `/restore/docs`,
			currentPath:  `\docs\notes\todo.txt`,
			wantPath:     "/restore/docs/notes/todo.txt",
			wantOK:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOK := relocateDeletedPathRestorePath(tt.sourcePath, tt.restoredPath, tt.currentPath)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func TestRelocateDeletedPathRestoreState_ClonesAndFiltersItems(t *testing.T) {
	originalShare := &share.Share{ID: "share-1", Path: "/deleted/docs/report.txt", Description: "keep metadata"}
	originalFavorite := &favorites.Favorite{Path: "/deleted/docs"}
	state := deletedPathRestoreState{
		Shares: []*share.Share{
			originalShare,
			nil,
			{ID: "outside-share", Path: "/other/report.txt"},
		},
		Favorites: []*favorites.Favorite{
			originalFavorite,
			nil,
			{Path: "/other/favorite.txt"},
		},
	}

	relocated := relocateDeletedPathRestoreState(state, "/deleted", "/restored")

	if len(relocated.Shares) != 1 {
		t.Fatalf("relocated shares length = %d, want 1", len(relocated.Shares))
	}
	if relocated.Shares[0] == originalShare {
		t.Fatal("expected relocated share to be cloned")
	}
	if relocated.Shares[0].Path != "/restored/docs/report.txt" {
		t.Fatalf("relocated share path = %q", relocated.Shares[0].Path)
	}
	if relocated.Shares[0].Description != originalShare.Description {
		t.Fatalf("relocated share metadata was not preserved: %#v", relocated.Shares[0])
	}
	if originalShare.Path != "/deleted/docs/report.txt" {
		t.Fatalf("original share mutated to %q", originalShare.Path)
	}

	if len(relocated.Favorites) != 1 {
		t.Fatalf("relocated favorites length = %d, want 1", len(relocated.Favorites))
	}
	if relocated.Favorites[0] == originalFavorite {
		t.Fatal("expected relocated favorite to be cloned")
	}
	if relocated.Favorites[0].Path != "/restored/docs" {
		t.Fatalf("relocated favorite path = %q", relocated.Favorites[0].Path)
	}
	if originalFavorite.Path != "/deleted/docs" {
		t.Fatalf("original favorite mutated to %q", originalFavorite.Path)
	}
}

func TestRelocateDeletedPathRestoreState_SameSourceAndTargetReturnsOriginalState(t *testing.T) {
	state := deletedPathRestoreState{
		Shares:    []*share.Share{{ID: "share-1", Path: "/docs"}},
		Favorites: []*favorites.Favorite{{Path: "/docs"}},
	}

	relocated := relocateDeletedPathRestoreState(state, "/docs", "/docs")

	if !reflect.DeepEqual(relocated, state) {
		t.Fatalf("relocated state = %#v, want original %#v", relocated, state)
	}
}

func TestCombineDeleteHookResults(t *testing.T) {
	t.Run("passes through nil sides", func(t *testing.T) {
		primary := &storage.PathDeleteHookResult{RestoreData: []byte("primary")}
		secondary := &storage.PathDeleteHookResult{RestoreData: []byte("secondary")}

		if got, err := combineDeleteHookResults(primary, nil); err != nil || got != primary {
			t.Fatalf("primary passthrough = (%#v, %v), want primary and nil error", got, err)
		}
		if got, err := combineDeleteHookResults(nil, secondary); err != nil || got != secondary {
			t.Fatalf("secondary passthrough = (%#v, %v), want secondary and nil error", got, err)
		}
	})

	t.Run("keeps only restore data and combines rollback order", func(t *testing.T) {
		var calls []string
		primaryErr := errors.New("primary rollback")
		secondaryErr := errors.New("secondary rollback")
		primary := &storage.PathDeleteHookResult{
			RestoreData: []byte("primary-data"),
			Rollback: func() error {
				calls = append(calls, "primary")
				return primaryErr
			},
		}
		secondary := &storage.PathDeleteHookResult{
			Rollback: func() error {
				calls = append(calls, "secondary")
				return secondaryErr
			},
		}

		combined, err := combineDeleteHookResults(primary, secondary)
		if err != nil {
			t.Fatalf("combineDeleteHookResults() error: %v", err)
		}
		if string(combined.RestoreData) != "primary-data" {
			t.Fatalf("restore data = %q, want primary-data", string(combined.RestoreData))
		}
		err = combined.Rollback()
		if !reflect.DeepEqual(calls, []string{"secondary", "primary"}) {
			t.Fatalf("rollback order = %v, want [secondary primary]", calls)
		}
		if !errors.Is(err, primaryErr) || !errors.Is(err, secondaryErr) {
			t.Fatalf("rollback error = %v, want joined primary and secondary errors", err)
		}
	})

	t.Run("uses secondary restore data when primary has none", func(t *testing.T) {
		combined, err := combineDeleteHookResults(
			&storage.PathDeleteHookResult{},
			&storage.PathDeleteHookResult{RestoreData: []byte("secondary-data")},
		)
		if err != nil {
			t.Fatalf("combineDeleteHookResults() error: %v", err)
		}
		if string(combined.RestoreData) != "secondary-data" {
			t.Fatalf("restore data = %q, want secondary-data", string(combined.RestoreData))
		}
		if combined.Rollback != nil {
			t.Fatal("expected no rollback when neither side defines one")
		}
	})

	t.Run("rejects multiple restore metadata sources", func(t *testing.T) {
		_, err := combineDeleteHookResults(
			&storage.PathDeleteHookResult{RestoreData: []byte("primary-data")},
			&storage.PathDeleteHookResult{RestoreData: []byte("secondary-data")},
		)
		if err == nil || !strings.Contains(err.Error(), "multiple delete hooks") {
			t.Fatalf("error = %v, want multiple metadata source rejection", err)
		}
	})
}

func TestRunDeleteHookRollback(t *testing.T) {
	if err := runDeleteHookRollback(nil); err != nil {
		t.Fatalf("nil rollback error = %v", err)
	}
	if err := runDeleteHookRollback(&storage.PathDeleteHookResult{}); err != nil {
		t.Fatalf("empty rollback error = %v", err)
	}

	wantErr := errors.New("rollback failed")
	result := &storage.PathDeleteHookResult{
		Rollback: func() error {
			return wantErr
		},
	}
	if err := runDeleteHookRollback(result); !errors.Is(err, wantErr) {
		t.Fatalf("rollback error = %v, want %v", err, wantErr)
	}
}
