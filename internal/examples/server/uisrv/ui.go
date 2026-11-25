package uisrv

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/internal/examples/certs"
	"github.com/open-telemetry/opamp-go/internal/examples/html"
	"github.com/open-telemetry/opamp-go/internal/examples/server/data"
	"github.com/open-telemetry/opamp-go/protobufs"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	htmlDir         string
	packageManifest string
	srv             *http.Server
)

var logger = log.New(log.Default().Writer(), "[UI] ", log.Default().Flags()|log.Lmsgprefix|log.Lmicroseconds)

const examplePackagesAvailableJSON = `{
  "packages": {
    "": {
      "type": "PackageType_TopLevel",
      "version": "1.0.0",
      "file": {
        "downloadUrl": "https://downloads.example.com/package.tar.gz",
        "contentHash": ""
      },
      "hash": ""
    }
  },
  "allPackagesHash": ""
}`

type agentPageData struct {
	*data.Agent
	PackagesAvailableJSON  string
	PackagesAvailableError string
	AgentAcceptsPackages   bool
	DiskOffersEnabled      bool
	PackageStatusesJSON    string
}

func Start(rootDir, manifestPath string) {
	packageManifest = manifestPath
	htmlDir = path.Join(rootDir, "uisrv/html")

	mux := http.NewServeMux()
	mux.HandleFunc("/", renderRoot)
	mux.HandleFunc("/agent", renderAgent)
	mux.HandleFunc("/save_config", saveCustomConfigForInstance)
	mux.HandleFunc("/rotate_client_cert", rotateInstanceClientCert)
	mux.HandleFunc("/opamp_connection_settings", opampConnectionSettings)
	mux.HandleFunc("/send_custom_message", sendCustomMessage)
	mux.HandleFunc("/send_packages_available", sendPackagesAvailable)
	srv = &http.Server{
		Addr:    "0.0.0.0:4321",
		Handler: mux,
	}
	go srv.ListenAndServe()

	logger.Printf("Admin UI started, available at http://localhost:4321")
}

func Shutdown() {
	srv.Shutdown(context.Background())
}

func renderTemplate(w http.ResponseWriter, htmlTemplateFile string, data interface{}) {
	t, err := template.ParseFS(html.HtmlFS, "html/*")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Printf("Error parsing html template %s: %v", htmlTemplateFile, err)
		return
	}

	err = t.Lookup(htmlTemplateFile).Execute(w, data)
	if err != nil {
		// It is too late to send an HTTP status code since content is already written.
		// We can just log the error.
		logger.Printf("Error writing html content %s: %v", htmlTemplateFile, err)
		return
	}
}

func renderRoot(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "root.html", data.AllAgents.GetAllAgentsReadonlyClone())
}

func renderAgent(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(r.URL.Query().Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	agent := data.AllAgents.GetAgentReadonlyClone(data.InstanceId(uid))
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	renderAgentPage(w, agent, examplePackagesAvailableJSON, "")
}

func renderAgentPage(w http.ResponseWriter, agent *data.Agent, packagesJSON, packagesError string) {
	var statusJSON string
	if status := agent.Status.GetPackageStatuses(); status != nil {
		statusJSON = (protojson.MarshalOptions{Indent: "  ", EmitDefaultValues: true}).Format(status)
	}
	renderTemplate(w, "agent.html", agentPageData{
		Agent:                  agent,
		DiskOffersEnabled:      packageManifest != "",
		PackageStatusesJSON:    statusJSON,
		PackagesAvailableJSON:  packagesJSON,
		PackagesAvailableError: packagesError,
		AgentAcceptsPackages: agent.Status != nil &&
			agent.Status.Capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsPackages) != 0,
	})
}

func sendCustomMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	capability := r.PostForm.Get("capability")
	msgType := r.PostForm.Get("type")
	dataStr := r.PostForm.Get("data")

	customMsg := &protobufs.ServerToAgent{
		CustomMessage: &protobufs.CustomMessage{
			Capability: capability,
			Type:       msgType,
			Data:       []byte(dataStr),
		},
	}

	data.AllAgents.SendCustomMessageToAgent(instanceId, customMsg)

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func sendPackagesAvailable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	packagesJSON := r.PostForm.Get("packages_available")
	var packages protobufs.PackagesAvailable
	var errOffer error
	if r.PostForm.Get("source") == "disk" {
		packagesJSON = examplePackagesAvailableJSON
		errOffer = loadPackageManifest(packageManifest, &packages)
	} else {
		errOffer = protojson.Unmarshal([]byte(packagesJSON), &packages)
	}
	if errOffer != nil {
		w.WriteHeader(http.StatusBadRequest)
		renderAgentPage(w, agent, packagesJSON, fmt.Sprintf("Could not load package offer: %v", errOffer))
		return
	}
	if err := validatePackagesAvailable(&packages); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		renderAgentPage(w, agent, packagesJSON, err.Error())
		return
	}

	if err := data.AllAgents.SendPackagesAvailableToAgent(r.Context(), instanceId, &packages); err != nil {
		w.WriteHeader(http.StatusConflict)
		renderAgentPage(w, agent, packagesJSON, fmt.Sprintf("Could not send package offer: %v", err))
		return
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func validatePackagesAvailable(packages *protobufs.PackagesAvailable) error {
	if len(packages.AllPackagesHash) == 0 {
		return fmt.Errorf("allPackagesHash must contain the aggregate hash as Base64")
	}
	for name, pkg := range packages.Packages {
		if pkg == nil {
			return fmt.Errorf("package %q needs a definition", name)
		}
		if name == "" && pkg.Type != protobufs.PackageType_PackageType_TopLevel {
			return fmt.Errorf("the empty package name is reserved for a top-level package")
		}
		if pkg.Version == "" {
			return fmt.Errorf("package %q must have a version", name)
		}
		if pkg.Type != protobufs.PackageType_PackageType_TopLevel && pkg.Type != protobufs.PackageType_PackageType_Addon {
			return fmt.Errorf("package %q must use PackageType_TopLevel or PackageType_Addon", name)
		}
		if pkg.File == nil {
			return fmt.Errorf("package %q must define file", name)
		}
		fileURL, err := url.ParseRequestURI(pkg.File.DownloadUrl)
		if err != nil || fileURL.Host == "" || (fileURL.Scheme != "http" && fileURL.Scheme != "https") {
			return fmt.Errorf("package %q file.downloadUrl must be an absolute HTTP or HTTPS URL", name)
		}
		if len(pkg.File.ContentHash) != sha256.Size {
			return fmt.Errorf("package %q file.contentHash must be a Base64-encoded SHA-256 hash (32 bytes)", name)
		}
		if len(pkg.Hash) == 0 {
			return fmt.Errorf("package %q hash must be a non-empty Base64-encoded value", name)
		}
	}
	return nil
}

func saveCustomConfigForInstance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	configStr := r.PostForm.Get("config")
	config := &protobufs.AgentConfigMap{
		ConfigMap: map[string]*protobufs.AgentConfigObject{
			"": {Body: []byte(configStr)},
		},
	}

	notifyNextStatusUpdate := make(chan struct{}, 1)
	data.AllAgents.SetCustomConfigForAgent(instanceId, config, notifyNextStatusUpdate)

	// Wait for up to 5 seconds for a Status update, which is expected
	// to be reported by the Agent after we set the remote config.
	timer := time.NewTicker(time.Second * 5)

	select {
	case <-notifyNextStatusUpdate:
	case <-timer.C:
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func rotateInstanceClientCert(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Find the agent instance.
	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Create a new certificate for the agent.
	certificate, err := certs.CreateTLSCert(certs.CaCert, certs.CaKey)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Println(err)
		return
	}

	// Create an offer for the agent.
	offers := &protobufs.ConnectionSettingsOffers{
		Opamp: &protobufs.OpAMPConnectionSettings{
			Certificate: certificate,
		},
	}

	// Send the offer to the agent.
	data.AllAgents.OfferAgentConnectionSettings(instanceId, offers)

	logger.Printf("Waiting for agent %x to reconnect\n", instanceId)

	// Wait for up to 5 seconds for a Status update, which is expected
	// to be reported by the agent after we set the remote config.
	timer := time.NewTicker(time.Second * 5)

	// TODO: wait for agent to reconnect instead of waiting full 5 seconds.

	select {
	case <-timer.C:
		logger.Printf("Time out waiting for agent %x to reconnect\n", instanceId)
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func opampConnectionSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Find the agent instance.
	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// parse tls_min
	tlsMinVal := r.Form.Get("tls_min")
	var tlsMin string
	switch tlsMinVal {
	case "TLSv1.0":
		tlsMin = "1.0"
	case "TLSv1.1":
		tlsMin = "1.1"
	case "TLSv1.2":
		tlsMin = "1.2"
	case "TLSv1.3":
		tlsMin = "1.3"
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	offers := &protobufs.ConnectionSettingsOffers{
		Opamp: &protobufs.OpAMPConnectionSettings{
			Tls: &protobufs.TLSConnectionSettings{
				CaPemContents: string(certs.CaCert),
				MinVersion:    tlsMin,
				MaxVersion:    "1.3",
			},
		},
	}

	rawProxyURL := r.Form.Get("proxy_url")
	if len(rawProxyURL) > 0 {
		proxyURL, err := url.Parse(rawProxyURL)
		if err != nil {
			logger.Printf("Unable to parse %q as URL: %v", rawProxyURL, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		offers.Opamp.Proxy = &protobufs.ProxyConnectionSettings{
			Url: proxyURL.String(),
		}
	}

	data.AllAgents.OfferAgentConnectionSettings(instanceId, offers)

	logger.Printf("Waiting for agent %x to reconnect\n", instanceId)

	// Wait for up to 5 seconds for a Status update, which is expected
	// to be reported by the agent after we set the remote config.
	timer := time.NewTicker(time.Second * 5)

	// TODO: wait for agent to reconnect instead of waiting full 5 seconds.

	select {
	case <-timer.C:
		logger.Printf("Time out waiting for agent %x to reconnect\n", instanceId)
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}
