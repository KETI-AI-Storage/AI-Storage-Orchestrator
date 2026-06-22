// ============================================
// MantaFS gRPC Client for Orchestrator (1차 통합)
// KETI는 워크로드/데이터셋 정보만 전달하고, 캐시·티어·배치는 MantaFS가 담당합니다.
// ============================================

package manta

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MantaClient는 MantaFS와 통신하는 클라이언트로, IntegrationClient를 구현합니다.
type MantaClient struct {
	conn            *grpc.ClientConn
	endpoint        string
	mu              sync.RWMutex
	connected       bool
	reconnectPeriod time.Duration

	// TODO(gluesys): 실제 Gluesys gRPC 생성 클라이언트로 교체
}

// Config는 MantaFS 연결 설정입니다.
type Config struct {
	Endpoint        string
	Timeout         time.Duration
	ReconnectPeriod time.Duration
	MaxRetries      int
}

// DefaultConfig returns default MantaFS configuration
func DefaultConfig() *Config {
	return &Config{
		Endpoint:        "manta-grpc.keti.svc.cluster.local:50052",
		Timeout:         10 * time.Second,
		ReconnectPeriod: 30 * time.Second,
		MaxRetries:      3,
	}
}

// NewMantaClient creates a new MantaFS client
func NewMantaClient(config *Config) (*MantaClient, error) {
	if config == nil {
		config = DefaultConfig()
	}

	client := &MantaClient{
		endpoint:        config.Endpoint,
		reconnectPeriod: config.ReconnectPeriod,
	}

	if err := client.connect(config.Timeout); err != nil {
		log.Printf("[MantaClient] Initial connection failed: %v, will retry in background", err)
		go client.reconnectLoop()
	}

	return client, nil
}

// NewMantaClientForTest returns a MantaClient with connected=true for testing.
// For testing: no real connection; [Result] logs only.
func NewMantaClientForTest() *MantaClient {
	c := &MantaClient{endpoint: "test", reconnectPeriod: 0}
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return c
}

func (c *MantaClient) connect(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, c.endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to MantaFS at %s: %w", c.endpoint, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	log.Printf("[MantaClient] Connected to MantaFS at %s", c.endpoint)
	return nil
}

func (c *MantaClient) reconnectLoop() {
	for {
		time.Sleep(c.reconnectPeriod)
		c.mu.RLock()
		connected := c.connected
		c.mu.RUnlock()
		if connected {
			continue
		}
		log.Printf("[MantaClient] Attempting to reconnect to MantaFS...")
		if err := c.connect(10 * time.Second); err != nil {
			log.Printf("[MantaClient] Reconnection failed: %v", err)
		}
	}
}

// IsConnected returns the connection status
func (c *MantaClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Close closes the connection
func (c *MantaClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.connected = false
		return c.conn.Close()
	}
	return nil
}

// ============================================
// IntegrationClient 구현 (stub, 향후 Gluesys gRPC 연결 예정)
// ============================================

// PrepareDataset implements IntegrationClient.PrepareDataset.
func (c *MantaClient) PrepareDataset(ctx context.Context, req *PrepareDatasetRequest) (*PrepareDatasetResponse, error) {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return nil, &BackendError{
			Code:    BackendUnavailable,
			Op:      "PrepareDataset",
			Message: "MantaFS backend is not connected",
		}
	}

	log.Printf("[Result] PrepareDataset called")
	log.Printf("  workload_id=%s", req.WorkloadID)
	log.Printf("  namespace=%s", req.Namespace)
	log.Printf("  pod=%s", req.PodName)
	log.Printf("  dataset=%s", req.DatasetName)
	log.Printf("  node=%s", req.NodeName)
	log.Printf("  job_type=%s", req.JobType)
	log.Printf("  pattern=%s", req.AccessPattern)
	log.Printf("  paths=%v", req.DataPaths)

	// TODO(gluesys): 실제 MantaFS gRPC PrepareDataset RPC 호출로 교체
	return nil, &BackendError{
		Code:    NotImplemented,
		Op:      "PrepareDataset",
		Message: "not yet wired to Gluesys MantaFS API",
	}
}

// ReleaseDatasetHint implements IntegrationClient.ReleaseDatasetHint.
func (c *MantaClient) ReleaseDatasetHint(ctx context.Context, req *ReleaseDatasetRequest) error {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return &BackendError{
			Code:    BackendUnavailable,
			Op:      "ReleaseDatasetHint",
			Message: "MantaFS backend is not connected",
		}
	}

	log.Printf("[Result] ReleaseDatasetHint called")
	log.Printf("  workload_id=%s", req.WorkloadID)
	log.Printf("  namespace=%s", req.Namespace)
	log.Printf("  dataset=%s", req.DatasetName)
	log.Printf("  reason=%s", req.Reason)

	// TODO(gluesys): 실제 MantaFS gRPC ReleaseDatasetHint RPC 호출로 교체
	return &BackendError{
		Code:    NotImplemented,
		Op:      "ReleaseDatasetHint",
		Message: "not yet wired to Gluesys MantaFS API",
	}
}

// ReportPodPlacement implements IntegrationClient.ReportPodPlacement.
func (c *MantaClient) ReportPodPlacement(ctx context.Context, req *PodPlacementReportRequest) (*PodPlacementReportResponse, error) {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return nil, &BackendError{
			Code:    BackendUnavailable,
			Op:      "ReportPodPlacement",
			Message: "MantaFS backend is not connected",
		}
	}

	log.Printf("[Result] ReportPodPlacement called")
	log.Printf("  pod=%s", req.PodName)
	log.Printf("  namespace=%s", req.Namespace)
	log.Printf("  node=%s", req.NodeName)

	// TODO(gluesys): 실제 MantaFS gRPC ReportPodPlacement RPC 호출로 교체
	return nil, &BackendError{
		Code:    NotImplemented,
		Op:      "ReportPodPlacement",
		Message: "not yet wired to Gluesys MantaFS API",
	}
}

// ReportDatasetUsage implements IntegrationClient.ReportDatasetUsage.
func (c *MantaClient) ReportDatasetUsage(ctx context.Context, req *DatasetUsageReportRequest) (*DatasetUsageReportResponse, error) {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return nil, &BackendError{
			Code:    BackendUnavailable,
			Op:      "ReportDatasetUsage",
			Message: "MantaFS backend is not connected",
		}
	}

	log.Printf("[Result] ReportDatasetUsage called")
	log.Printf("  workload_id=%s", req.WorkloadID)
	log.Printf("  dataset=%s", req.DatasetName)
	log.Printf("  pattern=%s", req.AccessPattern)
	log.Printf("  paths=%v", req.DataPaths)

	// TODO(gluesys): 실제 MantaFS gRPC ReportDatasetUsage RPC 호출로 교체
	return nil, &BackendError{
		Code:    NotImplemented,
		Op:      "ReportDatasetUsage",
		Message: "not yet wired to Gluesys MantaFS API",
	}
}
