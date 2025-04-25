package versionstore

import (
	"errors"
	"strings"
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
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("mapObjectGetError() = %v, want ErrUnavailable", err)
		}
		if got := err.Error(); got == grpcErr.Error() || !strings.Contains(got, grpcErr.Error()) {
			t.Fatalf("mapObjectGetError() message = %q, want to include %q", got, grpcErr.Error())
		}
	})
}

func TestMapObjectDeleteError(t *testing.T) {
	t.Run("maps grpc not found to ErrNotFound", func(t *testing.T) {
		err := mapObjectDeleteError(status.Error(codes.NotFound, "missing"))
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("mapObjectDeleteError() = %v, want ErrNotFound", err)
		}
	})

	t.Run("preserves transport and server failures", func(t *testing.T) {
		grpcErr := status.Error(codes.Unavailable, "dataplane down")
		err := mapObjectDeleteError(grpcErr)

		if errors.Is(err, ErrNotFound) {
			t.Fatalf("mapObjectDeleteError() incorrectly mapped unavailable error to ErrNotFound")
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("mapObjectDeleteError() = %v, want ErrUnavailable", err)
		}
		if got := err.Error(); got == grpcErr.Error() || !strings.Contains(got, grpcErr.Error()) {
			t.Fatalf("mapObjectDeleteError() message = %q, want to include %q", got, grpcErr.Error())
		}
	})
}
