package uisrv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxPackageDownloadBytes = 128 << 20

var packageDownloadClient = &http.Client{
	Timeout: 45 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type packageRow struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	DownloadURL string `json:"downloadUrl"`
}

func preparePackageOffer(client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		decoder := json.NewDecoder(r.Body)
		var rows []packageRow
		if err := decoder.Decode(&rows); err != nil || rows == nil || len(rows) > 32 || decoder.Decode(new(json.RawMessage)) != io.EOF {
			http.Error(w, "Expected an array of up to 32 package rows", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		offer, err := packageOfferFromRows(ctx, client, rows)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := (protojson.MarshalOptions{Indent: "  ", EmitDefaultValues: true}).Marshal(offer)
		if err != nil {
			http.Error(w, "Could not encode the offer", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func packageOfferFromRows(ctx context.Context, client *http.Client, rows []packageRow) (*protobufs.PackagesAvailable, error) {
	offer := &protobufs.PackagesAvailable{Packages: make(map[string]*protobufs.PackageAvailable, len(rows))}
	for i, row := range rows {
		name := strings.TrimSpace(row.Name)
		version := strings.TrimSpace(row.Version)
		if version == "" {
			return nil, fmt.Errorf("row %d needs a version", i+1)
		}
		if _, exists := offer.Packages[name]; exists {
			return nil, fmt.Errorf("row %d repeats package name %q", i+1, name)
		}
		typ := protobufs.PackageType_PackageType_TopLevel
		if name != "" {
			prefix, suffix, ok := strings.Cut(name, ":")
			if !ok || prefix == "" || suffix == "" {
				return nil, fmt.Errorf("row %d needs an addon name like file:smoke", i+1)
			}
			typ = protobufs.PackageType_PackageType_Addon
		}

		downloadURL := strings.TrimSpace(row.DownloadURL)
		u, err := url.Parse(downloadURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
			(u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("row %d needs an absolute HTTP or HTTPS URL without credentials or a fragment", i+1)
		}
		contentHash, err := hashPackageDownload(ctx, client, u.String())
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}
		pkg := &protobufs.PackageAvailable{
			Type: typ, Version: version,
			File: &protobufs.DownloadableFile{DownloadUrl: downloadURL, ContentHash: contentHash},
		}
		pkg.Hash, err = packageMessageHash(pkg)
		if err != nil {
			return nil, err
		}
		offer.Packages[name] = pkg
	}
	var err error
	offer.AllPackagesHash, err = packageMessageHash(offer)
	return offer, err
}

func hashPackageDownload(ctx context.Context, client *http.Client, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid download URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch package from the artifact server")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact server returned HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(resp.Body, maxPackageDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read package from the artifact server")
	}
	if n > maxPackageDownloadBytes {
		return nil, fmt.Errorf("package exceeds 128 MiB")
	}
	return hash.Sum(nil), nil
}
