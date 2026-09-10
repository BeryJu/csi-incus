package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These cover request validation that returns before ever touching Incus,
// so a ControllerServer with a nil Incus client is enough: calling into
// incusvol with a nil InstanceServer would panic, which would fail the
// test just as loudly as a wrong error code would.

func singleWriterCap() *csi.VolumeCapability {
	return &csi.VolumeCapability{AccessMode: &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	}}
}

func multiWriterCap() *csi.VolumeCapability {
	return &csi.VolumeCapability{AccessMode: &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	}}
}

func TestCreateVolumeValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *csi.CreateVolumeRequest
	}{
		{"missing name", &csi.CreateVolumeRequest{}},
		{"missing pool", &csi.CreateVolumeRequest{Name: "vol1"}},
		{"bad content type", &csi.CreateVolumeRequest{
			Name:       "vol1",
			Parameters: map[string]string{"pool": "default", "contentType": "bogus"},
		}},
		{"block volume with multi-node access mode", &csi.CreateVolumeRequest{
			Name:               "vol1",
			Parameters:         map[string]string{"pool": "default"},
			VolumeCapabilities: []*csi.VolumeCapability{multiWriterCap()},
		}},
	}

	s := &ControllerServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.CreateVolume(context.Background(), tt.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("CreateVolume() err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestControllerPublishVolumeValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *csi.ControllerPublishVolumeRequest
	}{
		{"malformed volume id", &csi.ControllerPublishVolumeRequest{VolumeId: "novalues", NodeId: "n1", VolumeCapability: singleWriterCap()}},
		{"missing node id", &csi.ControllerPublishVolumeRequest{VolumeId: "default/vol1", VolumeCapability: singleWriterCap()}},
		{"block volume with multi-node access mode", &csi.ControllerPublishVolumeRequest{
			VolumeId: "default/vol1", NodeId: "n1", VolumeCapability: multiWriterCap(),
		}},
	}

	s := &ControllerServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ControllerPublishVolume(context.Background(), tt.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("ControllerPublishVolume() err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestControllerUnpublishVolumeValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *csi.ControllerUnpublishVolumeRequest
	}{
		{"malformed volume id", &csi.ControllerUnpublishVolumeRequest{VolumeId: "novalues", NodeId: "n1"}},
		{"missing node id", &csi.ControllerUnpublishVolumeRequest{VolumeId: "default/vol1"}},
	}

	s := &ControllerServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ControllerUnpublishVolume(context.Background(), tt.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("ControllerUnpublishVolume() err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestCreateSnapshotValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *csi.CreateSnapshotRequest
	}{
		{"missing name", &csi.CreateSnapshotRequest{SourceVolumeId: "default/vol1"}},
		{"malformed source volume id", &csi.CreateSnapshotRequest{Name: "snap1", SourceVolumeId: "novalues"}},
	}

	s := &ControllerServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.CreateSnapshot(context.Background(), tt.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("CreateSnapshot() err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestDeleteSnapshotValidation(t *testing.T) {
	s := &ControllerServer{}
	if _, err := s.DeleteSnapshot(context.Background(), &csi.DeleteSnapshotRequest{SnapshotId: "novalues"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteSnapshot() err = %v, want InvalidArgument", err)
	}
}

func TestControllerExpandVolumeValidation(t *testing.T) {
	tests := []struct {
		name string
		req  *csi.ControllerExpandVolumeRequest
	}{
		{"malformed volume id", &csi.ControllerExpandVolumeRequest{VolumeId: "novalues", CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 30}}},
		{"missing capacity range", &csi.ControllerExpandVolumeRequest{VolumeId: "default/vol1"}},
	}

	s := &ControllerServer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ControllerExpandVolume(context.Background(), tt.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("ControllerExpandVolume() err = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestGetCapacityValidation(t *testing.T) {
	s := &ControllerServer{}
	if _, err := s.GetCapacity(context.Background(), &csi.GetCapacityRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetCapacity() err = %v, want InvalidArgument", err)
	}
}

func TestValidateVolumeCapabilitiesMalformedID(t *testing.T) {
	s := &ControllerServer{}
	req := &csi.ValidateVolumeCapabilitiesRequest{VolumeId: "novalues"}
	if _, err := s.ValidateVolumeCapabilities(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ValidateVolumeCapabilities() err = %v, want InvalidArgument", err)
	}
}

// fakeIncus implements just enough of incus.InstanceServer for
// TestListVolumesPagination; every other method panics if called, which is
// fine since ListVolumes never calls them.
type fakeIncus struct {
	incus.InstanceServer

	pools []api.StoragePool
	vols  map[string][]api.StorageVolume
}

func (f *fakeIncus) GetStoragePools() ([]api.StoragePool, error) {
	return f.pools, nil
}

func (f *fakeIncus) GetStoragePoolVolumes(pool string) ([]api.StorageVolume, error) {
	return f.vols[pool], nil
}

func TestListVolumesPagination(t *testing.T) {
	fake := &fakeIncus{
		pools: []api.StoragePool{{Name: "poolA"}, {Name: "poolB"}},
		vols: map[string][]api.StorageVolume{
			"poolA": {
				{Type: "custom", StorageVolumePut: api.StorageVolumePut{Config: map[string]string{}}, Name: "vol2"},
				{Type: "custom", StorageVolumePut: api.StorageVolumePut{Config: map[string]string{}}, Name: "vol1"},
				{Type: "image", StorageVolumePut: api.StorageVolumePut{Config: map[string]string{}}, Name: "not-a-custom-volume"},
			},
			"poolB": {
				{Type: "custom", StorageVolumePut: api.StorageVolumePut{Config: map[string]string{}}, Name: "vol3"},
			},
		},
	}

	s := &ControllerServer{Incus: fake}

	var gotIDs []string
	token := ""
	for {
		resp, err := s.ListVolumes(context.Background(), &csi.ListVolumesRequest{MaxEntries: 2, StartingToken: token})
		if err != nil {
			t.Fatalf("ListVolumes(token=%q): %v", token, err)
		}

		for _, e := range resp.GetEntries() {
			gotIDs = append(gotIDs, e.GetVolume().GetVolumeId())
		}

		if resp.GetNextToken() == "" {
			break
		}
		token = resp.GetNextToken()

		if len(gotIDs) > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	want := []string{"poolA/vol1", "poolA/vol2", "poolB/vol3"} // sorted by pool then name; image volumes excluded
	if len(gotIDs) != len(want) {
		t.Fatalf("got %v, want %v", gotIDs, want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, gotIDs[i], want[i])
		}
	}
}

func TestListVolumesInvalidStartingToken(t *testing.T) {
	fake := &fakeIncus{pools: nil, vols: map[string][]api.StorageVolume{}}
	s := &ControllerServer{Incus: fake}

	_, err := s.ListVolumes(context.Background(), &csi.ListVolumesRequest{StartingToken: "not-a-number"})
	if status.Code(err) != codes.Aborted {
		t.Errorf("ListVolumes() err = %v, want Aborted", err)
	}
}
