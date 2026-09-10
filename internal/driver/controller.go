package driver

import (
	"context"
	"fmt"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	incus "github.com/lxc/incus/v7/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"csi-incus/internal/incusvol"
)

const defaultVolumeSizeBytes = 1 << 30 // 1GiB, used when a PVC sets no size

// singleNodeModes are the only access modes safe for an Incus block
// volume: it must never be attached to more than one VM at a time.
var singleNodeModes = map[csi.VolumeCapability_AccessMode_Mode]bool{
	csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:        true,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER: true,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:  true,
	csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:   true,
}

type ControllerServer struct {
	csi.UnimplementedControllerServer

	Incus incus.InstanceServer
}

func (s *ControllerServer) ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	rpcs := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	}

	caps := make([]*csi.ControllerServiceCapability, len(rpcs))
	for i, rpc := range rpcs {
		caps[i] = &csi.ControllerServiceCapability{Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{Type: rpc},
		}}
	}

	return &csi.ControllerGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (s *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	pool := req.GetParameters()["pool"]
	if pool == "" {
		return nil, status.Error(codes.InvalidArgument, "storage class must set the \"pool\" parameter to an Incus storage pool name")
	}

	contentType := req.GetParameters()["contentType"]
	if contentType == "" {
		contentType = "block"
	}
	if contentType != "block" && contentType != "filesystem" {
		return nil, status.Errorf(codes.InvalidArgument, "storage class \"contentType\" must be \"block\" or \"filesystem\", got %q", contentType)
	}

	// Block volumes must never be attached to more than one VM at a
	// time; filesystem volumes are shared mounts and Incus allows
	// attaching them to many instances concurrently.
	if contentType == "block" {
		for _, c := range req.GetVolumeCapabilities() {
			if !singleNodeModes[c.GetAccessMode().GetMode()] {
				return nil, status.Errorf(codes.InvalidArgument, "Incus block volumes only support single-node access modes, got %s", c.GetAccessMode().GetMode())
			}
		}
	}

	size := int64(req.GetCapacityRange().GetRequiredBytes())
	if size == 0 {
		size = int64(req.GetCapacityRange().GetLimitBytes())
	}
	if size == 0 {
		size = defaultVolumeSizeBytes
	}

	var srcPool, srcVol, srcSnap string
	if snap := req.GetVolumeContentSource().GetSnapshot(); snap != nil {
		var err error
		srcPool, srcVol, srcSnap, err = decodeSnapshotID(snap.GetSnapshotId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if srcPool != pool {
			return nil, status.Error(codes.InvalidArgument, "snapshot source must be in the same pool as the requested volume")
		}
	}

	if err := incusvol.CreateVolume(s.Incus, pool, name, contentType, size, srcPool, srcVol, srcSnap); err != nil {
		return nil, status.Errorf(codes.Internal, "create volume: %v", err)
	}

	vol := &csi.Volume{
		VolumeId:      encodeVolumeID(pool, name),
		CapacityBytes: size,
		VolumeContext: map[string]string{"pool": pool, "content_type": contentType},
	}
	if srcVol != "" {
		vol.ContentSource = req.GetVolumeContentSource()
	}

	return &csi.CreateVolumeResponse{Volume: vol}, nil
}

func (s *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	pool, name, err := decodeVolumeID(req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := incusvol.DeleteVolume(s.Incus, pool, name); err != nil {
		return nil, status.Errorf(codes.Internal, "delete volume: %v", err)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume attaches the volume as a disk device on the
// Incus instance named by req.NodeId. NodeId must equal the Incus instance
// name of the target Kubernetes node — see NodeGetInfo / the README.
func (s *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	pool, name, err := decodeVolumeID(req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	contentType := req.GetVolumeContext()["content_type"]
	if contentType == "" {
		contentType = "block"
	}

	if contentType == "block" && !singleNodeModes[req.GetVolumeCapability().GetAccessMode().GetMode()] {
		return nil, status.Errorf(codes.InvalidArgument, "Incus block volumes only support single-node access modes, got %s", req.GetVolumeCapability().GetAccessMode().GetMode())
	}

	dev := deviceName(req.GetVolumeId())
	publishCtx := map[string]string{"device_name": dev, "content_type": contentType}

	var guestPath string
	if contentType == "filesystem" {
		guestPath = "/run/csi-incus/" + dev
		publishCtx["guest_path"] = guestPath
	}

	if err := incusvol.AttachToInstance(s.Incus, req.GetNodeId(), dev, pool, name, guestPath); err != nil {
		return nil, status.Errorf(codes.Internal, "attach volume to instance %q: %v", req.GetNodeId(), err)
	}

	return &csi.ControllerPublishVolumeResponse{PublishContext: publishCtx}, nil
}

func (s *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if _, _, err := decodeVolumeID(req.GetVolumeId()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}

	dev := deviceName(req.GetVolumeId())

	if err := incusvol.DetachFromInstance(s.Incus, req.GetNodeId(), dev); err != nil {
		return nil, status.Errorf(codes.Internal, "detach volume from instance %q: %v", req.GetNodeId(), err)
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (s *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	pool, name, err := decodeVolumeID(req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	_, contentType, err := incusvol.GetVolume(s.Incus, pool, name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %q not found: %v", req.GetVolumeId(), err)
	}

	if contentType == "block" {
		for _, c := range req.GetVolumeCapabilities() {
			if !singleNodeModes[c.GetAccessMode().GetMode()] {
				return &csi.ValidateVolumeCapabilitiesResponse{Message: fmt.Sprintf("unsupported access mode %s", c.GetAccessMode().GetMode())}, nil
			}
		}
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

func (s *ControllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	pool, vol, err := decodeVolumeID(req.GetSourceVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := incusvol.CreateSnapshot(s.Incus, pool, vol, req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "create snapshot: %v", err)
	}

	size, _, err := incusvol.GetVolume(s.Incus, pool, vol)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get source volume: %v", err)
	}

	return &csi.CreateSnapshotResponse{Snapshot: &csi.Snapshot{
		SnapshotId:     encodeSnapshotID(pool, vol, req.GetName()),
		SourceVolumeId: req.GetSourceVolumeId(),
		SizeBytes:      size,
		ReadyToUse:     true,
	}}, nil
}

func (s *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	pool, vol, snap, err := decodeSnapshotID(req.GetSnapshotId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := incusvol.DeleteSnapshot(s.Incus, pool, vol, snap); err != nil {
		return nil, status.Errorf(codes.Internal, "delete snapshot: %v", err)
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

func (s *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	pool, name, err := decodeVolumeID(req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	newSize := int64(req.GetCapacityRange().GetRequiredBytes())
	if newSize == 0 {
		newSize = int64(req.GetCapacityRange().GetLimitBytes())
	}
	if newSize == 0 {
		return nil, status.Error(codes.InvalidArgument, "capacity_range is required")
	}

	curSize, contentType, err := incusvol.GetVolume(s.Incus, pool, name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %q not found: %v", req.GetVolumeId(), err)
	}

	if contentType == "block" && newSize < curSize {
		return nil, status.Error(codes.InvalidArgument, "Incus block volumes cannot be shrunk")
	}

	if err := incusvol.ResizeVolume(s.Incus, pool, name, newSize); err != nil {
		return nil, status.Errorf(codes.Internal, "resize volume: %v", err)
	}

	// Block volumes may have a guest filesystem on top that also needs
	// growing; filesystem-content-type volumes are Incus-managed shares
	// with nothing local left to resize.
	return &csi.ControllerExpandVolumeResponse{CapacityBytes: newSize, NodeExpansionRequired: contentType == "block"}, nil
}

func (s *ControllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	vols, err := incusvol.ListVolumes(s.Incus)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list volumes: %v", err)
	}

	start := 0
	if tok := req.GetStartingToken(); tok != "" {
		start, err = strconv.Atoi(tok)
		if err != nil || start < 0 || start > len(vols) {
			return nil, status.Errorf(codes.Aborted, "invalid starting_token %q", tok)
		}
	}

	end := len(vols)
	if max := int(req.GetMaxEntries()); max > 0 && start+max < end {
		end = start + max
	}

	entries := make([]*csi.ListVolumesResponse_Entry, 0, end-start)
	for _, v := range vols[start:end] {
		entries = append(entries, &csi.ListVolumesResponse_Entry{Volume: &csi.Volume{
			VolumeId:      encodeVolumeID(v.Pool, v.Name),
			CapacityBytes: v.SizeBytes,
			VolumeContext: map[string]string{"pool": v.Pool, "content_type": v.ContentType},
		}})
	}

	resp := &csi.ListVolumesResponse{Entries: entries}
	if end < len(vols) {
		resp.NextToken = strconv.Itoa(end)
	}

	return resp, nil
}

func (s *ControllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	pool := req.GetParameters()["pool"]
	if pool == "" {
		return nil, status.Error(codes.InvalidArgument, "parameters must include \"pool\"")
	}

	available, _, err := incusvol.PoolCapacity(s.Incus, pool)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get pool resources: %v", err)
	}

	return &csi.GetCapacityResponse{AvailableCapacity: available}, nil
}
