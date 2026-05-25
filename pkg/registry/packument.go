package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ErrNoIntegrity is returned when a packument version has no sha512 integrity field.
// This typically indicates an old package predating the integrity field.
var ErrNoIntegrity = errors.New("registry: version has no sha512 integrity")

// PackumentVersion holds the fields we need from a specific version entry.
type PackumentVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"` // "sha512-<base64>" — canonical, verified
		SHA1Sum   string `json:"shasum"`    // legacy sha1, recorded but never used for verification
	} `json:"dist"`
}

// Packument is the minimal packument shape we parse from the registry.
type Packument struct {
	Name     string                      `json:"name"`
	Rev      string                      `json:"_rev"`
	DistTags map[string]string           `json:"dist-tags"`
	Versions map[string]PackumentVersion `json:"versions"`
}

// FetchPackument GETs <baseURL>/<urlEncodedName> and parses the packument.
// The User-Agent header is set from the userAgent parameter.
func FetchPackument(ctx context.Context, client *http.Client, baseURL, userAgent, name string) (*Packument, error) {
	escapedName := url.PathEscape(name)
	reqURL := fmt.Sprintf("%s/%s", baseURL, escapedName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("registry: build packument request for %q: %w", name, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry: fetch packument %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("registry: packument %q status %d: %s", name, resp.StatusCode, body)
	}

	var p Packument
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("registry: decode packument %q: %w", name, err)
	}

	return &p, nil
}
