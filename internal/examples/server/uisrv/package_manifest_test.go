package uisrv

import (
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/open-telemetry/opamp-go/internal/examples/server/data"
	"github.com/open-telemetry/opamp-go/protobufs"
)

type packageConnection struct {
	sent *protobufs.ServerToAgent
}

func (*packageConnection) Connection() net.Conn { return nil }
func (*packageConnection) Disconnect() error    { return nil }
func (c *packageConnection) Send(_ context.Context, msg *protobufs.ServerToAgent) error {
	c.sent = msg
	return nil
}

func TestOfferPackagesFromDiskAndRenderStatus(t *testing.T) {
	dir := t.TempDir()
	artifact := []byte("package artifact")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "collector"), artifact, 0o600))
	manifest := filepath.Join(dir, "packages.json")
	require.NoError(t, os.WriteFile(manifest, []byte(`{"packages":{"":{"type":"PackageType_TopLevel","version":"2","file":"collector","downloadUrl":"https://files.example/collector"}}}`), 0o600))
	previousPath := packageManifest
	packageManifest = manifest
	t.Cleanup(func() { packageManifest = previousPath })

	uid := uuid.New()
	conn := &packageConnection{}
	agent := data.AllAgents.FindOrCreateAgent(data.InstanceId(uid), conn)
	t.Cleanup(func() { data.AllAgents.RemoveConnection(conn) })
	agent.UpdateStatus(&protobufs.AgentToServer{SequenceNum: 1}, &protobufs.ServerToAgent{})

	form := url.Values{"instanceid": {uid.String()}, "source": {"disk"}}
	req := httptest.NewRequest(http.MethodPost, "/send_packages_available", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	sendPackagesAvailable(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	require.Equal(t, uid[:], conn.sent.GetInstanceUid())
	offer := conn.sent.GetPackagesAvailable()
	pkg := offer.GetPackages()[""]
	sum := sha256.Sum256(artifact)
	require.Equal(t, sum[:], pkg.GetFile().GetContentHash())
	require.Len(t, pkg.GetHash(), sha256.Size)
	require.Len(t, offer.GetAllPackagesHash(), sha256.Size)

	agent.UpdateStatus(&protobufs.AgentToServer{
		SequenceNum: 2,
		PackageStatuses: &protobufs.PackageStatuses{
			Packages: map[string]*protobufs.PackageStatus{
				"": {AgentHasVersion: "2", Status: protobufs.PackageStatusEnum_PackageStatusEnum_Installed},
			},
		},
	}, &protobufs.ServerToAgent{})
	// A subsequent incremental update can omit PackageStatuses.
	agent.UpdateStatus(&protobufs.AgentToServer{SequenceNum: 3}, &protobufs.ServerToAgent{})
	w = httptest.NewRecorder()
	renderAgent(w, httptest.NewRequest(http.MethodGet, "/agent?instanceid="+uid.String(), nil))
	require.Contains(t, w.Body.String(), "PackageStatusEnum_Installed")
	require.Contains(t, w.Body.String(), "agentHasVersion")
	require.Contains(t, w.Body.String(), "Offer packages from disk")
}
