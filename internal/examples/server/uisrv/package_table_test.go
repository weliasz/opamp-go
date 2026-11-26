package uisrv

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/encoding/protojson"
)

type packageDownloadTransport func(*http.Request) (*http.Response, error)

func (f packageDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPreparePackageOffer(t *testing.T) {
	var urls []string
	contents := "test package"
	client := &http.Client{Transport: packageDownloadTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/smoke.txt", "/other.tgz":
			urls = append(urls, r.URL.String())
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(contents))}, nil
	})}
	handler := preparePackageOffer(client)
	prepare := func(input string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/prepare_package_offer", strings.NewReader(input))
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(w, r)
		return w
	}
	const rows = `[{"name":"file:smoke","version":"1","downloadUrl":"http://127.0.0.1:8000/smoke.txt"}]`
	w := prepare(rows)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var offer protobufs.PackagesAvailable
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &offer))
	pkg := offer.GetPackages()["file:smoke"]
	require.Equal(t, protobufs.PackageType_PackageType_Addon, pkg.GetType())
	sum := sha256.Sum256([]byte(contents))
	require.Equal(t, sum[:], pkg.GetFile().GetContentHash())
	require.Len(t, pkg.GetHash(), sha256.Size)
	require.Len(t, offer.GetAllPackagesHash(), sha256.Size)

	contents = "updated package"
	w = prepare(rows)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var updated protobufs.PackagesAvailable
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &updated))
	require.NotEqual(t, pkg.GetHash(), updated.GetPackages()["file:smoke"].GetHash())
	require.NotEqual(t, offer.GetAllPackagesHash(), updated.GetAllPackagesHash())

	const mixedRows = `[
		{"name":"file:smoke","version":"1","downloadUrl":"http://127.0.0.1:8000/smoke.txt"},
		{"name":"file:other","version":"1","downloadUrl":"https://packages.example.com/other.tgz"}
	]`
	w = prepare(mixedRows)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, protojson.Unmarshal(w.Body.Bytes(), &updated))
	require.Len(t, updated.GetPackages(), 2)
	require.Equal(t, "https://packages.example.com/other.tgz", urls[3])

	w = prepare(strings.Replace(rows, "http://127.0.0.1:8000/smoke.txt", "file:///tmp/smoke.txt", 1))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Len(t, urls, 4)
}

func TestHashPackageDownloadFollowsRedirect(t *testing.T) {
	client := *packageDownloadClient
	const start = "https://packages.example.com/latest"
	const target = "https://cdn.example.org/package.tgz"
	client.Transport = packageDownloadTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case start:
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {target}},
				Body:       http.NoBody,
			}, nil
		case target:
			require.Empty(t, r.Header.Get("Referer"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("artifact"))}, nil
		default:
			t.Fatalf("unexpected URL %s", r.URL)
			return nil, nil
		}
	})
	got, err := hashPackageDownload(t.Context(), &client, start)
	require.NoError(t, err)
	want := sha256.Sum256([]byte("artifact"))
	require.Equal(t, want[:], got)
}

func TestPackageContentHashUsesChecksumWithoutDownloadingArtifact(t *testing.T) {
	const artifactURL = "https://packages.example.com/releases/smoke%20test.tgz?token=artifact-only"
	const checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct {
		name string
		path string
		body string
		hits int
	}{
		{"bare sidecar", "/releases/smoke test.tgz.sha256", checksum + "\n", 1},
		{"named sidecar", "/releases/smoke test.tgz.sha256sum", checksum + " *smoke test.tgz\n", 2},
		{"aggregate", "/releases/SHA256SUMS", checksum + "  other.tgz\nSHA256 (smoke test.tgz) = " + checksum + "\n", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: packageDownloadTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				require.Empty(t, r.URL.RawQuery)
				require.NotEqual(t, "/releases/smoke test.tgz", r.URL.Path)
				if r.URL.Path != tt.path {
					return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}
			artifact, err := url.Parse(artifactURL)
			require.NoError(t, err)
			got, err := packageContentHash(t.Context(), client, artifact)
			require.NoError(t, err)
			require.Equal(t, decodeSHA256(checksum), got)
			require.Equal(t, tt.hits, requests)
		})
	}
}
