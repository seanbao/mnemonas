// Package versionstore provides SQLite-based version management for MnemoNAS

package versionstore

import (
	"context"
	"fmt"
	"sync"

	"github.com/seanbao/mnemonas/internal/dataplane"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ObjectStore handles version object storage via Rust dataplane
type ObjectStore struct {
	mu     sync.RWMutex
	client *dataplane.Client
}

// NewObjectStore creates an object store using dataplane client
func NewObjectStore(client *dataplane.Client) *ObjectStore {
	return &ObjectStore{client: client}
}

// SetClient swaps the dataplane client used for object operations.
func (s *ObjectStore) SetClient(client *dataplane.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = client
}

func (s *ObjectStore) getClient() *dataplane.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Put stores data via dataplane and returns its hash
func (s *ObjectStore) Put(ctx context.Context, data []byte) (string, error) {
	client := s.getClient()
	if client == nil || !client.IsConnected() {
		return "", fmt.Errorf("%w: dataplane not connected", ErrUnavailable)
	}

	info, err := client.PutChunk(ctx, data)
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return "", fmt.Errorf("failed to put chunk: %w", err)
	}

	return info.Hash, nil
}

// Get retrieves data via dataplane by hash
func (s *ObjectStore) Get(ctx context.Context, hash string) ([]byte, error) {
	client := s.getClient()
	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("%w: dataplane not connected", ErrUnavailable)
	}

	data, err := client.GetChunk(ctx, hash)
	if err != nil {
		return nil, mapObjectGetError(err)
	}

	return data, nil
}

func mapObjectGetError(err error) error {
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	if status.Code(err) == codes.Unavailable {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	return fmt.Errorf("failed to get chunk: %w", err)
}

func mapObjectDeleteError(err error) error {
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	if status.Code(err) == codes.Unavailable {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	return fmt.Errorf("failed to delete chunk: %w", err)
}

// Has checks if an object exists via dataplane
func (s *ObjectStore) Has(ctx context.Context, hash string) (bool, error) {
	client := s.getClient()
	if client == nil || !client.IsConnected() {
		return false, fmt.Errorf("%w: dataplane not connected", ErrUnavailable)
	}

	exists, err := client.HasChunk(ctx, hash)
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return false, fmt.Errorf("failed to check chunk existence: %w", err)
	}

	return exists, nil
}

// Delete removes an object via dataplane
func (s *ObjectStore) Delete(ctx context.Context, hash string) error {
	client := s.getClient()
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("%w: dataplane not connected", ErrUnavailable)
	}

	_, err := client.DeleteChunk(ctx, hash)
	if err != nil {
		return mapObjectDeleteError(err)
	}

	return nil
}
