package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

func newTestHandler(t *testing.T) (http.Handler, *MemoryRepository) {
	t.Helper()
	repo := NewMemoryRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(repo, logger)
	return httpx.Chain(h.Routes(), httpx.RequestID(), httpx.Logging(logger)), repo
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateAndGetDevice(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/v1/devices", `{"id":"pump-01","name":"West Pump 1","site":"west","kind":"pump"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/devices/pump-01" {
		t.Errorf("Location = %q, want /v1/devices/pump-01", loc)
	}

	var created Device
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("server-assigned timestamps were not populated")
	}

	got := do(t, h, http.MethodGet, "/v1/devices/pump-01", "")
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", got.Code)
	}
}

func TestCreateRejectsDuplicateWithConflict(t *testing.T) {
	h, _ := newTestHandler(t)
	body := `{"id":"pump-01","name":"West Pump 1","site":"west"}`

	if rec := do(t, h, http.MethodPost, "/v1/devices", body); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", rec.Code)
	}
	rec := do(t, h, http.MethodPost, "/v1/devices", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", rec.Code)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	h, _ := newTestHandler(t)

	cases := []struct{ name, body string }{
		{"missing id", `{"name":"x","site":"west"}`},
		{"missing name", `{"id":"pump-01","site":"west"}`},
		{"missing site", `{"id":"pump-01","name":"x"}`},
		{"id too short", `{"id":"p","name":"x","site":"west"}`},
		{"id with path traversal", `{"id":"../../etc/passwd","name":"x","site":"west"}`},
		{"id with spaces", `{"id":"pump 01","name":"x","site":"west"}`},
		{"id with url characters", `{"id":"pump%2f01","name":"x","site":"west"}`},
		{"name too long", fmt.Sprintf(`{"id":"pump-01","name":%q,"site":"west"}`, strings.Repeat("x", 201))},
		{"unknown field", `{"id":"pump-01","name":"x","site":"west","admin":true}`},
		{"not json", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/v1/devices", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestCreateNormalisesInput(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/v1/devices", `{"id":"  PUMP-01 ","name":"  West Pump  ","site":" WEST ","kind":"PUMP"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}

	var d Device
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d.ID != "pump-01" || d.Site != "west" || d.Kind != "pump" {
		t.Fatalf("device = %+v; ids and sites must be normalised so lookups are case-insensitive", d)
	}
	if d.Name != "West Pump" {
		t.Fatalf("name = %q, want whitespace trimmed but case preserved", d.Name)
	}
}

func TestCreateIgnoresClientSuppliedTimestamps(t *testing.T) {
	// A client must not be able to backdate a record.
	h, _ := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/v1/devices",
		`{"id":"pump-01","name":"x","site":"west","created_at":"1990-01-01T00:00:00Z","updated_at":"1990-01-01T00:00:00Z"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}

	var d Device
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d.CreatedAt.Year() == 1990 {
		t.Fatal("the client's created_at was accepted; timestamps must be server-assigned")
	}
}

func TestGetUnknownDeviceReturns404(t *testing.T) {
	h, _ := newTestHandler(t)

	rec := do(t, h, http.MethodGet, "/v1/devices/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not problem+json: %v", err)
	}
	if p.Code != "device_not_found" {
		t.Errorf("code = %q, want device_not_found", p.Code)
	}
}

func TestListPaginatesWithKeysetCursor(t *testing.T) {
	h, _ := newTestHandler(t)

	for i := 0; i < 10; i++ {
		body := fmt.Sprintf(`{"id":"dev-%02d","name":"Device %d","site":"west"}`, i, i)
		if rec := do(t, h, http.MethodPost, "/v1/devices", body); rec.Code != http.StatusCreated {
			t.Fatalf("seed %d: status %d", i, rec.Code)
		}
	}

	first := do(t, h, http.MethodGet, "/v1/devices?limit=4", "")
	var page Page
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Devices) != 4 {
		t.Fatalf("page size = %d, want 4", len(page.Devices))
	}
	if page.NextCursor != "dev-03" {
		t.Fatalf("next_cursor = %q, want dev-03", page.NextCursor)
	}

	// Walk every page and confirm the full set is covered exactly once.
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 10; i++ {
		url := "/v1/devices?limit=4"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		var p Page
		_ = json.Unmarshal(do(t, h, http.MethodGet, url, "").Body.Bytes(), &p)
		for _, d := range p.Devices {
			if seen[d.ID] {
				t.Fatalf("device %s was returned on two pages", d.ID)
			}
			seen[d.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 10 {
		t.Fatalf("pagination covered %d of 10 devices", len(seen))
	}
}

func TestListFiltersBySite(t *testing.T) {
	h, _ := newTestHandler(t)

	for _, seed := range []string{
		`{"id":"dev-w1","name":"a","site":"west"}`,
		`{"id":"dev-w2","name":"b","site":"west"}`,
		`{"id":"dev-e1","name":"c","site":"east"}`,
	} {
		do(t, h, http.MethodPost, "/v1/devices", seed)
	}

	var page Page
	_ = json.Unmarshal(do(t, h, http.MethodGet, "/v1/devices?site=west", "").Body.Bytes(), &page)
	if len(page.Devices) != 2 {
		t.Fatalf("got %d devices for site=west, want 2", len(page.Devices))
	}
	for _, d := range page.Devices {
		if d.Site != "west" {
			t.Fatalf("device %s has site %q, want west", d.ID, d.Site)
		}
	}
}

func TestListClampsLimit(t *testing.T) {
	// An unbounded page size is a denial-of-service vector against both the
	// database and this pod's memory.
	opts := ListOptions{Limit: 100000}
	opts.Normalise()
	if opts.Limit != maxLimit {
		t.Fatalf("limit = %d, want it clamped to %d", opts.Limit, maxLimit)
	}

	opts = ListOptions{}
	opts.Normalise()
	if opts.Limit != defaultLimit {
		t.Fatalf("default limit = %d, want %d", opts.Limit, defaultLimit)
	}
}

func TestListRejectsMalformedLimit(t *testing.T) {
	h, _ := newTestHandler(t)

	for _, raw := range []string{"abc", "-1", "1.5"} {
		rec := do(t, h, http.MethodGet, "/v1/devices?limit="+raw, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s status = %d, want 400", raw, rec.Code)
		}
	}
}

func TestRepositoryErrorsAreNotLeaked(t *testing.T) {
	// An unclassified repository error is the kind that carries a DSN or a SQL
	// fragment, so the handler must return a generic body.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := httpx.Chain(NewHandler(&failingRepo{}, logger).Routes(), httpx.RequestID(), httpx.Logging(logger))

	rec := do(t, h, http.MethodGet, "/v1/devices/pump-01", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	for _, leak := range []string{"password", "10.0.1.5", "SELECT"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("response leaked %q: %s", leak, rec.Body)
		}
	}
}

type failingRepo struct{}

func (f *failingRepo) Create(_ context.Context, _ Device) (Device, error) {
	return Device{}, f.err()
}
func (f *failingRepo) Get(_ context.Context, _ string) (Device, error) { return Device{}, f.err() }
func (f *failingRepo) List(_ context.Context, _ ListOptions) (Page, error) {
	return Page{}, f.err()
}
func (f *failingRepo) Ping(_ context.Context) error { return f.err() }
func (f *failingRepo) err() error {
	return errors.New(`SELECT * FROM devices: dial tcp 10.0.1.5:5432: password authentication failed`)
}
