package registry

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxTarballSize = 200 * 1024 * 1024 // 200 MB

// ErrIntegrityMismatch is returned when the downloaded tarball's sha512 hash
// does not match the integrity field from the packument.
var ErrIntegrityMismatch = errors.New("registry: tarball integrity mismatch")

// DownloadTarball fetches tarballURL, streams the body through a sha512 hasher,
// verifies the result against the "sha512-<base64>" integrity string, and returns
// the raw bytes and the verified sha512 as a lowercase hex string.
//
// Returns ErrIntegrityMismatch if the hash doesn't match.
// Returns an error if the body exceeds maxTarballSize.
func DownloadTarball(ctx context.Context, client *http.Client, tarballURL, integrity string) ([]byte, string, error) {
	// Parse the integrity field: "sha512-<base64url-or-base64>"
	if !strings.HasPrefix(integrity, "sha512-") {
		return nil, "", fmt.Errorf("registry: unsupported integrity algorithm in %q (want sha512-...)", integrity)
	}
	b64 := strings.TrimPrefix(integrity, "sha512-")

	// SRI base64 may use URL-safe encoding (+ replaced with -, / with _).
	// Standard and URL-safe are interchangeable for verification; normalise to std.
	b64 = strings.ReplaceAll(b64, "-", "+")
	b64 = strings.ReplaceAll(b64, "_", "/")
	// Add padding if needed.
	switch len(b64) % 4 {
	case 2:
		b64 += "=="
	case 3:
		b64 += "="
	}

	expectedBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("registry: decode integrity base64 %q: %w", integrity, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("registry: build tarball request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("registry: fetch tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, "", fmt.Errorf("registry: tarball %q status %d: %s", tarballURL, resp.StatusCode, body)
	}

	hasher := sha512.New()
	limitedReader := io.LimitReader(resp.Body, maxTarballSize+1)

	var buf bytes.Buffer
	n, err := io.Copy(io.MultiWriter(&buf, hasher), limitedReader)
	if err != nil {
		return nil, "", fmt.Errorf("registry: read tarball body: %w", err)
	}
	if n > maxTarballSize {
		return nil, "", fmt.Errorf("registry: tarball exceeds %d byte limit", maxTarballSize)
	}

	actualHash := hasher.Sum(nil)
	if !bytes.Equal(actualHash, expectedBytes) {
		return nil, "", ErrIntegrityMismatch
	}

	sha512Hex := hex.EncodeToString(actualHash)
	return buf.Bytes(), sha512Hex, nil
}
