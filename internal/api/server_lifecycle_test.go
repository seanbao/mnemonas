package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"github.com/seanbao/mnemonas/internal/storage"
)

func TestNewServerDefersBackgroundTasksUntilExplicitStart(t *testing.T) {
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		ShareStoreFile:       filepath.Join(t.TempDir(), "shares.json"),
		AlertMonitor:         &fakeAlertMonitor{},
		DeferBackgroundTasks: true,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if server.backgroundTasksStarted {
		t.Fatal("background tasks started before explicit startup")
	}
	if server.shareExpiryReminderCancel != nil {
		t.Fatal("share expiry scheduler started before explicit startup")
	}

	if started := server.StartBackgroundTasks(context.Background()); !started {
		t.Fatal("StartBackgroundTasks() did not start deferred tasks")
	}
	if !server.backgroundTasksStarted {
		t.Fatal("background task lifecycle was not marked started")
	}
	if server.shareExpiryReminderCancel == nil {
		t.Fatal("share expiry scheduler was not started")
	}
	if started := server.StartBackgroundTasks(context.Background()); started {
		t.Fatal("StartBackgroundTasks() started tasks more than once")
	}
}

func TestNewServerStartsBackgroundTasksByDefault(t *testing.T) {
	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		ShareStoreFile: filepath.Join(t.TempDir(), "shares.json"),
		AlertMonitor:   &fakeAlertMonitor{},
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if !server.backgroundTasksStarted {
		t.Fatal("background tasks were not started by default")
	}
	if server.shareExpiryReminderCancel == nil {
		t.Fatal("share expiry scheduler was not started by default")
	}
}

func TestServerRecoverTrashTransfersRequiresFileSystem(t *testing.T) {
	server := &Server{}
	if _, err := server.RecoverTrashTransfers(context.Background()); err == nil {
		t.Fatal("RecoverTrashTransfers() error = nil, want filesystem initialization error")
	}
}

func TestServerRecoversTrashBeforeDeferredBackgroundTasksStart(t *testing.T) {
	root := t.TempDir()
	client := dataplane.NewClient("127.0.0.1:1")
	t.Cleanup(func() { _ = client.Close() })
	fs, err := storage.New(&storage.Config{
		FilesRoot:    filepath.Join(root, "files"),
		InternalRoot: filepath.Join(root, ".mnemonas"),
		TrashRoot:    filepath.Join(root, ".mnemonas", "trash"),
		Dataplane:    client,
	})
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	server, err := NewServer(zerolog.Nop(), &ServerConfig{
		FileSystem:           fs,
		ShareStoreFile:       filepath.Join(root, "shares.json"),
		FavoritesStoreFile:   filepath.Join(root, "favorites.json"),
		AlertMonitor:         &fakeAlertMonitor{},
		DeferBackgroundTasks: true,
	})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	purgeReport, err := fs.RecoverTrashDeletions(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashDeletions() error: %v", err)
	}
	if purgeReport.RolledBack != 0 || purgeReport.RolledForward != 0 || len(purgeReport.Blocked) != 0 {
		t.Fatalf("unexpected empty purge recovery report: %+v", purgeReport)
	}
	if server.backgroundTasksStarted || server.shareExpiryReminderCancel != nil {
		t.Fatal("background tasks started during Trash purge recovery")
	}

	report, err := server.RecoverTrashTransfers(context.Background())
	if err != nil {
		t.Fatalf("RecoverTrashTransfers() error: %v", err)
	}
	if report.RolledBack != 0 || report.RolledForward != 0 || report.Completed != 0 || len(report.Blocked) != 0 {
		t.Fatalf("unexpected empty recovery report: %+v", report)
	}
	if server.backgroundTasksStarted || server.shareExpiryReminderCancel != nil {
		t.Fatal("background tasks started during Trash transfer recovery")
	}
	if started := server.StartBackgroundTasks(context.Background()); !started {
		t.Fatal("StartBackgroundTasks() did not start after recovery")
	}
}
