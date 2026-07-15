package storage

import (
	"context"
	"errors"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

type writeTransactionRuntimeMetadataFaultStore struct {
	*versionstore.Store
	objects                    map[string][]byte
	captureWriteMetadataPlanFn func(
		context.Context,
		versionstore.FileIndexRecord,
		*versionstore.VersionRecord,
	) (versionstore.WriteMetadataPlan, error)
	inspectWriteMetadataFn func(
		context.Context,
		versionstore.WriteMetadataPlan,
	) (versionstore.WriteMetadataState, error)
	rollbackWriteMetadataFn func(context.Context, versionstore.WriteMetadataPlan) error
	ensureWriteMetadataFn   func(context.Context, versionstore.WriteMetadataPlan) error
	getObjectFn             func(context.Context, string) ([]byte, error)
	putObjectExpectedFn     func(
		context.Context,
		[]byte,
		string,
	) (versionstore.ObjectPutResult, error)
	hasVersionReferenceFn  func(context.Context, string) (bool, error)
	deleteObjectFn         func(context.Context, string) error
	ensureAfterThenUnknown bool
	ensureBeforeUnknown    bool
	ensureCalls            int
	deleteObjectErr        error
}

func newWriteTransactionRuntimeTestStore(
	fs *FileSystem,
) *writeTransactionRuntimeMetadataFaultStore {
	return &writeTransactionRuntimeMetadataFaultStore{
		Store:   fs.versions,
		objects: make(map[string][]byte),
	}
}

func (store *writeTransactionRuntimeMetadataFaultStore) CaptureWriteMetadataPlan(
	ctx context.Context,
	indexAfter versionstore.FileIndexRecord,
	versionAfter *versionstore.VersionRecord,
) (versionstore.WriteMetadataPlan, error) {
	if store.captureWriteMetadataPlanFn != nil {
		return store.captureWriteMetadataPlanFn(ctx, indexAfter, versionAfter)
	}
	return store.Store.CaptureWriteMetadataPlan(ctx, indexAfter, versionAfter)
}

func (store *writeTransactionRuntimeMetadataFaultStore) InspectWriteMetadata(
	ctx context.Context,
	plan versionstore.WriteMetadataPlan,
) (versionstore.WriteMetadataState, error) {
	if store.inspectWriteMetadataFn != nil {
		return store.inspectWriteMetadataFn(ctx, plan)
	}
	return store.Store.InspectWriteMetadata(ctx, plan)
}

func (store *writeTransactionRuntimeMetadataFaultStore) RollbackWriteMetadata(
	ctx context.Context,
	plan versionstore.WriteMetadataPlan,
) error {
	if store.rollbackWriteMetadataFn != nil {
		return store.rollbackWriteMetadataFn(ctx, plan)
	}
	return store.Store.RollbackWriteMetadata(ctx, plan)
}

func (store *writeTransactionRuntimeMetadataFaultStore) GetObject(
	ctx context.Context,
	hash string,
) ([]byte, error) {
	if store.getObjectFn != nil {
		return store.getObjectFn(ctx, hash)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := store.objects[hash]
	if !ok {
		return nil, versionstore.ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

func (store *writeTransactionRuntimeMetadataFaultStore) PutObjectExpected(
	ctx context.Context,
	data []byte,
	expectedHash string,
) (versionstore.ObjectPutResult, error) {
	if store.putObjectExpectedFn != nil {
		return store.putObjectExpectedFn(ctx, data, expectedHash)
	}
	if err := ctx.Err(); err != nil {
		return versionstore.ObjectPutResult{}, err
	}
	if computeHash(data) != expectedHash {
		return versionstore.ObjectPutResult{}, errors.New("unexpected object hash")
	}
	if existing, ok := store.objects[expectedHash]; ok {
		if string(existing) != string(data) {
			return versionstore.ObjectPutResult{}, errors.New("existing object content differs")
		}
		return versionstore.ObjectPutResult{
			Hash:         expectedHash,
			Size:         int64(len(data)),
			Deduplicated: true,
		}, nil
	}
	store.objects[expectedHash] = append([]byte(nil), data...)
	return versionstore.ObjectPutResult{
		Hash: expectedHash,
		Size: int64(len(data)),
	}, nil
}

func (store *writeTransactionRuntimeMetadataFaultStore) DeleteObject(
	ctx context.Context,
	hash string,
) error {
	if store.deleteObjectFn != nil {
		return store.deleteObjectFn(ctx, hash)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if store.deleteObjectErr != nil {
		return store.deleteObjectErr
	}
	if _, ok := store.objects[hash]; !ok {
		return versionstore.ErrNotFound
	}
	delete(store.objects, hash)
	return nil
}

func (store *writeTransactionRuntimeMetadataFaultStore) HasVersionReference(
	ctx context.Context,
	hash string,
) (bool, error) {
	if store.hasVersionReferenceFn != nil {
		return store.hasVersionReferenceFn(ctx, hash)
	}
	return store.Store.HasVersionReference(ctx, hash)
}

func (store *writeTransactionRuntimeMetadataFaultStore) EnsureWriteMetadataCommitted(
	ctx context.Context,
	plan versionstore.WriteMetadataPlan,
) error {
	store.ensureCalls++
	if store.ensureWriteMetadataFn != nil {
		return store.ensureWriteMetadataFn(ctx, plan)
	}
	if store.ensureCalls == 1 {
		if store.ensureBeforeUnknown {
			return versionstore.ErrWriteMetadataOutcomeUnknown
		}
		if store.ensureAfterThenUnknown {
			return errors.Join(
				store.Store.EnsureWriteMetadataCommitted(ctx, plan),
				versionstore.ErrWriteMetadataOutcomeUnknown,
			)
		}
	}
	return store.Store.EnsureWriteMetadataCommitted(ctx, plan)
}
