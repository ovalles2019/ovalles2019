package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/ovalles2019/eks-microservices-platform/internal/platform/httpx"
)

type authCtxKey int

const ctxKeyClientID authCtxKey = iota

// AnonymousClient is the rate-limit key used when authentication is disabled.
const AnonymousClient = "anonymous"

// APIKeyStore resolves an API key to a client identity.
//
// Keys are held only as SHA-256 digests and compared in constant time. Storing
// them in plaintext means a heap dump, a log line or a debug endpoint leaks
// every client's credential at once; comparing with == leaks key material
// through timing.
type APIKeyStore struct {
	byDigest map[string]string
}

// NewAPIKeyStore builds a store from a clientID -> key map.
func NewAPIKeyStore(keys map[string]string) *APIKeyStore {
	s := &APIKeyStore{byDigest: make(map[string]string, len(keys))}
	for clientID, key := range keys {
		if key == "" {
			continue
		}
		s.byDigest[digest(key)] = clientID
	}
	return s
}

// ParseKeySpec builds a store from a "client1:key1,client2:key2" string, the
// form the value arrives in from a mounted Secret.
func ParseKeySpec(spec string) *APIKeyStore {
	keys := make(map[string]string)
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		clientID, key, ok := strings.Cut(pair, ":")
		if !ok {
			continue
		}
		clientID, key = strings.TrimSpace(clientID), strings.TrimSpace(key)
		if clientID == "" || key == "" {
			continue
		}
		keys[clientID] = key
	}
	return NewAPIKeyStore(keys)
}

// Len returns the number of configured keys.
func (s *APIKeyStore) Len() int { return len(s.byDigest) }

// Lookup returns the client identity for a presented key.
func (s *APIKeyStore) Lookup(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	want := digest(key)

	// Every candidate is compared even after a match so the running time does
	// not reveal which key matched or how many keys are configured.
	var clientID string
	var found bool
	for stored, id := range s.byDigest {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(want)) == 1 {
			clientID, found = id, true
		}
	}
	return clientID, found
}

func digest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Authenticate rejects requests without a valid API key.
//
// When the store is empty authentication is skipped entirely, which is what
// makes `make dev` usable with no secrets. The chart refuses to render a
// production environment without keys, so this cannot silently ship open.
func Authenticate(store *APIKeyStore) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil || store.Len() == 0 {
				next.ServeHTTP(w, r.WithContext(withClient(r.Context(), AnonymousClient)))
				return
			}

			key := presentedKey(r)
			clientID, ok := store.Lookup(key)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="platform"`)
				httpx.WriteProblem(w, r, http.StatusUnauthorized, "unauthorized",
					"A valid API key is required. Send it as 'Authorization: Bearer <key>'.")
				return
			}
			next.ServeHTTP(w, r.WithContext(withClient(r.Context(), clientID)))
		})
	}
}

func presentedKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if key, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(key)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

func withClient(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, ctxKeyClientID, clientID)
}

// ClientFrom returns the authenticated client identity, or AnonymousClient.
func ClientFrom(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyClientID).(string); ok && id != "" {
		return id
	}
	return AnonymousClient
}

// RateLimitKey keys the limiter by authenticated client.
//
// Keying by client rather than source IP is what makes the limit meaningful
// behind a load balancer, where every request otherwise appears to come from a
// handful of NAT addresses.
func RateLimitKey(r *http.Request) string { return ClientFrom(r.Context()) }
