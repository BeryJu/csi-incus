package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Volume and snapshot IDs are just "pool/name" and "pool/volume/snapshot" —
// Incus is the only source of truth, so there's nothing else to encode.

func encodeVolumeID(pool, name string) string {
	return pool + "/" + name
}

func decodeVolumeID(id string) (pool, name string, err error) {
	pool, name, ok := strings.Cut(id, "/")
	if !ok || pool == "" || name == "" {
		return "", "", fmt.Errorf("malformed volume id %q", id)
	}

	return pool, name, nil
}

func encodeSnapshotID(pool, volume, snapshot string) string {
	return pool + "/" + volume + "/" + snapshot
}

func decodeSnapshotID(id string) (pool, volume, snapshot string, err error) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("malformed snapshot id %q", id)
	}

	return parts[0], parts[1], parts[2], nil
}

// deviceName derives the Incus disk device name (and therefore its qemu
// disk serial) from a volume ID. Two constraints, both learned the hard
// way:
//   - It must stay <=14 chars: Incus hashes the device name into a
//     shorter serial past that length for virtio-blk/nvme buses.
//   - It must not contain "-": Incus's actual qemu serial is
//     "incus_"+deviceName with every "-" doubled (e.g. "csi-ab" becomes
//     "incus_csi--ab"), which broke NodeStageVolume's by-id substring
//     lookup below since the literal device name no longer appears
//     verbatim in the resulting serial.
func deviceName(volumeID string) string {
	sum := sha256.Sum256([]byte(volumeID))
	return "csi" + hex.EncodeToString(sum[:])[:9]
}
