package uisrv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/proto"
)

type diskPackage struct {
	Type        string `json:"type"`
	Version     string `json:"version"`
	File        string `json:"file"`
	DownloadURL string `json:"downloadUrl"`
	Signature   []byte `json:"signature,omitempty"`
}

func loadPackageManifest(manifestPath string, offer *protobufs.PackagesAvailable) error {
	if manifestPath == "" {
		return fmt.Errorf("disk offers require -package-manifest")
	}
	root, err := os.OpenRoot(filepath.Dir(manifestPath))
	if err != nil {
		return err
	}
	defer root.Close()

	body, err := root.ReadFile(filepath.Base(manifestPath))
	if err != nil {
		return err
	}
	var manifest struct {
		Packages map[string]diskPackage `json:"packages"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return err
	}
	if manifest.Packages == nil {
		return fmt.Errorf("manifest must define a packages object (use {} to offer an empty assignment)")
	}

	offer.Packages = make(map[string]*protobufs.PackageAvailable, len(manifest.Packages))
	for name, entry := range manifest.Packages {
		typ, ok := protobufs.PackageType_value[entry.Type]
		if !ok {
			return fmt.Errorf("package %q has unknown type %q", name, entry.Type)
		}
		contentHash, err := hashPackageFile(root, entry.File)
		if err != nil {
			return fmt.Errorf("package %q: %w", name, err)
		}
		pkg := &protobufs.PackageAvailable{
			Type:    protobufs.PackageType(typ),
			Version: entry.Version,
			File: &protobufs.DownloadableFile{
				DownloadUrl: entry.DownloadURL,
				ContentHash: contentHash,
				Signature:   entry.Signature,
			},
		}
		pkg.Hash, err = packageMessageHash(pkg)
		if err != nil {
			return err
		}
		offer.Packages[name] = pkg
	}

	offer.AllPackagesHash, err = packageMessageHash(offer)
	return err
}

func hashPackageFile(root *os.Root, name string) ([]byte, error) {
	if !filepath.IsLocal(name) {
		return nil, fmt.Errorf("file must be relative to the manifest directory")
	}
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file %q must be a regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func packageMessageHash(msg proto.Message) ([]byte, error) {
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}
