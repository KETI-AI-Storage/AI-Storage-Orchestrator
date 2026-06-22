package etri

import (
	"context"
	"sync"
)

// Repository defines storage operations for ETRI workload metadata and status.
// In this phase it is intentionally in-memory and stub-friendly.
type Repository interface {
	SaveWorkloadMetadata(ctx context.Context, meta *EtriWorkloadMetadata) error
	GetWorkloadMetadata(ctx context.Context, ref WorkloadRef) (*EtriWorkloadMetadata, error)

	SaveWorkloadStatus(ctx context.Context, status *WorkloadStatusResponse) error
	GetWorkloadStatus(ctx context.Context, ref WorkloadRef) (*WorkloadStatusResponse, error)
}

// InMemoryRepository is a simple implementation used as default/mock repository.
type InMemoryRepository struct {
	mu       sync.RWMutex
	metadata map[string]*EtriWorkloadMetadata
	status   map[string]*WorkloadStatusResponse
}

// NewInMemoryRepository creates a new in-memory repository instance.
func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		metadata: make(map[string]*EtriWorkloadMetadata),
		status:   make(map[string]*WorkloadStatusResponse),
	}
}

func makeKey(namespace, name string) string {
	return namespace + "/" + name
}

// SaveWorkloadMetadata stores or updates metadata.
func (r *InMemoryRepository) SaveWorkloadMetadata(_ context.Context, meta *EtriWorkloadMetadata) error {
	if meta == nil {
		return nil
	}
	key := makeKey(meta.Namespace, meta.WorkloadName)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadata[key] = meta
	return nil
}

// GetWorkloadMetadata retrieves metadata, if present.
func (r *InMemoryRepository) GetWorkloadMetadata(_ context.Context, ref WorkloadRef) (*EtriWorkloadMetadata, error) {
	key := makeKey(ref.Namespace, ref.WorkloadName)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if meta, ok := r.metadata[key]; ok {
		return meta, nil
	}
	return nil, nil
}

// SaveWorkloadStatus stores or updates status.
func (r *InMemoryRepository) SaveWorkloadStatus(_ context.Context, status *WorkloadStatusResponse) error {
	if status == nil {
		return nil
	}
	key := makeKey(status.Namespace, status.WorkloadName)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status[key] = status
	return nil
}

// GetWorkloadStatus retrieves status, if present.
func (r *InMemoryRepository) GetWorkloadStatus(_ context.Context, ref WorkloadRef) (*WorkloadStatusResponse, error) {
	key := makeKey(ref.Namespace, ref.WorkloadName)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if st, ok := r.status[key]; ok {
		return st, nil
	}
	return nil, nil
}

