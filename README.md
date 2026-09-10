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
   `NodeGetInfo` reports the node's hostname as its CSI `NodeID`, and
   `ControllerPublishVolume` uses that ID directly as the Incus instance
   name to attach to.
2. **Block-content-type volumes require the node to be an Incus VM**, not a
   container (an Incus/qemu restriction, not something this driver can work
   around). Use `contentType: filesystem` if your nodes are containers.
3. The controller needs network access to the Incus API and a client
   certificate authorized on the target project — see `deploy/controller.yaml`
   and the `INCUS_ADDR`/`INCUS_CLIENT_CERT`/`INCUS_CLIENT_KEY`/
   `INCUS_SERVER_CERT`/`INCUS_INSECURE_SKIP_VERIFY` env vars (or matching
   `-incus-*` flags) read by `incusvol.Connect`. The node plugin needs none
   of this — it only formats/mounts a device that's already attached.

Apply in order: `deploy/csidriver.yaml`, `deploy/rbac.yaml`, a
`incus-credentials` Secret (see `deploy/controller.yaml`'s header comment),
then `deploy/controller.yaml` and `deploy/node.yaml`. A sample
StorageClass/VolumeSnapshotClass pair is in
`deploy/storageclass-example.yaml`.

Expanding a PVC resizes the Incus volume (`ControllerExpandVolume`); for
block volumes with a guest filesystem, the node plugin then runs
`resize2fs`/`xfs_growfs` on it (`NodeExpandVolume`). Incus doesn't allow
shrinking block volumes, so a shrink request is rejected. Filesystem-content-
type volumes are Incus-managed shares, so only the controller-side resize
runs for those.

Not implemented (kept out on purpose — add if you need it): raw volume
cloning without a snapshot, topology-aware scheduling.

## Building

```
go build ./...
docker build -t csi-incus .
```

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
