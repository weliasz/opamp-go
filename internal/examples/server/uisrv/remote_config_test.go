package uisrv

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPrepareRemoteConfig(t *testing.T) {
	old := configFileRoot
	configFileRoot = t.TempDir()
	t.Cleanup(func() { configFileRoot = old })
	require.NoError(t, os.WriteFile(filepath.Join(configFileRoot, "agent.yaml"), []byte("receivers: {}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(configFileRoot, "addon.conf"), []byte("state = disabled"), 0o600))
	request := `[ {"name":"","path":"agent.yaml","contentType":"text/yaml"}, {"name":"splunk_ta:app","path":"addon.conf","role":"addon-config"} ]`
	recorder := httptest.NewRecorder()
	prepareRemoteConfig(recorder, httptest.NewRequest(http.MethodPost, "/prepare_remote_config", strings.NewReader(request)))
	require.Equal(t, http.StatusOK, recorder.Code)
	var result protobufs.AgentRemoteConfig
	require.NoError(t, protojson.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, "receivers: {}", string(result.Config.ConfigMap[""].Body))
	require.Equal(t, "addon-config", result.Config.ConfigMap["splunk_ta:app"].Role)
	require.Len(t, result.ConfigHash, 32)

	recorder = httptest.NewRecorder()
	prepareRemoteConfig(recorder, httptest.NewRequest(http.MethodPost, "/prepare_remote_config", strings.NewReader(`[{"path":"../outside"}]`)))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
