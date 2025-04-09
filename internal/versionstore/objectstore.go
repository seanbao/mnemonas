// Package versionstore provides SQLite-based version management for MnemoNAS

package versionstore

import (
	"context"
	"fmt"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ObjectStore handles version object storage via Rust dataplane
type ObjectStore struct {
	client *dataplane.Client
}

// NewObjectStore creates an object store using dataplane client
func NewObjectStore(client *dataplane.Client) *ObjectStore {
	return &ObjectStore{client: client}
}

// Put stores data via dataplane and returns its hash
func (s *ObjectStore) Put(ctx context.Context, data []byte) (string, error) {
	if !s.client.IsConnected() {
		return "", fmt.Errorf("dataplane not connected")
	}

	info, err := s.client.PutChunk(ctx, data)
	if err != nil {
		return "", fmt.Errorf("failed to put chunk: %w", err)
	}

	return info.Hash, nil
}

// Get retrieves data via dataplane by hash
func (s *ObjectStore) Get(ctx context.Context, hash string) ([]byte, error) {
	if !s.client.IsConnected() {
		return nil, fmt.Errorf("dataplane not connected")
	}

	data, err := s.client.GetChunk(ctx, hash)
	if err != nil {
		return nil, mapObjectGetError(err)
	}

	return data, nil
}

func mapObjectGetError(err error) error {
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}

	return fmt.Errorf("failed to get chunk: %w", err)
}

// Has checks if an object exists via dataplane
func (s *ObjectStore) Has(ctx context.Context, hash string) (bool, error) {
	if !s.client.IsConnected() {
		return false, fmt.Errorf("dataplane not connected")
	}

	exists, err := s.client.HasChunk(ctx, hash)
	if err != nil {
		return false, fmt.Errorf("failed to check chunk existence: %w", err)
	}

	return exists, nil
}

// Delete removes an object via dataplane
func (s *ObjectStore) Delete(ctx context.Context, hash string) error {
	if !s.client.IsConnected() {
		return fmt.Errorf("dataplane not connected")
	}

	_, err := s.client.DeleteChunk(ctx, hash)
	if err != nil {
		return fmt.Errorf("failed to delete chunk: %w", err)
	}

	return nil
}
