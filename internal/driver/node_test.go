package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodeGetVolumeStats(t *testing.T) {
	s := &NodeServer{}

	resp, err := s.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{VolumePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Usage) != 2 {
		t.Fatalf("want bytes+inodes usage, got %d entries", len(resp.Usage))
	}
	b := resp.Usage[0]
	if b.Unit != csi.VolumeUsage_BYTES || b.Total <= 0 || b.Used <= 0 || b.Used > b.Total {
		t.Fatalf("bad byte usage: %+v", b)
	}

	_, err = s.NodeGetVolumeStats(context.Background(), &csi.NodeGetVolumeStatsRequest{VolumePath: "/nonexistent/vol"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}
