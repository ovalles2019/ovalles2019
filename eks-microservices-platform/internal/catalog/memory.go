package catalog

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryRepository is an in-process Repository.
//
// It exists so the HTTP layer can be tested exhaustively with no database and
// no container runtime, and so `make dev` runs the API with a single command.
// It is never wired into a deployed environment: cmd/catalog requires a DSN
// unless CATALOG_STORE is explicitly set to "memory".
type MemoryRepository struct {
	mu      sync.RWMutex
	devices map[string]Device
	now     func() time.Time
	// failPing simulates a dependency outage in readiness tests.
	failPing error
}

// NewMemoryRepository returns an empty in-memory store.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{devices: make(map[string]Device), now: time.Now}
}

// SetPingError makes Ping fail, for readiness tests.
func (r *MemoryRepository) SetPingError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failPing = err
}

// Ping reports store health.
func (r *MemoryRepository) Ping(context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.failPing
}

// Create inserts a device, reporting ErrAlreadyExists on a duplicate ID.
func (r *MemoryRepository) Create(_ context.Context, d Device) (Device, error) {
	if err := d.Validate(); err != nil {
		return Device{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.devices[d.ID]; exists {
		return Device{}, fmt.Errorf("%w: %s", ErrAlreadyExists, d.ID)
	}
	now := r.now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	r.devices[d.ID] = d
	return d, nil
}

// Get returns one device, or ErrNotFound.
func (r *MemoryRepository) Get(_ context.Context, id string) (Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	d, ok := r.devices[id]
	if !ok {
		return Device{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return d, nil
}

// List returns one page of devices, matching the Postgres ordering and keyset
// pagination semantics so tests written against this store stay meaningful.
func (r *MemoryRepository) List(_ context.Context, opts ListOptions) (Page, error) {
	opts.Normalise()

	r.mu.RLock()
	matched := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		if opts.Site != "" && d.Site != opts.Site {
			continue
		}
		if opts.Cursor != "" && d.ID <= opts.Cursor {
			continue
		}
		matched = append(matched, d)
	}
	r.mu.RUnlock()

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	page := Page{Devices: matched}
	if len(matched) > opts.Limit {
		page.Devices = matched[:opts.Limit]
		page.NextCursor = page.Devices[len(page.Devices)-1].ID
	}
	return page, nil
}
