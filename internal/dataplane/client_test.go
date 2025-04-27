package dataplane

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	pb "github.com/seanbao/mnemonas/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeDataPlaneClient struct {
	putFileStream grpc.ClientStreamingClient[pb.PutFileRequest, pb.PutFileResponse]
	putFileErr    error
	getFileStream grpc.ServerStreamingClient[pb.GetFileResponse]
	getFileErr    error
}

func (f *fakeDataPlaneClient) PutChunk(context.Context, *pb.PutChunkRequest, ...grpc.CallOption) (*pb.PutChunkResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) GetChunk(context.Context, *pb.GetChunkRequest, ...grpc.CallOption) (*pb.GetChunkResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) HasChunk(context.Context, *pb.HasChunkRequest, ...grpc.CallOption) (*pb.HasChunkResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) DeleteChunk(context.Context, *pb.DeleteChunkRequest, ...grpc.CallOption) (*pb.DeleteChunkResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) PutFile(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[pb.PutFileRequest, pb.PutFileResponse], error) {
	return f.putFileStream, f.putFileErr
}

func (f *fakeDataPlaneClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.GetFileResponse], error) {
	return f.getFileStream, f.getFileErr
}

func (f *fakeDataPlaneClient) Health(context.Context, *pb.HealthRequest, ...grpc.CallOption) (*pb.HealthResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) Stats(context.Context, *pb.StatsRequest, ...grpc.CallOption) (*pb.StatsResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) Scrub(context.Context, *pb.ScrubRequest, ...grpc.CallOption) (*pb.ScrubResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeDataPlaneClient) ListObjects(context.Context, *pb.ListObjectsRequest, ...grpc.CallOption) (*pb.ListObjectsResponse, error) {
	return nil, errors.New("not implemented")
}

type fakePutFileStream struct {
	closeSendCalled bool
	closeAndRecvErr error
	sendErr         error
	sentRequests    []*pb.PutFileRequest
}

func (f *fakePutFileStream) Send(req *pb.PutFileRequest) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sentRequests = append(f.sentRequests, req)
	return nil
}

func (f *fakePutFileStream) CloseAndRecv() (*pb.PutFileResponse, error) {
	if f.closeAndRecvErr != nil {
		return nil, f.closeAndRecvErr
	}
	return &pb.PutFileResponse{}, nil
}

func (f *fakePutFileStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakePutFileStream) Trailer() metadata.MD         { return nil }
func (f *fakePutFileStream) CloseSend() error {
	f.closeSendCalled = true
	return nil
}
func (f *fakePutFileStream) Context() context.Context { return context.Background() }
func (f *fakePutFileStream) SendMsg(any) error        { return nil }
func (f *fakePutFileStream) RecvMsg(any) error        { return nil }

type fakeGetFileStream struct {
	closeSendCalled bool
	responses       []*pb.GetFileResponse
	recvErr         error
}

func (f *fakeGetFileStream) Recv() (*pb.GetFileResponse, error) {
	if len(f.responses) > 0 {
		resp := f.responses[0]
		f.responses = f.responses[1:]
		return resp, nil
	}
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	return nil, io.EOF
}

func (f *fakeGetFileStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakeGetFileStream) Trailer() metadata.MD         { return nil }
func (f *fakeGetFileStream) CloseSend() error {
	f.closeSendCalled = true
	return nil
}
func (f *fakeGetFileStream) Context() context.Context { return context.Background() }
func (f *fakeGetFileStream) SendMsg(any) error        { return nil }
func (f *fakeGetFileStream) RecvMsg(any) error        { return nil }

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestNewClient(t *testing.T) {
	client := NewClient("localhost:9090")

	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.addr != "localhost:9090" {
		t.Errorf("Expected addr 'localhost:9090', got '%s'", client.addr)
	}
	if client.conn != nil {
		t.Error("Expected conn to be nil initially")
	}
	if client.client != nil {
		t.Error("Expected client to be nil initially")
	}
}

func TestIsConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	// Initially not connected
	if client.IsConnected() {
		t.Error("Expected IsConnected to return false for new client")
	}
}

func TestClose(t *testing.T) {
	client := NewClient("localhost:9090")

	// Close without connection should not error
	err := client.Close()
	if err != nil {
		t.Errorf("Close on unconnected client should not error, got: %v", err)
	}
}

func TestHealthNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.Health(ctx)
	if err == nil {
		t.Error("Expected error when calling Health on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestStatsNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.Stats(ctx)
	if err == nil {
		t.Error("Expected error when calling Stats on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestPutChunkNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.PutChunk(ctx, []byte("test data"))
	if err == nil {
		t.Error("Expected error when calling PutChunk on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestGetChunkNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.GetChunk(ctx, "abc123")
	if err == nil {
		t.Error("Expected error when calling GetChunk on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestHasChunkNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.HasChunk(ctx, "abc123")
	if err == nil {
		t.Error("Expected error when calling HasChunk on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestDeleteChunkNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.DeleteChunk(ctx, "abc123")
	if err == nil {
		t.Error("Expected error when calling DeleteChunk on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestPutFileNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.PutFile(ctx, "/test.txt", nil)
	if err == nil {
		t.Error("Expected error when calling PutFile on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestGetFileNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	err := client.GetFile(ctx, "abc123", nil)
	if err == nil {
		t.Error("Expected error when calling GetFile on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestPutFileClosesStreamOnReadError(t *testing.T) {
	stream := &fakePutFileStream{}
	client := NewClient("localhost:9090")
	client.client = &fakeDataPlaneClient{putFileStream: stream}

	_, err := client.PutFile(context.Background(), "/test.txt", errReader{err: errors.New("read failed")})
	if err == nil || err.Error() != "read failed" {
		t.Fatalf("PutFile error = %v, want read failed", err)
	}
	if !stream.closeSendCalled {
		t.Fatal("expected PutFile to close the stream on read error")
	}
	if len(stream.sentRequests) != 1 {
		t.Fatalf("expected metadata send before read failure, got %d sends", len(stream.sentRequests))
	}
}

func TestGetFileClosesStreamOnRecvError(t *testing.T) {
	stream := &fakeGetFileStream{recvErr: errors.New("recv failed")}
	client := NewClient("localhost:9090")
	client.client = &fakeDataPlaneClient{getFileStream: stream}

	err := client.GetFile(context.Background(), "manifest-1", io.Discard)
	if err == nil || err.Error() != "recv failed" {
		t.Fatalf("GetFile error = %v, want recv failed", err)
	}
	if !stream.closeSendCalled {
		t.Fatal("expected GetFile to close the stream on recv error")
	}
}

func TestScrubNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.Scrub(ctx, nil)
	if err == nil {
		t.Error("Expected error when calling Scrub on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestListObjectsNotConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	ctx := context.Background()
	_, err := client.ListObjects(ctx, "", 10)
	if err == nil {
		t.Error("Expected error when calling ListObjects on unconnected client")
	}
	if err.Error() != "not connected" {
		t.Errorf("Expected 'not connected' error, got: %v", err)
	}
}

func TestConnectInvalidAddress(t *testing.T) {
	client := NewClient("invalid-addr:99999")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected Connect to fail for invalid address")
	}
	if client.IsConnected() {
		t.Fatal("expected client to remain disconnected after failed connect")
	}
}

func TestWithTimeout(t *testing.T) {
	ctx, cancel := WithTimeout(5 * time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("Expected context to have deadline")
	}

	expectedDeadline := time.Now().Add(5 * time.Second)
	diff := deadline.Sub(expectedDeadline)
	if diff > 100*time.Millisecond || diff < -100*time.Millisecond {
		t.Errorf("Deadline not as expected, diff: %v", diff)
	}
}

func TestHealthStatus(t *testing.T) {
	status := HealthStatus{
		Healthy:    true,
		Version:    "0.3.0",
		UptimeSecs: 86400,
	}

	if !status.Healthy {
		t.Error("Expected Healthy to be true")
	}
	if status.Version != "0.3.0" {
		t.Errorf("Expected Version '0.3.0', got '%s'", status.Version)
	}
	if status.UptimeSecs != 86400 {
		t.Errorf("Expected UptimeSecs 86400, got %d", status.UptimeSecs)
	}
}

func TestStorageStats(t *testing.T) {
	stats := StorageStats{
		TotalChunks: 1000,
		TotalSize:   1048576,
		UniqueSize:  524288,
		DedupRatio:  0.5,
	}

	if stats.TotalChunks != 1000 {
		t.Errorf("Expected TotalChunks 1000, got %d", stats.TotalChunks)
	}
	if stats.TotalSize != 1048576 {
		t.Errorf("Expected TotalSize 1048576, got %d", stats.TotalSize)
	}
	if stats.UniqueSize != 524288 {
		t.Errorf("Expected UniqueSize 524288, got %d", stats.UniqueSize)
	}
	if stats.DedupRatio != 0.5 {
		t.Errorf("Expected DedupRatio 0.5, got %f", stats.DedupRatio)
	}
}

func TestChunkInfo(t *testing.T) {
	info := ChunkInfo{
		Hash:         "abc123def456",
		Size:         1024,
		Deduplicated: true,
	}

	if info.Hash != "abc123def456" {
		t.Errorf("Expected Hash 'abc123def456', got '%s'", info.Hash)
	}
	if info.Size != 1024 {
		t.Errorf("Expected Size 1024, got %d", info.Size)
	}
	if !info.Deduplicated {
		t.Error("Expected Deduplicated to be true")
	}
}

func TestFileInfo(t *testing.T) {
	info := FileInfo{
		ManifestHash: "manifest123",
		ChunkHashes:  []string{"chunk1", "chunk2"},
		TotalSize:    2048,
		ChunkCount:   2,
		DedupRatio:   0.3,
	}

	if info.ManifestHash != "manifest123" {
		t.Errorf("Expected ManifestHash 'manifest123', got '%s'", info.ManifestHash)
	}
	if len(info.ChunkHashes) != 2 {
		t.Errorf("Expected 2 chunk hashes, got %d", len(info.ChunkHashes))
	}
	if info.TotalSize != 2048 {
		t.Errorf("Expected TotalSize 2048, got %d", info.TotalSize)
	}
	if info.ChunkCount != 2 {
		t.Errorf("Expected ChunkCount 2, got %d", info.ChunkCount)
	}
	if info.DedupRatio != 0.3 {
		t.Errorf("Expected DedupRatio 0.3, got %f", info.DedupRatio)
	}
}

func TestScrubResult(t *testing.T) {
	result := ScrubResult{
		TotalObjects:     100,
		ValidObjects:     98,
		CorruptedObjects: 1,
		MissingObjects:   1,
		TotalSize:        10485760,
		DurationMs:       5000,
		Errors: []ScrubError{
			{Hash: "hash1", ErrorType: "corrupted", Message: "data mismatch"},
		},
	}

	if result.TotalObjects != 100 {
		t.Errorf("Expected TotalObjects 100, got %d", result.TotalObjects)
	}
	if result.ValidObjects != 98 {
		t.Errorf("Expected ValidObjects 98, got %d", result.ValidObjects)
	}
	if result.CorruptedObjects != 1 {
		t.Errorf("Expected CorruptedObjects 1, got %d", result.CorruptedObjects)
	}
	if result.MissingObjects != 1 {
		t.Errorf("Expected MissingObjects 1, got %d", result.MissingObjects)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0].ErrorType != "corrupted" {
		t.Errorf("Expected error type 'corrupted', got '%s'", result.Errors[0].ErrorType)
	}
}

func TestObjectInfo(t *testing.T) {
	now := time.Now()
	info := ObjectInfo{
		Hash:      "objhash123",
		Size:      4096,
		CreatedAt: now,
	}

	if info.Hash != "objhash123" {
		t.Errorf("Expected Hash 'objhash123', got '%s'", info.Hash)
	}
	if info.Size != 4096 {
		t.Errorf("Expected Size 4096, got %d", info.Size)
	}
	if !info.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch")
	}
}

func TestListObjectsResult(t *testing.T) {
	result := ListObjectsResult{
		Objects: []ObjectInfo{
			{Hash: "obj1", Size: 100},
			{Hash: "obj2", Size: 200},
		},
		NextCursor: "cursor-abc",
	}

	if len(result.Objects) != 2 {
		t.Errorf("Expected 2 objects, got %d", len(result.Objects))
	}
	if result.NextCursor != "cursor-abc" {
		t.Errorf("Expected NextCursor 'cursor-abc', got '%s'", result.NextCursor)
	}
}

func TestConcurrentIsConnected(t *testing.T) {
	client := NewClient("localhost:9090")

	// Test concurrent access to IsConnected
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = client.IsConnected()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestConcurrentClose(t *testing.T) {
	client := NewClient("localhost:9090")

	// Test concurrent Close calls
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_ = client.Close()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
