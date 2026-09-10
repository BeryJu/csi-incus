// Command csi-incus-driver is a CSI plugin that provisions Incus custom
// block volumes and attaches them to Incus VM instances backing
// Kubernetes nodes.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	"beryju.io/csi-incus/internal/driver"
	"beryju.io/csi-incus/internal/incusvol"
)

func main() {
	var (
		endpoint   = flag.String("endpoint", "unix:///csi/csi.sock", "CSI gRPC endpoint")
		mode       = flag.String("mode", "controller,node", "comma-separated: controller, node")
		nodeID     = flag.String("node-id", "", "Incus instance name of this node (must match the k8s Node name); defaults to $NODE_NAME, then hostname")
		incusAddr  = flag.String("incus-addr", os.Getenv("INCUS_ADDR"), "Incus server address (https://host:8443, or empty/unix://path for the local socket); only needed in controller mode")
		clientCert = flag.String("incus-client-cert", os.Getenv("INCUS_CLIENT_CERT"), "Incus TLS client cert (PEM or file path)")
		clientKey  = flag.String("incus-client-key", os.Getenv("INCUS_CLIENT_KEY"), "Incus TLS client key (PEM or file path)")
		serverCert = flag.String("incus-server-cert", os.Getenv("INCUS_SERVER_CERT"), "Incus TLS server cert to trust (PEM or file path)")
		insecure   = flag.Bool("incus-insecure-skip-verify", os.Getenv("INCUS_INSECURE_SKIP_VERIFY") == "true", "skip TLS verification of the Incus server")
	)
	flag.Parse()

	modes := map[string]bool{}
	for _, m := range strings.Split(*mode, ",") {
		modes[strings.TrimSpace(m)] = true
	}

	if *nodeID == "" {
		id, err := resolveNodeID()
		if err != nil {
			log.Fatalf("determine node id: %v", err)
		}
		*nodeID = id
	}

	srv := grpc.NewServer()
	csi.RegisterIdentityServer(srv, &driver.IdentityServer{HasController: modes["controller"]})

	if modes["controller"] {
		conn, err := incusvol.Connect(incusvol.ConnConfig{
			Addr:            *incusAddr,
			TLSClientCert:   *clientCert,
			TLSClientKey:    *clientKey,
			TLSServerCert:   *serverCert,
			InsecureSkipTLS: *insecure,
		})
		if err != nil {
			log.Fatalf("connect to Incus: %v", err)
		}

		csi.RegisterControllerServer(srv, &driver.ControllerServer{Incus: conn})
		log.Printf("controller service registered (incus addr=%q)", *incusAddr)
	}

	if modes["node"] {
		csi.RegisterNodeServer(srv, driver.NewNodeServer(*nodeID))
		log.Printf("node service registered (node id=%q)", *nodeID)
	}

	listener, err := listen(*endpoint)
	if err != nil {
		log.Fatalf("listen on %q: %v", *endpoint, err)
	}

	log.Printf("csi-incus-driver listening on %s", *endpoint)
	if err := srv.Serve(listener); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// resolveNodeID is the --node-id default: a pod's own hostname is its pod
// name, not the Kubernetes Node's name, so the DaemonSet sets NODE_NAME
// from the downward API (spec.nodeName) for exactly this reason. Hostname
// is only a fallback for running the binary directly on an Incus instance.
func resolveNodeID() (string, error) {
	if n := os.Getenv("NODE_NAME"); n != "" {
		return n, nil
	}

	return os.Hostname()
}

func listen(endpoint string) (net.Listener, error) {
	addr := strings.TrimPrefix(endpoint, "unix://")
	if addr == endpoint {
		return nil, fmt.Errorf("endpoint must start with unix://, got %q", endpoint)
	}

	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return net.Listen("unix", addr)
}
