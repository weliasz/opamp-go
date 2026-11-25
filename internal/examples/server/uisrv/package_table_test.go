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
	const origin = "http://127.0.0.1:8000"
	contents := "test package"
	var downloads int
	client := &http.Client{Transport: packageDownloadTransport(func(r *http.Request) (*http.Response, error) {
		downloads++
		require.Equal(t, origin+"/smoke.txt", r.URL.String())
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(contents))}, nil
	})}
	handler := preparePackageOffer(origin, client)
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

	w = prepare(strings.Replace(rows, "127.0.0.1:8000", "127.0.0.1:8080", 1))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, 2, downloads)
}
