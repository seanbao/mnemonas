package api

import (
	"context"

	"github.com/seanbao/mnemonas/internal/versionstore"
)

type apiFaultingWriteTransactionStore struct {
	delegate   *versionstore.Store
	ensureErr  error
	ensureCall int
}

func (s *apiFaultingWriteTransactionStore) CaptureWriteMetadataPlan(
	ctx context.Context,
	index versionstore.FileIndexRecord,
	version *versionstore.VersionRecord,
) (versionstore.WriteMetadataPlan, error) {
	return s.delegate.CaptureWriteMetadataPlan(ctx, index, version)
}

func (s *apiFaultingWriteTransactionStore) InspectWriteMetadata(
	ctx context.Context,
	plan versionstore.WriteMetadataPlan,
) (versionstore.WriteMetadataState, error) {
	return s.delegate.InspectWriteMetadata(ctx, plan)
}

func (s *apiFaultingWriteTransactionStore) RollbackWriteMetadata(
	ctx context.Context,
	plan versionstore.WriteMetadataPlan,
) error {
	return s.delegate.RollbackWriteMetadata(ctx, plan)
}

func (s *apiFaultingWriteTransactionStore) EnsureWriteMetadataCommitted(
	context.Context,
	versionstore.WriteMetadataPlan,
) error {
	s.ensureCall++
	return s.ensureErr
}

func (s *apiFaultingWriteTransactionStore) GetObject(ctx context.Context, hash string) ([]byte, error) {
	return s.delegate.GetObject(ctx, hash)
}

func (s *apiFaultingWriteTransactionStore) PutObjectExpected(
	ctx context.Context,
	data []byte,
	expectedHash string,
) (versionstore.ObjectPutResult, error) {
	return s.delegate.PutObjectExpected(ctx, data, expectedHash)
}

func (s *apiFaultingWriteTransactionStore) HasVersionReference(ctx context.Context, hash string) (bool, error) {
	return s.delegate.HasVersionReference(ctx, hash)
}

func (s *apiFaultingWriteTransactionStore) DeleteObject(ctx context.Context, hash string) error {
	return s.delegate.DeleteObject(ctx, hash)
}
