package driver

import "testing"

func TestVolumeIDRoundTrip(t *testing.T) {
	pool, name, err := decodeVolumeID(encodeVolumeID("default", "pvc-123"))
	if err != nil {
		t.Fatalf("decodeVolumeID: %v", err)
	}
	if pool != "default" || name != "pvc-123" {
		t.Errorf("got pool=%q name=%q, want pool=default name=pvc-123", pool, name)
	}
}

func TestDecodeVolumeIDMalformed(t *testing.T) {
	for _, id := range []string{"", "novalues", "/missingpool", "pool/"} {
		if _, _, err := decodeVolumeID(id); err == nil {
			t.Errorf("decodeVolumeID(%q): want error, got nil", id)
		}
	}
}

func TestSnapshotIDRoundTrip(t *testing.T) {
	pool, vol, snap, err := decodeSnapshotID(encodeSnapshotID("default", "pvc-123", "snap-1"))
	if err != nil {
		t.Fatalf("decodeSnapshotID: %v", err)
	}
	if pool != "default" || vol != "pvc-123" || snap != "snap-1" {
		t.Errorf("got pool=%q vol=%q snap=%q, want default/pvc-123/snap-1", pool, vol, snap)
	}
}

func TestDecodeSnapshotIDMalformed(t *testing.T) {
	for _, id := range []string{"", "pool", "pool/vol", "pool//snap", "/vol/snap"} {
		if _, _, _, err := decodeSnapshotID(id); err == nil {
			t.Errorf("decodeSnapshotID(%q): want error, got nil", id)
		}
	}
}

func TestDeviceName(t *testing.T) {
	// Must stay well under the 14-char limit Incus imposes on virtio-blk/
	// nvme serials before it starts re-hashing the name itself (see the
	// comment on deviceName) — otherwise waitForDevice's by-id lookup
	// would be searching for the wrong serial.
	const maxSerialLen = 14

	dev := deviceName("default/pvc-123")
	if len(dev) > maxSerialLen {
		t.Errorf("deviceName length = %d, want <= %d", len(dev), maxSerialLen)
	}

	if got := deviceName("default/pvc-123"); got != dev {
		t.Errorf("deviceName not deterministic: %q != %q", got, dev)
	}

	if deviceName("default/pvc-123") == deviceName("default/pvc-456") {
		t.Error("deviceName collided for two different volume IDs")
	}
}
