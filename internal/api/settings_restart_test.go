package api

import (
	"testing"

	"github.com/seanbao/mnemonas/internal/config"
)

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func uint32Ptr(v uint32) *uint32 {
	return &v
}

func TestServerTLSUpdateMayRequireRestart_OnlyRequestedChangedFieldsMatter(t *testing.T) {
	current := *config.Default()
	updated := current
	updated.Server.TLS.Enabled = !current.Server.TLS.Enabled
	updated.Server.TLS.CertFile = "/new/server.crt"

	if serverTLSUpdateMayRequireRestart(ServerTLSSettingsUpdate{}, current, updated) {
		t.Fatal("expected unrequested TLS differences not to require restart")
	}
	if !serverTLSUpdateMayRequireRestart(ServerTLSSettingsUpdate{Enabled: boolPtr(updated.Server.TLS.Enabled)}, current, updated) {
		t.Fatal("expected requested TLS enabled change to require restart")
	}
	if !serverTLSUpdateMayRequireRestart(ServerTLSSettingsUpdate{CertFile: stringPtr(updated.Server.TLS.CertFile)}, current, updated) {
		t.Fatal("expected requested TLS cert file change to require restart")
	}

	same := current
	if serverTLSUpdateMayRequireRestart(ServerTLSSettingsUpdate{Enabled: boolPtr(current.Server.TLS.Enabled)}, current, same) {
		t.Fatal("expected requested unchanged TLS field not to require restart")
	}
}

func TestSettingsUpdateMayRequireRestart_TracksServerTLSAndCDC(t *testing.T) {
	current := *config.Default()

	t.Run("runtime-only update", func(t *testing.T) {
		updated := current
		updated.WebDAV.Enabled = !current.WebDAV.Enabled
		if settingsUpdateMayRequireRestart(UpdateSettingsRequest{
			WebDAV: &WebDAVSettingsUpdate{Enabled: boolPtr(updated.WebDAV.Enabled)},
		}, current, updated) {
			t.Fatal("expected runtime WebDAV setting change not to require process restart")
		}
	})

	t.Run("server port", func(t *testing.T) {
		updated := current
		updated.Server.Port = current.Server.Port + 1
		if !settingsUpdateMayRequireRestart(UpdateSettingsRequest{
			Server: &ServerSettingsUpdate{Port: &updated.Server.Port},
		}, current, updated) {
			t.Fatal("expected server port change to require restart")
		}
	})

	t.Run("tls cert directory", func(t *testing.T) {
		updated := current
		updated.Server.TLS.CertDir = "/new/certs"
		if !settingsUpdateMayRequireRestart(UpdateSettingsRequest{
			Server: &ServerSettingsUpdate{
				TLS: &ServerTLSSettingsUpdate{CertDir: stringPtr(updated.Server.TLS.CertDir)},
			},
		}, current, updated) {
			t.Fatal("expected TLS cert dir change to require restart")
		}
	})

	t.Run("cdc chunk size", func(t *testing.T) {
		updated := current
		updated.DataPlane.CDC.MinChunkSize = current.DataPlane.CDC.MinChunkSize + 1024
		if !settingsUpdateMayRequireRestart(UpdateSettingsRequest{
			CDC: &CDCSettingsUpdate{MinChunkSize: uint32Ptr(updated.DataPlane.CDC.MinChunkSize)},
		}, current, updated) {
			t.Fatal("expected CDC chunk size change to require restart")
		}
	})
}
