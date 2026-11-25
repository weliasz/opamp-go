# OpAMP protocol implementation in Go

---

<p align="center">
  <strong>
    <a href="https://cloud-native.slack.com/archives/C02J58HR58R">Getting In Touch</a>
  </strong>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/open-telemetry/opamp-go">
    <img alt="Go Docs" height="28" src="https://godoc.org/github.com/open-telemetry/opamp-go?status.svg"></a>
  <a href="https://github.com/open-telemetry/opamp-go/actions/workflows/build-and-test.yml?query=branch%3Amain">
    <img alt="Build Status" src="https://img.shields.io/github/actions/workflow/status/open-telemetry/opamp-go/build-and-test.yml?branch%3Amain&style=for-the-badge"></a>
  <a href="https://codecov.io/gh/open-telemetry/opamp-go/branch/main/">
    <img alt="Codecov Status" src="https://img.shields.io/codecov/c/github/open-telemetry/opamp-go?style=for-the-badge"></a>
  <a href="https://github.com/open-telemetry/opamp-go/releases">
    <img alt="GitHub release (latest by date including pre-releases)" src="https://img.shields.io/github/v/release/open-telemetry/opamp-go?include_prereleases&style=for-the-badge"></a>
</p>

---

[Open Agent Management Protocol (OpAMP)](https://github.com/open-telemetry/opamp-spec)
is a network protocol for remote management of large fleets of data collection Agents.

OpAMP allows Agents to report their status to and receive configuration from a
Server and to receive agent package updates from the server.
The protocol is vendor-agnostic, so the Server can remotely monitor and
manage a fleet of different Agents that implement OpAMP, including a fleet of
mixed agents from different vendors.

This repository is work-in-progress of an OpAMP implementation in Go.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

### Maintainers

- [Andy Keller](https://github.com/andykellr), Bindplane
- [Tigran Najaryan](https://github.com/tigrannajaryan), Splunk

For more information about the maintainer role, see the [community repository](https://github.com/open-telemetry/community/blob/main/guides/contributor/membership.md#maintainer).

### Approvers

- [Evan Bradley](https://github.com/evan-bradley), Dynatrace
- [Michel Laterman](https://github.com/michel-laterman), Elastic

For more information about the approver role, see the [community repository](https://github.com/open-telemetry/community/blob/main/guides/contributor/membership.md#approver).

### Emeritus Approvers

- [Alex Boten](https://github.com/codeboten)
- [Anthony Mirabella](https://github.com/Aneurysm9)
- [Przemek Maciolek](https://github.com/pmm-sumo)
- [Srikanth Chekuri](https://github.com/srikanthccv)

For more information about the emeritus role, see the [community repository](https://github.com/open-telemetry/community/blob/main/guides/contributor/membership.md#emeritus-maintainerapprovertriager).

### Thanks to all of our contributors!

<a href="https://github.com/open-telemetry/opamp-go/graphs/contributors">
  <img alt="Repo contributors" src="https://contrib.rocks/image?repo=open-telemetry/opamp-go" />
</a>

### Test package delivery with the example server

Create a package directory on the example server host and put the artifact and
this manifest in it. Use a download URL as seen by the agent. The server reads
the manifest at each click, hashes the artifact from disk, and generates the
package and assignment hashes.

```json
{
  "packages": {
    "file:smoke": {
      "type": "PackageType_Addon",
      "version": "1",
      "file": "smoke.txt",
      "downloadUrl": "http://127.0.0.1:8000/smoke.txt"
    }
  }
}
```

Save it as `packages.json`. For a single Collector binary update, use the
empty key `""` with `"type": "PackageType_TopLevel"`. An entry may include a
Base64 `signature` when the agent verifies package signatures. Each manifest
is the complete assignment. To remove an addon, omit it from the next
assignment or use `{"packages": {}}` to remove all assigned addons.

Start the example server on the lab network:

```sh
cd internal/examples
go run ./server -no-tls -package-manifest /absolute/path/to/packages/packages.json
```

Connect the agent over WebSocket at `ws://SERVER:4320/v1/opamp`. Open the
example server's admin UI on port 4321, select the agent, and click **Offer
packages from disk**. After the agent handles the offer, use **Refresh agent
status** to view its latest **Package Statuses**.

You can also prepare an offer on the agent page using the package table.
Enter the name, version, and download URL for each package, then click
**Done**. The server downloads the artifacts, calculates the content, package,
and assignment hashes, and fills the **PackagesAvailable JSON** field.
Review the complete assignment and click **Send PackagesAvailable** to send it.
The table accepts URLs on `http://127.0.0.1:8000` by default. To use another
artifact endpoint, start the server with `-package-download-origin` followed
by its scheme, host, and port if needed. The URL must work from both the
OpAMP server and the agent.

For temporary HTTP hosting, serve only the intended artifact directory.
Keep the manifest outside the served subtree if it contains private URLs
or metadata. For example, place `smoke.txt` under `artifacts/` next to
`packages.json` and change the manifest's `file` to `artifacts/smoke.txt`.

```sh
python3 -m http.server 8000 --bind 127.0.0.1 --directory /absolute/path/to/packages/artifacts
```

For the Splunk Supervisor, which permits plain HTTP package downloads on
loopback, run this SSH tunnel on the agent host and keep it open while testing:

```sh
ssh -N -L 127.0.0.1:8000:127.0.0.1:8000 user@SERVER
```

Use the loopback download URL shown in the manifest. For direct downloads
between hosts, serve the artifacts over HTTPS with a certificate the agent
trusts and put that HTTPS URL in the manifest.
