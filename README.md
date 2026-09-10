# csi-incus

A Kubernetes CSI driver for [Incus](https://linuxcontainers.org/incus/). It
provisions Incus custom storage volumes, snapshots them, and attaches them
as disk devices to the Incus instance backing each Kubernetes node — using
the official `github.com/lxc/incus/v7/client` Go SDK throughout, no
hand-rolled HTTP calls to the Incus API.

## How it maps to Incus

- **StorageClass `pool` parameter** → the Incus storage pool the volume is
  created in (required).
- **StorageClass `contentType` parameter** → `block` (default) or
  `filesystem`, matching Incus custom volume content types.
  - `block` volumes are hotplugged as a raw virtio disk. **Incus only
    allows block-content-type volumes to attach to virtual machines**, and
    never to more than one instance at a time — so the driver only accepts
    `SINGLE_NODE_*` access modes for these.
  - `filesystem` volumes are attached with a `path=` device option, so
    Incus mounts them itself (via virtiofs/9p for VMs, bind-mount for
    containers) — these work on containers too and can be attached to
    several instances at once.
- **VolumeSnapshot** → an Incus custom volume snapshot
  (`CreateStoragePoolVolumeSnapshot`/`DeleteStoragePoolVolumeSnapshot`).
  Creating a PVC from a VolumeSnapshot clones a new volume from it via the
  volume-copy API (`Source.Type: "copy"`).
- **ControllerPublishVolume/ControllerUnpublishVolume** → add/remove a disk
  device on the target instance (`UpdateInstance`), keyed by a short
  deterministic name derived from the volume ID. The device name doubles
  as the qemu disk serial, which is how the node plugin finds the device
  (`/dev/disk/by-id/*<name>*`) without needing any Incus API access itself.

## Deployment prerequisites

1. **A Kubernetes Node's name must equal its Incus instance's name.**
   `NodeGetInfo` reports the Kubernetes Node name (from the `NODE_NAME` env
   var — a pod's own hostname is its pod name, not the Node's, so the
   DaemonSet sets it from the downward API's `spec.nodeName`) as its CSI
   `NodeID`, and `ControllerPublishVolume` uses that ID directly as the
   Incus instance name to attach to.
2. **Block-content-type volumes require the node to be an Incus VM**, not a
   container (an Incus/qemu restriction, not something this driver can work
   around). Use `contentType: filesystem` if your nodes are containers.
3. The controller needs network access to the Incus API and a client
   certificate authorized on the target project — the
   `INCUS_ADDR`/`INCUS_CLIENT_CERT`/`INCUS_CLIENT_KEY`/`INCUS_SERVER_CERT`/
   `INCUS_INSECURE_SKIP_VERIFY` env vars (or matching `-incus-*` flags) read
   by `incusvol.Connect`. The node plugin needs none of this — it only
   formats/mounts a device that's already attached.

## Installing

```sh
helm install csi-incus oci://ghcr.io/beryju/charts/csi-incus \
  --namespace kube-system \
  --set incus.addr=https://your-incus-host:8443
```

See [`charts/csi-incus/README.md`](charts/csi-incus/README.md) for the full
values reference — StorageClasses, the Incus TLS client cert/key (the chart
generates and persists a self-signed one if you don't supply your own),
Prometheus `ServiceMonitor`s, sidecar image overrides, etc. The chart's
post-install NOTES walk through registering that client certificate with
Incus, which Helm can't do on your behalf (it's a call against your Incus
server, not the Kubernetes API).

Expanding a PVC resizes the Incus volume (`ControllerExpandVolume`); for
block volumes with a guest filesystem, the node plugin then runs
`resize2fs`/`xfs_growfs` on it (`NodeExpandVolume`). Incus doesn't allow
shrinking block volumes, so a shrink request is rejected. Filesystem-content-
type volumes are Incus-managed shares, so only the controller-side resize
runs for those.

Not implemented (kept out on purpose — add if you need it): raw volume
cloning without a snapshot, topology-aware scheduling.

## The controller's Incus client certificate

The controller authenticates to Incus as a TLS client, like any other Incus
client (`incus remote add`, Terraform, etc.) — there's nothing
driver-specific here. The Helm chart generates a self-signed client
keypair automatically if you don't set `incus.tls.clientCert`/
`incus.tls.clientKey` yourself (and keeps reusing the same one across
`helm upgrade`s). Either way, two things stay manual because they're calls
against your Incus server, not the Kubernetes API:

1. Register the client certificate — the chart's post-install NOTES print
   the exact commands, in short:

   ```sh
   kubectl get secret -n kube-system csi-incus-incus-tls -o jsonpath='{.data.client-cert}' | base64 -d > client.crt
   scp client.crt incus-server:/tmp/csi-incus.crt
   ssh incus-server incus config trust add-certificate /tmp/csi-incus.crt --name csi-incus
   ```

2. Fetch the server's certificate, if you want to pin it (`incus.tls.serverCert`)
   instead of trusting the system CA pool or setting `incus.insecureSkipVerify`:

   ```sh
   scp incus-server:/var/lib/incus/server.crt server.crt
   ```

## Building

```
go build ./...
docker build -t csi-incus .
```

CI's `docker` job publishes the image to `ghcr.io/beryju/csi-incus` on every
push to `main` (tagged `latest`, the branch name, and the commit SHA) using
`docker/build-push-action`; pull requests build the same image to catch a
broken Dockerfile but never push it.

## Releasing

Pushing a `v*` tag (e.g. `v1.2.3`) additionally tags that same image build
with the version (`docker/metadata-action`'s `type=semver` tags) and runs
the `helm` job: it packages `charts/csi-incus` with the chart and app
version both set to the tag, and pushes it with `helm push` to
`oci://ghcr.io/beryju/charts/csi-incus`.

## Testing

`go test ./...` runs the (non-integration) unit build. The real coverage is
the integration suite in `test/integration`, which drives
`internal/incusvol` against an actual Incus server — nothing is mocked:

```
INCUS_TEST_POOL=<pool> \
INCUS_TEST_INSTANCE=<optional vm name, to also cover attach/detach> \
INCUS_ADDR=... INCUS_CLIENT_CERT=... INCUS_CLIENT_KEY=... \
  go test -tags integration ./test/integration/... -v
```

With no `INCUS_ADDR` etc. set, it connects over the local unix socket. Each
test creates its own randomly-named scratch volume(s) and cleans up via
`t.Cleanup`, regardless of pass/fail.

CI (`.github/workflows/ci.yml`) runs this same suite on every push/PR: it
installs Incus on the runner, `incus admin init --minimal`s the default
setup, then creates a loop-backed `zfs` pool for the tests to use (the
`dir` driver `--minimal` defaults to doesn't really support snapshots —
custom-volume snapshots come back read-only and fail to delete), boots a
real Incus VM (using the GitHub-hosted runner's KVM support) to cover
attach/detach, then runs `go test -tags integration` against it —
alongside separate lint, unit-test, and build jobs.
