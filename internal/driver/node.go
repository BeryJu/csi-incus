package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	mount "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
)

const defaultFSType = "ext4"

type NodeServer struct {
	csi.UnimplementedNodeServer

	NodeID  string
	Mounter *mount.SafeFormatAndMount
}

func NewNodeServer(nodeID string) *NodeServer {
	return &NodeServer{
		NodeID:  nodeID,
		Mounter: mount.NewSafeFormatAndMount(mount.New(""), utilexec.New()),
	}
}

func (s *NodeServer) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: s.NodeID}, nil
}

func (s *NodeServer) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	rpcs := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
	}

	caps := make([]*csi.NodeServiceCapability, len(rpcs))
	for i, rpc := range rpcs {
		caps[i] = &csi.NodeServiceCapability{Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{Type: rpc},
		}}
	}

	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

func (s *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if req.GetPublishContext()["content_type"] == "filesystem" {
		return &csi.NodeStageVolumeResponse{}, nil // Incus already mounted it at guest_path
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		return &csi.NodeStageVolumeResponse{}, nil // raw block: nothing to format
	}

	mnt := req.GetVolumeCapability().GetMount()
	if mnt == nil {
		return nil, status.Error(codes.InvalidArgument, "volume_capability must set block or mount")
	}

	devName := req.GetPublishContext()["device_name"]
	if devName == "" {
		return nil, status.Error(codes.InvalidArgument, "publish_context is missing device_name")
	}

	devPath, err := waitForDevice(ctx, devName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "locate attached device: %v", err)
	}

	fsType := mnt.GetFsType()
	if fsType == "" {
		fsType = defaultFSType
	}

	if err := os.MkdirAll(req.GetStagingTargetPath(), 0750); err != nil {
		return nil, status.Errorf(codes.Internal, "create staging path: %v", err)
	}

	if err := s.Mounter.FormatAndMount(devPath, req.GetStagingTargetPath(), fsType, mnt.GetMountFlags()); err != nil {
		return nil, status.Errorf(codes.Internal, "format and mount: %v", err)
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (s *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	if err := mount.CleanupMountPoint(req.GetStagingTargetPath(), s.Mounter, true); err != nil {
		return nil, status.Errorf(codes.Internal, "unmount staging path: %v", err)
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (s *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	target := req.GetTargetPath()

	options := []string{"bind"}
	if req.GetReadonly() {
		options = append(options, "ro")
	}

	if req.GetPublishContext()["content_type"] == "filesystem" {
		guestPath := req.GetPublishContext()["guest_path"]
		if guestPath == "" {
			return nil, status.Error(codes.InvalidArgument, "publish_context is missing guest_path")
		}

		if err := os.MkdirAll(target, 0750); err != nil {
			return nil, status.Errorf(codes.Internal, "create target path: %v", err)
		}

		if err := s.Mounter.Mount(guestPath, target, "", options); err != nil {
			return nil, status.Errorf(codes.Internal, "bind mount guest path: %v", err)
		}

		return &csi.NodePublishVolumeResponse{}, nil
	}

	if req.GetVolumeCapability().GetBlock() != nil {
		devName := req.GetPublishContext()["device_name"]
		if devName == "" {
			return nil, status.Error(codes.InvalidArgument, "publish_context is missing device_name")
		}

		devPath, err := waitForDevice(ctx, devName)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "locate attached device: %v", err)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return nil, status.Errorf(codes.Internal, "create target parent: %v", err)
		}

		f, err := os.OpenFile(target, os.O_CREATE, 0660)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create target file: %v", err)
		}
		if err := f.Close(); err != nil {
			return nil, status.Errorf(codes.Internal, "create target file: %v", err)
		}

		if err := s.Mounter.Mount(devPath, target, "", options); err != nil {
			return nil, status.Errorf(codes.Internal, "bind mount device: %v", err)
		}

		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err := os.MkdirAll(target, 0750); err != nil {
		return nil, status.Errorf(codes.Internal, "create target path: %v", err)
	}

	if err := s.Mounter.Mount(req.GetStagingTargetPath(), target, "", options); err != nil {
		return nil, status.Errorf(codes.Internal, "bind mount staging path: %v", err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (s *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if err := mount.CleanupMountPoint(req.GetTargetPath(), s.Mounter, true); err != nil {
		return nil, status.Errorf(codes.Internal, "unmount target path: %v", err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (s *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	if req.GetVolumeCapability().GetBlock() != nil {
		return &csi.NodeExpandVolumeResponse{}, nil // raw block: nothing to grow
	}

	path := req.GetVolumePath()

	devPath, _, err := mount.GetDeviceNameFromMount(s.Mounter, path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "find device for %q: %v", path, err)
	}

	if _, err := mount.NewResizeFs(utilexec.New()).Resize(devPath, path); err != nil {
		return nil, status.Errorf(codes.Internal, "resize filesystem: %v", err)
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

func (s *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	path := req.GetVolumePath()
	if path == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_path is required")
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume path %q not found", path)
	} else if err != nil {
		return nil, status.Errorf(codes.Internal, "stat %q: %v", path, err)
	}

	if !info.IsDir() {
		// raw block: only the device size is knowable
		f, err := os.Open(path)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "open %q: %v", path, err)
		}
		defer f.Close()

		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "size of %q: %v", path, err)
		}

		return &csi.NodeGetVolumeStatsResponse{Usage: []*csi.VolumeUsage{
			{Unit: csi.VolumeUsage_BYTES, Total: size},
		}}, nil
	}

	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return nil, status.Errorf(codes.Internal, "statfs %q: %v", path, err)
	}

	bsize := int64(st.Bsize)
	return &csi.NodeGetVolumeStatsResponse{Usage: []*csi.VolumeUsage{
		{
			Unit:      csi.VolumeUsage_BYTES,
			Total:     int64(st.Blocks) * bsize,
			Available: int64(st.Bavail) * bsize,
			Used:      int64(st.Blocks-st.Bfree) * bsize,
		},
		{
			Unit:      csi.VolumeUsage_INODES,
			Total:     int64(st.Files),
			Available: int64(st.Ffree),
			Used:      int64(st.Files - st.Ffree),
		},
	}}, nil
}

// waitForDevice resolves the disk-by-id symlink Incus/qemu creates for a
// hotplugged volume's serial (== our device name, see deviceName in
// ids.go). It's polled because the udev event can lag slightly behind the
// ControllerPublishVolume call that triggered the hotplug.
func waitForDevice(ctx context.Context, serial string) (string, error) {
	pattern := fmt.Sprintf("/dev/disk/by-id/*%s*", serial)

	deadline := time.Now().Add(30 * time.Second)
	for {
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			return filepath.EvalSymlinks(matches[0])
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("no /dev/disk/by-id entry for serial %q after waiting", serial)
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
