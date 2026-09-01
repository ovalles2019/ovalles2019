// Package catalog owns the device registry. It is the only service that talks
// to the devices database; every other service reaches this data through the
// catalog API, which is what keeps the schema an implementation detail rather
// than a shared contract nobody can change.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	ErrNotFound      = errors.New("device not found")
	ErrAlreadyExists = errors.New("device already exists")
	ErrInvalid       = errors.New("device is invalid")
)

// Device is a registered piece of fleet hardware.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Site      string    `json:"site"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// idPattern bounds device IDs to characters that are safe in a URL path
// segment, a metric label and a log line without escaping.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// Validate normalises and checks a device, returning ErrInvalid on bad input.
//
// Validation lives on the domain type rather than in the HTTP handler so it
// cannot be bypassed by a second entry point (a seeder, a migration backfill, a
// future gRPC surface) that forgets to call it.
func (d *Device) Validate() error {
	d.ID = strings.ToLower(strings.TrimSpace(d.ID))
	d.Name = strings.TrimSpace(d.Name)
	d.Site = strings.ToLower(strings.TrimSpace(d.Site))
	d.Kind = strings.ToLower(strings.TrimSpace(d.Kind))

	switch {
	case d.ID == "":
		return fmt.Errorf("%w: id is required", ErrInvalid)
	case !idPattern.MatchString(d.ID):
		return fmt.Errorf("%w: id must be 2-63 characters of lowercase letters, digits and hyphens, starting with a letter or digit", ErrInvalid)
	case d.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalid)
	case len(d.Name) > 200:
		return fmt.Errorf("%w: name must be at most 200 characters", ErrInvalid)
	case d.Site == "":
		return fmt.Errorf("%w: site is required", ErrInvalid)
	case len(d.Site) > 100:
		return fmt.Errorf("%w: site must be at most 100 characters", ErrInvalid)
	case len(d.Kind) > 100:
		return fmt.Errorf("%w: kind must be at most 100 characters", ErrInvalid)
	}

	if d.Kind == "" {
		d.Kind = "unknown"
	}
	return nil
}

// ListOptions bounds a listing query.
//
// There is no unbounded list: an endpoint that returns every row works fine on
// a seeded dev database and falls over the first time production has a million
// devices.
type ListOptions struct {
	Site  string
	Limit int
	// Cursor is the last ID from the previous page. Keyset pagination rather
	// than OFFSET, because OFFSET makes the database scan and discard every
	// skipped row, so deep pages get linearly slower.
	Cursor string
}

const (
	defaultLimit = 50
	maxLimit     = 200
)

// Normalise clamps the options to the supported range.
func (o *ListOptions) Normalise() {
	o.Site = strings.ToLower(strings.TrimSpace(o.Site))
	o.Cursor = strings.ToLower(strings.TrimSpace(o.Cursor))
	if o.Limit <= 0 {
		o.Limit = defaultLimit
	}
	if o.Limit > maxLimit {
		o.Limit = maxLimit
	}
}

// Page is one page of devices plus the cursor for the next.
type Page struct {
	Devices    []Device `json:"devices"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// Repository is the persistence port.
//
// The service depends on this interface rather than on a concrete Postgres
// type, which is what lets the handler tests run against an in-memory store
// with no database and no container runtime.
type Repository interface {
	Create(ctx context.Context, d Device) (Device, error)
	Get(ctx context.Context, id string) (Device, error)
	List(ctx context.Context, opts ListOptions) (Page, error)
	Ping(ctx context.Context) error
}
