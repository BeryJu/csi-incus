package driver

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		hasController bool
		wantCaps      int
	}{
		{"controller mode advertises controller service", true, 1},
		{"node-only mode advertises nothing", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &IdentityServer{HasController: tt.hasController}
			resp, err := s.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Capabilities) != tt.wantCaps {
				t.Fatalf("got %d capabilities, want %d", len(resp.Capabilities), tt.wantCaps)
			}
		})
	}
}
