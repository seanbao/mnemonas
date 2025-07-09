// Package dataplane provides gRPC client for Rust data plane
package dataplane

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/seanbao/mnemonas/proto"
)

// Client wraps gRPC connection to data plane service
type Client struct {
	conn   *grpc.ClientConn
	client pb.DataPlaneClient
	addr   string
	mu     sync.RWMutex
}

// NewClient creates a new data plane client
func NewClient(addr string) *Client {
	return &Client{addr: addr}
}

// Addr returns the configured data plane address.
func (c *Client) Addr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.addr
}

// Connect establishes connection to data plane
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if c.conn.GetState() == connectivity.Shutdown {
			_ = c.conn.Close()
			c.conn = nil
			c.client = nil
		} else {
			if err := waitForReady(ctx, c.conn); err != nil {
				return fmt.Errorf("failed to connect to data plane: %w", err)
			}
			c.client = pb.NewDataPlaneClient(c.conn)
			return nil
		}
	}

	// V3-4 fix: Set max message size limits
	const maxMsgSize = 100 * 1024 * 1024 // 100MB
	conn, err := grpc.NewClient(c.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgSize),
			grpc.MaxCallSendMsgSize(maxMsgSize),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to data plane: %w", err)
	}

	if err := waitForReady(ctx, conn); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to connect to data plane: %w", err)
	}

	c.conn = conn
	c.client = pb.NewDataPlaneClient(conn)
	return nil
}

func waitForReady(ctx context.Context, conn *grpc.ClientConn) error {
	for {
		state := conn.GetState()
		if state == connectivity.Idle {
			conn.Connect()
		}
		if state == connectivity.Ready {
			return nil
		}
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.client = nil
		return err
	}
	return nil
}

// IsConnected returns whether client is connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.conn.GetState() == connectivity.Ready
}

// Health checks data plane health
func (c *Client) Health(ctx context.Context) (*HealthStatus, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	resp, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return nil, err
	}

	return &HealthStatus{
		Healthy:    resp.Healthy,
		Version:    resp.Version,
		UptimeSecs: resp.UptimeSecs,
	}, nil
}

// Stats gets storage statistics
func (c *Client) Stats(ctx context.Context) (*StorageStats, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	resp, err := client.Stats(ctx, &pb.StatsRequest{})
	if err != nil {
		return nil, err
	}

	return &StorageStats{
		TotalChunks: resp.TotalChunks,
		TotalSize:   resp.TotalSize,
		UniqueSize:  resp.UniqueSize,
		DedupRatio:  resp.DedupRatio,
	}, nil
}

// PutChunk stores a data chunk
func (c *Client) PutChunk(ctx context.Context, data []byte) (*ChunkInfo, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	resp, err := client.PutChunk(ctx, &pb.PutChunkRequest{Data: data})
	if err != nil {
		return nil, err
	}

	return &ChunkInfo{
		Hash:         resp.Hash,
		Size:         resp.Size,
		Deduplicated: resp.Deduplicated,
	}, nil
}

// GetChunk retrieves a data chunk
func (c *Client) GetChunk(ctx context.Context, hash string) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	resp, err := client.GetChunk(ctx, &pb.GetChunkRequest{Hash: hash})
	if err != nil {
		return nil, err
	}

	return resp.Data, nil
}

// HasChunk checks if a chunk exists
func (c *Client) HasChunk(ctx context.Context, hash string) (bool, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return false, fmt.Errorf("not connected")
	}

	resp, err := client.HasChunk(ctx, &pb.HasChunkRequest{Hash: hash})
	if err != nil {
		return false, err
	}

	return resp.Exists, nil
}

// DeleteChunk marks a chunk for deletion
func (c *Client) DeleteChunk(ctx context.Context, hash string) (bool, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return false, fmt.Errorf("not connected")
	}

	resp, err := client.DeleteChunk(ctx, &pb.DeleteChunkRequest{Hash: hash})
	if err != nil {
		return false, err
	}

	return resp.Deleted, nil
}

// PutFile stores a file with CDC chunking
func (c *Client) PutFile(ctx context.Context, path string, r io.Reader) (*FileInfo, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	stream, err := client.PutFile(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.CloseSend()
	}()

	// Send metadata first
	if err := stream.Send(&pb.PutFileRequest{
		Payload: &pb.PutFileRequest_Metadata{
			Metadata: &pb.FileMetadata{Path: path},
		},
	}); err != nil {
		return nil, err
	}

	// Stream file data in chunks
	buf := make([]byte, 64*1024) // 64KB buffer
	for {
		n, err := r.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if err := stream.Send(&pb.PutFileRequest{
			Payload: &pb.PutFileRequest_Chunk{
				Chunk: buf[:n],
			},
		}); err != nil {
			return nil, err
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}

	return &FileInfo{
		ManifestHash: resp.ManifestHash,
		ChunkHashes:  resp.ChunkHashes,
		TotalSize:    resp.TotalSize,
		ChunkCount:   resp.ChunkCount,
		DedupRatio:   resp.DedupRatio,
	}, nil
}

// GetFile retrieves a file by manifest hash
func (c *Client) GetFile(ctx context.Context, manifestHash string, w io.Writer) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	stream, err := client.GetFile(ctx, &pb.GetFileRequest{ManifestHash: manifestHash})
	if err != nil {
		return err
	}
	defer func() {
		_ = stream.CloseSend()
	}()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if _, err := w.Write(resp.Data); err != nil {
			return err
		}
	}
}

// HealthStatus represents data plane health status
type HealthStatus struct {
	Healthy    bool
	Version    string
	UptimeSecs uint64
}

// StorageStats represents storage statistics
type StorageStats struct {
	TotalChunks uint64
	TotalSize   uint64
	UniqueSize  uint64
	DedupRatio  float64
}

// ChunkInfo represents stored chunk information
type ChunkInfo struct {
	Hash         string
	Size         uint64
	Deduplicated bool
}

// FileInfo represents stored file information
type FileInfo struct {
	ManifestHash string
	ChunkHashes  []string
	TotalSize    uint64
	ChunkCount   uint32
	DedupRatio   float64
}

// ScrubResult represents scrub operation result
type ScrubResult struct {
	TotalObjects     uint64
	ValidObjects     uint64
	CorruptedObjects uint64
	MissingObjects   uint64
	TotalSize        uint64
	DurationMs       uint64
	Errors           []ScrubError
}

// ScrubError represents a single scrub error
type ScrubError struct {
	Hash      string
	ErrorType string
	Message   string
}

// ObjectInfo represents CAS object info
type ObjectInfo struct {
	Hash      string
	Size      uint64
	CreatedAt time.Time // NEW-2: Creation time for GC grace period
}

// ListObjectsResult represents object listing result
type ListObjectsResult struct {
	Objects    []ObjectInfo
	NextCursor string
}

// Scrub verifies data integrity
func (c *Client) Scrub(ctx context.Context, hashes []string) (*ScrubResult, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	resp, err := client.Scrub(ctx, &pb.ScrubRequest{Hashes: hashes})
	if err != nil {
		return nil, err
	}

	errors := make([]ScrubError, 0, len(resp.Errors))
	for _, e := range resp.Errors {
		errors = append(errors, ScrubError{
			Hash:      e.Hash,
			ErrorType: e.ErrorType,
			Message:   e.Message,
		})
	}

	return &ScrubResult{
		TotalObjects:     resp.TotalObjects,
		ValidObjects:     resp.ValidObjects,
		CorruptedObjects: resp.CorruptedObjects,
		MissingObjects:   resp.MissingObjects,
		TotalSize:        resp.TotalSize,
		DurationMs:       resp.DurationMs,
		Errors:           errors,
	}, nil
}

// ListObjects lists CAS objects with pagination
func (c *Client) ListObjects(ctx context.Context, cursor string, limit uint32) (*ListObjectsResult, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected")
	}

	req := &pb.ListObjectsRequest{}
	if cursor != "" {
		req.Cursor = &cursor
	}
	if limit > 0 {
		req.Limit = &limit
	}

	resp, err := client.ListObjects(ctx, req)
	if err != nil {
		return nil, err
	}

	objects := make([]ObjectInfo, 0, len(resp.Objects))
	for _, o := range resp.Objects {
		obj := ObjectInfo{
			Hash: o.Hash,
			Size: o.Size,
		}
		// NEW-2: Parse creation time if available
		if o.CreatedAtUnix != nil {
			obj.CreatedAt = time.Unix(*o.CreatedAtUnix, 0)
		}
		objects = append(objects, obj)
	}

	result := &ListObjectsResult{Objects: objects}
	if resp.NextCursor != nil {
		result.NextCursor = *resp.NextCursor
	}

	return result, nil
}

// WithTimeout creates context with timeout
func WithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
