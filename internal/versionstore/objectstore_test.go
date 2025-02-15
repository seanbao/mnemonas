package versionstore

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapObjectGetError(t *testing.T) {
	t.Run("maps grpc not found to ErrNotFound", func(t *testing.T) {
		err := mapObjectGetError(status.Error(codes.NotFound, "missing"))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("mapObjectGetError() = %v, want ErrNotFound", err)
		}
	})

	t.Run("preserves transport and server failures", func(t *testing.T) {
		grpcErr := status.Error(codes.Unavailable, "dataplane down")
		err := mapObjectGetError(grpcErr)

		if errors.Is(err, ErrNotFound) {
			t.Fatalf("mapObjectGetError() incorrectly mapped unavailable error to ErrNotFound")
		}
		if !errors.Is(err, grpcErr) {
			t.Fatalf("mapObjectGetError() = %v, want wrapped original error %v", err, grpcErr)
		}
	})
}
