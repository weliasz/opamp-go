package uisrv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var configFileRoot = "/home/splunker/configs"

const maxConfigFileBytes = 4 << 20

type remoteConfigRow struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Role        string `json:"role"`
}

func prepareRemoteConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var rows []remoteConfigRow
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&rows); err != nil || rows == nil || len(rows) > 32 || dec.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "Expected an array of up to 32 configuration rows", http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(configFileRoot)
	if err != nil {
		http.Error(w, "Configuration directory is unavailable", http.StatusBadRequest)
		return
	}
	defer root.Close()
	config := &protobufs.AgentConfigMap{ConfigMap: make(map[string]*protobufs.AgentConfigObject)}
	for i, row := range rows {
		if _, exists := config.ConfigMap[row.Name]; exists {
			http.Error(w, fmt.Sprintf("row %d repeats a name", i+1), http.StatusBadRequest)
			return
		}
		filePath := strings.TrimSpace(row.Path)
		if filepath.IsAbs(filePath) {
			filePath, err = filepath.Rel(configFileRoot, filePath)
			if err != nil {
				http.Error(w, "Invalid file path", http.StatusBadRequest)
				return
			}
		}
		info, statErr := root.Stat(filePath)
		if statErr != nil || !info.Mode().IsRegular() {
			http.Error(w, "Configuration must be a regular file within the configuration directory", http.StatusBadRequest)
			return
		}
		file, err := root.Open(filePath)
		if err != nil {
			http.Error(w, fmt.Sprintf("row %d: cannot open file within configuration directory", i+1), http.StatusBadRequest)
			return
		}
		info, statErr = file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			http.Error(w, "Configuration must be a regular file", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(file, maxConfigFileBytes+1))
		file.Close()
		if err != nil || len(body) > maxConfigFileBytes {
			http.Error(w, "Cannot read configuration or file exceeds 4 MiB", http.StatusBadRequest)
			return
		}
		config.ConfigMap[row.Name] = &protobufs.AgentConfigObject{Body: body, ContentType: row.ContentType, Role: row.Role}
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(config)
	if err != nil {
		http.Error(w, "Cannot encode configuration", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(encoded)
	body, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(&protobufs.AgentRemoteConfig{Config: config, ConfigHash: sum[:]})
	if err != nil {
		http.Error(w, "Cannot encode message", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
