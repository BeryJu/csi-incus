// Package incusvol wraps the operations the driver needs from the Incus
// Go client SDK: create/delete custom block volumes, snapshot them, and
// attach/detach them as disk devices on instances (Incus API only, no
// hand-rolled HTTP).
package incusvol

import (
	"fmt"
	"os"
	"sort"
	"strings"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// ConnConfig configures how to reach the Incus server.
//
// Addr selects the transport: empty or "unix://<path>" connects over the
// local unix socket (path "" uses the client library's default socket
// path); anything else is passed to ConnectIncus as an HTTPS URL.
type ConnConfig struct {
	Addr            string
	TLSClientCert   string // PEM, or a path to a PEM file
	TLSClientKey    string
	TLSServerCert   string
	InsecureSkipTLS bool
}

// Connect opens an InstanceServer connection per cfg.
func Connect(cfg ConnConfig) (incus.InstanceServer, error) {
	if cfg.Addr == "" || strings.HasPrefix(cfg.Addr, "unix://") {
		return incus.ConnectIncusUnix(strings.TrimPrefix(cfg.Addr, "unix://"), nil)
	}

	args := &incus.ConnectionArgs{InsecureSkipVerify: cfg.InsecureSkipTLS}

	var err error
	args.TLSClientCert, err = readPEM(cfg.TLSClientCert)
	if err != nil {
		return nil, fmt.Errorf("client cert: %w", err)
	}
	args.TLSClientKey, err = readPEM(cfg.TLSClientKey)
	if err != nil {
		return nil, fmt.Errorf("client key: %w", err)
	}
	args.TLSServerCert, err = readPEM(cfg.TLSServerCert)
	if err != nil {
		return nil, fmt.Errorf("server cert: %w", err)
	}

	return incus.ConnectIncus(cfg.Addr, args)
}

// readPEM returns s unchanged if it already looks like PEM, otherwise
// treats it as a file path and reads it. Empty stays empty.
func readPEM(s string) (string, error) {
	if s == "" || strings.HasPrefix(s, "-----BEGIN") {
		return s, nil
	}

	b, err := os.ReadFile(s)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

const volType = "custom"

// CreateVolume creates a custom volume of sizeBytes and contentType
// ("block" or "filesystem") in pool. If sourcePool/sourceVolume/
// sourceSnapshot are set, the volume is created as a copy of that snapshot
// instead of empty. Idempotent: an already-existing volume with the same
// name is left as-is.
func CreateVolume(s incus.InstanceServer, pool, name, contentType string, sizeBytes int64, sourcePool, sourceVolume, sourceSnapshot string) error {
	if _, _, err := s.GetStoragePoolVolume(pool, volType, name); err == nil {
		return nil // already exists
	}

	post := api.StorageVolumesPost{
		Name:        name,
		Type:        volType,
		ContentType: contentType,
		StorageVolumePut: api.StorageVolumePut{
			Config: map[string]string{"size": fmt.Sprintf("%d", sizeBytes)},
		},
	}

	if sourceVolume != "" {
		post.Source = api.StorageVolumeSource{
			Type: "copy",
			Pool: sourcePool,
			Name: sourceVolume + "/" + sourceSnapshot,
		}
	}

	return s.CreateStoragePoolVolume(pool, post)
}

// DeleteVolume deletes a custom volume. Idempotent: missing is not an error.
func DeleteVolume(s incus.InstanceServer, pool, name string) error {
	err := s.DeleteStoragePoolVolume(pool, volType, name)
	if isNotFound(err) {
		return nil
	}

	return err
}

// GetVolume returns a volume's configured size (0 if unset) and content type.
func GetVolume(s incus.InstanceServer, pool, name string) (sizeBytes int64, contentType string, err error) {
	vol, _, err := s.GetStoragePoolVolume(pool, volType, name)
	if err != nil {
		return 0, "", err
	}

	var sz int64
	fmt.Sscanf(vol.Config["size"], "%d", &sz)

	return sz, vol.ContentType, nil
}

// ResizeVolume grows (or shrinks, where the pool driver allows it — not
// block volumes, per Incus) a custom volume to newSizeBytes. Idempotent.
func ResizeVolume(s incus.InstanceServer, pool, name string, newSizeBytes int64) error {
	vol, etag, err := s.GetStoragePoolVolume(pool, volType, name)
	if err != nil {
		return err
	}

	put := vol.Writable()
	put.Config["size"] = fmt.Sprintf("%d", newSizeBytes)

	return s.UpdateStoragePoolVolume(pool, volType, name, put, etag)
}

// Volume describes one custom volume, as returned by ListVolumes.
type Volume struct {
	Pool        string
	Name        string
	SizeBytes   int64
	ContentType string
}

// ListVolumes returns every custom volume across every storage pool on the
// server, sorted by pool then name for stable pagination by the caller.
func ListVolumes(s incus.InstanceServer) ([]Volume, error) {
	pools, err := s.GetStoragePools()
	if err != nil {
		return nil, err
	}

	var out []Volume
	for _, pool := range pools {
		vols, err := s.GetStoragePoolVolumes(pool.Name)
		if err != nil {
			return nil, fmt.Errorf("list volumes in pool %q: %w", pool.Name, err)
		}

		for _, v := range vols {
			if v.Type != volType {
				continue
			}

			var sz int64
			fmt.Sscanf(v.Config["size"], "%d", &sz)

			out = append(out, Volume{Pool: pool.Name, Name: v.Name, SizeBytes: sz, ContentType: v.ContentType})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Pool != out[j].Pool {
			return out[i].Pool < out[j].Pool
		}

		return out[i].Name < out[j].Name
	})

	return out, nil
}

// PoolCapacity returns the available and total bytes of a storage pool.
func PoolCapacity(s incus.InstanceServer, pool string) (availableBytes, totalBytes int64, err error) {
	res, err := s.GetStoragePoolResources(pool)
	if err != nil {
		return 0, 0, err
	}

	total := int64(res.Space.Total)
	used := int64(res.Space.Used)

	return total - used, total, nil
}

// CreateSnapshot snapshots a custom volume. Idempotent.
func CreateSnapshot(s incus.InstanceServer, pool, volume, snapshot string) error {
	if _, _, err := s.GetStoragePoolVolumeSnapshot(pool, volType, volume, snapshot); err == nil {
		return nil
	}

	op, err := s.CreateStoragePoolVolumeSnapshot(pool, volType, volume, api.StorageVolumeSnapshotsPost{Name: snapshot})
	if err != nil {
		return err
	}

	return op.Wait()
}

// DeleteSnapshot deletes a volume snapshot. Idempotent.
func DeleteSnapshot(s incus.InstanceServer, pool, volume, snapshot string) error {
	op, err := s.DeleteStoragePoolVolumeSnapshot(pool, volType, volume, snapshot)
	if isNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	return op.Wait()
}

// AttachToInstance adds volume as a disk device (keyed by deviceName) on
// the named instance. guestPath is the in-instance mount path for a
// filesystem-content-type volume; leave it empty to attach a block-
// content-type volume as a raw block device instead. Idempotent as long
// as the existing device under deviceName already points at the same
// pool/volume.
func AttachToInstance(s incus.InstanceServer, instanceName, deviceName, pool, volume, guestPath string) error {
	inst, etag, err := s.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf("get instance %q: %w", instanceName, err)
	}

	put := inst.Writable()
	if put.Devices == nil {
		put.Devices = map[string]map[string]string{}
	}

	if dev, ok := put.Devices[deviceName]; ok {
		if dev["pool"] == pool && dev["source"] == volume && dev["type"] == "disk" {
			return nil // already attached
		}

		return fmt.Errorf("device %q on instance %q is already in use by a different volume", deviceName, instanceName)
	}

	dev := map[string]string{"type": "disk", "pool": pool, "source": volume}
	if guestPath != "" {
		dev["path"] = guestPath
	}
	put.Devices[deviceName] = dev

	op, err := s.UpdateInstance(instanceName, put, etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// DetachFromInstance removes the disk device keyed by deviceName from the
// named instance. Idempotent: a missing instance or device is not an error.
func DetachFromInstance(s incus.InstanceServer, instanceName, deviceName string) error {
	inst, etag, err := s.GetInstance(instanceName)
	if isNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("get instance %q: %w", instanceName, err)
	}

	put := inst.Writable()
	if _, ok := put.Devices[deviceName]; !ok {
		return nil
	}

	delete(put.Devices, deviceName)

	op, err := s.UpdateInstance(instanceName, put, etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
