package uisrv

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
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
		urls = append(urls, r.URL.String())
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
