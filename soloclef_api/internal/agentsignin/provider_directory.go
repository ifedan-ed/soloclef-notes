package agentsignin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// cacheFor is discovery.CACHE_SECONDS: an hour, after which both the metadata
// and the key set are read again.
const cacheFor = time.Hour

// fetchTimeout is discovery.FETCH_TIMEOUT_SECONDS.
const fetchTimeout = 10 * time.Second

// maxDocument caps what is read from the provider. The Python tier has no such
// cap because httpx holds the body in memory anyway; here it is the difference
// between a slow provider and an unbounded read.
const maxDocument = 1 << 20

// ProviderDirectory is one provider's metadata and key set, shared by every
// request, mirroring app/auth_oidc/discovery.py's ProviderDirectory.
//
// Both documents are re-read after an hour, and the key set also as soon as a
// token names a key id that is not in it, so a key rotation at the provider
// needs no restart here.
type ProviderDirectory struct {
	DiscoveryURL string
	// HTTPClient is overridable so a test can answer without a network.
	HTTPClient *http.Client
	// Now is overridable for tests only.
	Now func() time.Time

	mutex      sync.Mutex
	metadata   *ProviderMetadata
	metadataAt time.Time
	keys       []byte
	keysAt     time.Time
}

// Metadata is the discovery document, fetched at most once an hour.
func (d *ProviderDirectory) Metadata(ctx context.Context) (ProviderMetadata, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.metadataLocked(ctx)
}

// SigningKeys is the provider's JWKS document. A refresh is asked for when a
// token names a key id the cached set does not have.
func (d *ProviderDirectory) SigningKeys(ctx context.Context, refresh bool) ([]byte, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	metadata, err := d.metadataLocked(ctx)
	if err != nil {
		return nil, err
	}
	if !refresh && d.keys != nil && d.fresh(d.keysAt) {
		return d.keys, nil
	}

	document, err := d.get(ctx, metadata.JWKSURI)
	if err != nil {
		return nil, err
	}
	d.keys, d.keysAt = document, d.now()
	return d.keys, nil
}

func (d *ProviderDirectory) metadataLocked(ctx context.Context) (ProviderMetadata, error) {
	if d.metadata != nil && d.fresh(d.metadataAt) {
		return *d.metadata, nil
	}
	document, err := d.get(ctx, d.DiscoveryURL)
	if err != nil {
		return ProviderMetadata{}, err
	}
	metadata, err := ParseMetadata(document, d.DiscoveryURL)
	if err != nil {
		return ProviderMetadata{}, err
	}
	d.metadata, d.metadataAt = &metadata, d.now()
	return metadata, nil
}

func (d *ProviderDirectory) get(ctx context.Context, url string) ([]byte, error) {
	client := d.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	fetch, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(fetch, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", url, err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("could not read %s: status %d", url, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDocument))
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", url, err)
	}
	return body, nil
}

// defaultHTTPClient does not follow redirects, because httpx does not either:
// a discovery URL that answers 302 is a document this tier cannot read, not one
// to chase to wherever the redirect points.
var defaultHTTPClient = &http.Client{
	Timeout: fetchTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func (d *ProviderDirectory) fresh(fetchedAt time.Time) bool {
	return d.now().Sub(fetchedAt) < cacheFor
}

func (d *ProviderDirectory) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
