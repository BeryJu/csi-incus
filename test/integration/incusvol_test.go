//go:build integration

// Package integration exercises internal/incusvol against a real Incus
// server. It needs INCUS_TEST_POOL set to a pool the caller can create
// custom volumes in; everything else follows incusvol.Connect's usual env
// vars (INCUS_ADDR, INCUS_PROJECT, INCUS_CLIENT_CERT, INCUS_CLIENT_KEY, INCUS_SERVER_CERT,
// INCUS_INSECURE_SKIP_VERIFY), defaulting to the local unix socket.
//
// Run with: go test -tags integration ./test/integration/...
//
// Set INCUS_TEST_INSTANCE to an existing VM's name to also cover
// attach/detach (block volumes can only be attached to VMs).
package integration

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	incus "github.com/lxc/incus/v7/client"

	"beryju.io/csi-incus/internal/incusvol"
)

func testClient(t *testing.T) incus.InstanceServer {
	t.Helper()

	c, err := incusvol.Connect(incusvol.ConnConfig{
		Addr:            os.Getenv("INCUS_ADDR"),
		Project:         os.Getenv("INCUS_PROJECT"),
		TLSClientCert:   os.Getenv("INCUS_CLIENT_CERT"),
		TLSClientKey:    os.Getenv("INCUS_CLIENT_KEY"),
		TLSServerCert:   os.Getenv("INCUS_SERVER_CERT"),
		InsecureSkipTLS: os.Getenv("INCUS_INSECURE_SKIP_VERIFY") == "true",
	})
	if err != nil {
		t.Fatalf("connect to incus: %v", err)
	}

	return c
}

func randName(t *testing.T) string {
	t.Helper()

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}

	return "csi-it-" + hex.EncodeToString(b)
}

func TestVolumeSnapshotLifecycle(t *testing.T) {
	pool := os.Getenv("INCUS_TEST_POOL")
	if pool == "" {
		t.Skip("INCUS_TEST_POOL not set")
	}

	c := testClient(t)
	name := randName(t)
	const size = 1 << 30 // 1GiB

	if err := incusvol.CreateVolume(c, pool, name, "block", size, "", "", ""); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	t.Cleanup(func() {
		if err := incusvol.DeleteVolume(c, pool, name); err != nil {
			t.Errorf("cleanup DeleteVolume: %v", err)
		}
	})

	// CreateVolume must be idempotent.
	if err := incusvol.CreateVolume(c, pool, name, "block", size, "", "", ""); err != nil {
		t.Fatalf("CreateVolume (repeat): %v", err)
	}

	gotSize, gotContentType, err := incusvol.GetVolume(c, pool, name)
	if err != nil {
		t.Fatalf("GetVolume: %v", err)
	}
	if gotContentType != "block" {
		t.Errorf("content type = %q, want block", gotContentType)
	}
	if gotSize != size {
		t.Errorf("size = %d, want %d", gotSize, size)
	}

	const snap = "snap1"
	if err := incusvol.CreateSnapshot(c, pool, name, snap); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := incusvol.CreateSnapshot(c, pool, name, snap); err != nil {
		t.Fatalf("CreateSnapshot (repeat): %v", err)
	}

	clone := randName(t)
	if err := incusvol.CreateVolume(c, pool, clone, "block", size, pool, name, snap); err != nil {
		t.Fatalf("CreateVolume from snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := incusvol.DeleteVolume(c, pool, clone); err != nil {
			t.Errorf("cleanup DeleteVolume(clone): %v", err)
		}
	})

	if err := incusvol.DeleteSnapshot(c, pool, name, snap); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if err := incusvol.DeleteSnapshot(c, pool, name, snap); err != nil {
		t.Fatalf("DeleteSnapshot (repeat, already gone): %v", err)
	}
}

func TestResizeAndInventory(t *testing.T) {
	pool := os.Getenv("INCUS_TEST_POOL")
	if pool == "" {
		t.Skip("INCUS_TEST_POOL not set")
	}

	c := testClient(t)
	name := randName(t)
	const initial = 1 << 30 // 1GiB
	const grown = 2 << 30   // 2GiB

	if err := incusvol.CreateVolume(c, pool, name, "block", initial, "", "", ""); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	t.Cleanup(func() {
		if err := incusvol.DeleteVolume(c, pool, name); err != nil {
			t.Errorf("cleanup DeleteVolume: %v", err)
		}
	})

	if err := incusvol.ResizeVolume(c, pool, name, grown); err != nil {
		t.Fatalf("ResizeVolume: %v", err)
	}

	if gotSize, _, err := incusvol.GetVolume(c, pool, name); err != nil {
		t.Fatalf("GetVolume: %v", err)
	} else if gotSize != grown {
		t.Errorf("size after resize = %d, want %d", gotSize, grown)
	}

	vols, err := incusvol.ListVolumes(c)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	found := false
	for _, v := range vols {
		if v.Pool == pool && v.Name == name {
			found = true
			if v.SizeBytes != grown {
				t.Errorf("ListVolumes size = %d, want %d", v.SizeBytes, grown)
			}
		}
	}
	if !found {
		t.Errorf("ListVolumes did not include %s/%s", pool, name)
	}

	available, total, err := incusvol.PoolCapacity(c, pool)
	if err != nil {
		t.Fatalf("PoolCapacity: %v", err)
	}
	if total <= 0 || available > total {
		t.Errorf("PoolCapacity = available=%d total=%d, want 0 < available <= total", available, total)
	}
}

func TestAttachDetachInstance(t *testing.T) {
	pool := os.Getenv("INCUS_TEST_POOL")
	instance := os.Getenv("INCUS_TEST_INSTANCE")
	if pool == "" || instance == "" {
		t.Skip("INCUS_TEST_POOL and INCUS_TEST_INSTANCE must both be set")
	}

	c := testClient(t)
	name := randName(t)
	const dev = "csi-it-dev"

	if err := incusvol.CreateVolume(c, pool, name, "block", 1<<30, "", "", ""); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	t.Cleanup(func() {
		if err := incusvol.DetachFromInstance(c, instance, dev); err != nil {
			t.Errorf("cleanup DetachFromInstance: %v", err)
		}
		if err := incusvol.DeleteVolume(c, pool, name); err != nil {
			t.Errorf("cleanup DeleteVolume: %v", err)
		}
	})

	if err := incusvol.AttachToInstance(c, instance, dev, pool, name, ""); err != nil {
		t.Fatalf("AttachToInstance: %v", err)
	}
	if err := incusvol.AttachToInstance(c, instance, dev, pool, name, ""); err != nil {
		t.Fatalf("AttachToInstance (repeat): %v", err)
	}

	inst, _, err := c.GetInstance(instance)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got := inst.Devices[dev]; got["pool"] != pool || got["source"] != name {
		t.Errorf("instance device %q = %v, want pool=%s source=%s", dev, got, pool, name)
	}

	if err := incusvol.DetachFromInstance(c, instance, dev); err != nil {
		t.Fatalf("DetachFromInstance: %v", err)
	}
	if err := incusvol.DetachFromInstance(c, instance, dev); err != nil {
		t.Fatalf("DetachFromInstance (repeat, already gone): %v", err)
	}

	inst, _, err = c.GetInstance(instance)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if _, ok := inst.Devices[dev]; ok {
		t.Errorf("device %q still present after detach", dev)
	}
}
