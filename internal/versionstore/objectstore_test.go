package versionstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/seanbao/mnemonas/internal/dataplane"
)

func TestObjectStoreSetClient(t *testing.T) {
	first := dataplane.NewClient("first")
	second := dataplane.NewClient("second")
	store := NewObjectStore(first)

	if got := store.getClient(); got != first {
		t.Fatalf("initial client = %#v, want first client", got)
	}
	store.SetClient(second)
	if got := store.getClient(); got != second {
		t.Fatalf("updated client = %#v, want second client", got)
	}
}

func TestObjectStoreUnavailableWithoutClient(t *testing.T) {
	store := NewObjectStore(nil)
	ctx := context.Background()

	if _, err := store.Put(ctx, []byte("data")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put() error = %v, want %v", err, ErrUnavailable)
	}
	if _, err := store.Get(ctx, "hash"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get() error = %v, want %v", err, ErrUnavailable)
	}
	if exists, err := store.Has(ctx, "hash"); !errors.Is(err, ErrUnavailable) || exists {
		t.Fatalf("Has() = (%v, %v), want false %v", exists, err, ErrUnavailable)
	}
	if err := store.Delete(ctx, "hash"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete() error = %v, want %v", err, ErrUnavailable)
	}
}

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
