package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/seanbao/mnemonas/internal/favorites"
	"github.com/seanbao/mnemonas/internal/share"
)

func TestDurableTrashParticipantRecoveryEvidenceRemainsUnreliableAcrossReopen(t *testing.T) {
	tests := []struct {
		name      string
		storePath string
		open      func(string) (*Server, bool, error)
	}{
		{
			name:      "shares",
			storePath: filepath.Join(t.TempDir(), "shares.json"),
			open: func(storePath string) (*Server, bool, error) {
				store, err := share.NewShareStore(storePath)
				if err != nil {
					return nil, false, err
				}
				return &Server{shareStore: store}, store.RecoveredFromCorruption(), nil
			},
		},
		{
			name:      "favorites",
			storePath: filepath.Join(t.TempDir(), "favorites.json"),
			open: func(storePath string) (*Server, bool, error) {
				store, err := favorites.NewStore(storePath)
				if err != nil {
					return nil, false, err
				}
				return &Server{favoritesStore: store}, store.RecoveredFromCorruption(), nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(test.storePath, []byte(`{"version":`), 0o600); err != nil {
				t.Fatalf("WriteFile(corrupt store) error: %v", err)
			}
			for openIndex := 1; openIndex <= 2; openIndex++ {
				server, recovered, err := test.open(test.storePath)
				if err != nil {
					t.Fatalf("open %d error: %v", openIndex, err)
				}
				if !recovered {
					t.Fatalf("open %d lost corruption recovery state", openIndex)
				}
				if err := newDurableTrashParticipantHooks(server).RecoveryStateReliable(); err == nil {
					t.Fatalf("recovery reliability check after open %d error = nil", openIndex)
				}
			}
		})
	}
}
