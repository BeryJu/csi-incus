# csi-incus

Helm chart for the [Incus](https://linuxcontainers.org/incus/) CSI driver —
see the [repo README](https://github.com/BeryJu/csi-incus) for how it maps
onto Incus and the deployment prerequisites (Node name == Incus instance
name, block volumes need VM nodes, etc).

## Installing

```sh
helm install csi-incus oci://ghcr.io/beryju/charts/csi-incus \
  --namespace kube-system \
  --set incus.addr=https://your-incus-host:8443
```

Then follow the steps printed in the post-install NOTES to register the
controller's TLS client certificate with Incus.

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/beryju/csi-incus` | Driver image |
| `image.tag` | `""` | Defaults to the chart's `appVersion` |
| `driverName` | `incus.csi.linuxcontainers.org` | CSI driver name, must match across the CSIDriver object and every StorageClass's `provisioner` |
| `incus.addr` | `""` | Incus API address (`https://host:8443`), or empty/`unix://path` for a local socket. Required. |
| `incus.project` | `""` | Incus project instances and volumes live in; empty means the default project |
| `incus.insecureSkipVerify` | `false` | Skip TLS verification of the Incus server |
| `incus.tls.clientCert` / `incus.tls.clientKey` | `""` | PEM client cert/key; auto-generated (and kept across upgrades) if left empty |
| `incus.tls.serverCert` | `""` | PEM cert to pin the Incus server to; omit to trust the system CA pool |
| `controller.replicaCount` | `1` | Controller Deployment replica count |
| `controller.sidecars.*.image`/`tag` | upstream `registry.k8s.io/sig-storage/*` | `provisioner`, `attacher`, `snapshotter`, `resizer` sidecar images |
| `node.sidecars.nodeDriverRegistrar.image`/`tag` | `registry.k8s.io/sig-storage/csi-node-driver-registrar` | Node registrar sidecar image |
| `node.tolerations` | tolerate everything | The node plugin must run on every node, including tainted ones |
| `rbac.create` | `true` | Create the ServiceAccounts/ClusterRoles/ClusterRoleBindings |
| `storageClasses` | one `incus-block` class on pool `default` | List of StorageClasses to create; each needs at least `name` and `pool` |
| `volumeSnapshotClass.create` | `true` | Create a matching VolumeSnapshotClass |
| `metrics.serviceMonitor.enabled` | `false` | Expose and scrape the controller sidecars' `/metrics` (needs the Prometheus Operator CRDs) |

## Metrics

Setting `metrics.serviceMonitor.enabled=true` adds `--metrics-address` to
the `csi-provisioner`/`csi-attacher`/`csi-snapshotter`/`csi-resizer`
sidecars, a Service exposing their four metrics ports, and a
`ServiceMonitor` (only rendered if the `monitoring.coreos.com/v1` CRDs are
present in the cluster). The driver's own gRPC handlers aren't
instrumented — this only covers the standard sidecars.
