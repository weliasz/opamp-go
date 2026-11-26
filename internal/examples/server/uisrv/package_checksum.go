package uisrv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	maxChecksumBytes = 1 << 20
	checksumTimeout  = 4 * time.Second
)

func packageContentHash(ctx context.Context, client *http.Client, artifact *url.URL) ([]byte, error) {
	if digest := findPackageChecksum(ctx, client, artifact); digest != nil {
		return digest, nil
	}
	return hashPackageDownload(ctx, client, artifact.String())
}

func findPackageChecksum(ctx context.Context, client *http.Client, artifact *url.URL) []byte {
	if artifact.Path == "" || strings.HasSuffix(artifact.Path, "/") {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, checksumTimeout)
	defer cancel()

	name := path.Base(artifact.Path)
	for _, candidate := range []struct {
		filename string
		sidecar  bool
	}{
		{name + ".sha256", true},
		{name + ".sha256sum", true},
		{"SHA256SUMS", false},
		{"checksums.txt", false},
	} {
		// A query string may authorize just the artifact. Do not send it to
		// a different file in the directory.
		checksumURL := artifact.ResolveReference(&url.URL{Path: candidate.filename})
		body := fetchChecksum(probeCtx, client, checksumURL)
		if digest := parsePackageChecksum(string(body), name, candidate.sidecar); digest != nil {
			return digest
		}
	}
	return nil
}

func fetchChecksum(ctx context.Context, client *http.Client, checksumURL *url.URL) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL.String(), http.NoBody)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > maxChecksumBytes {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumBytes+1))
	if err != nil || len(body) > maxChecksumBytes {
		return nil
	}
	return body
}

func parsePackageChecksum(body, name string, sidecar bool) []byte {
	if sidecar {
		if digest := decodeSHA256(strings.TrimSpace(body)); digest != nil {
			return digest
		}
	}

	var found []byte
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		var filename, hexDigest string
		// BSD format: SHA256 (package.tgz) = <hex>
		if entry, ok := strings.CutPrefix(line, "SHA256 ("); ok {
			filename, hexDigest, _ = strings.Cut(entry, ") = ")
		} else if split := strings.IndexAny(line, " \t"); split > 0 {
			// GNU format: <hex>  package.tgz or <hex> *package.tgz
			hexDigest = line[:split]
			filename = strings.TrimPrefix(strings.TrimLeft(line[split:], " \t"), "*")
		}
		if strings.TrimPrefix(filename, "./") != name {
			continue
		}
		digest := decodeSHA256(hexDigest)
		if digest == nil || (found != nil && !bytes.Equal(found, digest)) {
			return nil
		}
		found = digest
	}
	return found
}

func decodeSHA256(value string) []byte {
	if len(value) != sha256.Size*2 {
		return nil
	}
	digest, err := hex.DecodeString(value)
	if err != nil {
		return nil
	}
	return digest
}
