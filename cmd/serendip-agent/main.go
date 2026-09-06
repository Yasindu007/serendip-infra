// Command serendip-agent is the node bootstrap agent: hardware/
// environment discovery, local node identity, and self-registration
// against a Serendip control plane's /v1/nodes API. Lives outside
// Kubernetes (agent/CLAUDE.md). See specs/004-bootstrap-agent/plan.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/serendip-infra/serendip-infra/agent/client"
	"github.com/serendip-infra/serendip-infra/agent/discover"
	"github.com/serendip-infra/serendip-infra/agent/identity"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "serendip-agent:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "discover":
		return runDiscover(args[1:])
	case "register":
		return runRegister(args[1:])
	default:
		usage()
		os.Exit(2)
		return nil
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: serendip-agent <discover|register> [flags]")
}

// runDiscover prints discovered facts as JSON to stdout. No network
// call -- always safe (agent/CLAUDE.md: "discovery is always
// safe/read-only").
func runDiscover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	res, err := discover.Discover(context.Background())
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// runRegister discovers this host's hardware facts and upserts its Node
// resource against the given control plane. Neither this nor discover
// needs --confirm (agent/CLAUDE.md's --confirm gate is for *host*
// mutations -- installing K3s, formatting a disk; register mutates the
// control plane's data, not the local host).
func runRegister(args []string) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	controlPlaneURL := fs.String("control-plane-url", "", "base URL of the Serendip control plane (required)")
	token := fs.String("token", "", "bearer token for control-plane auth (required)")
	name := fs.String("name", "", "node name (default: this host's hostname)")
	// Default resolution order matches cmd/serendipd's SERENDIP_DB_PATH/
	// SERENDIP_ADDR pattern: flag > env var > hardcoded fallback.
	// specs/004-bootstrap-agent/plan.md's resolved decision names
	// SERENDIP_AGENT_STATE_DIR explicitly -- T009's holistic review
	// caught that this env var had never actually been wired up despite
	// being a recorded design decision.
	defaultStateDir := "/var/lib/serendip/agent"
	if v := os.Getenv("SERENDIP_AGENT_STATE_DIR"); v != "" {
		defaultStateDir = v
	}
	stateDir := fs.String("state-dir", defaultStateDir, "directory for persisted agent state (env: SERENDIP_AGENT_STATE_DIR)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *controlPlaneURL == "" || *token == "" {
		return fmt.Errorf("register: --control-plane-url and --token are required")
	}

	nodeName := *name
	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("register: determine hostname: %w", err)
		}
		nodeName = hostname
	}

	nodeID, err := identity.Load(*stateDir)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	res, err := discover.Discover(context.Background())
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	status := client.NodeStatusWire{
		CPUCores:              res.CPUCores,
		MemoryBytes:           res.MemoryBytes,
		DiskType:              res.DiskType,
		DiskBytes:             res.DiskBytes,
		NetworkMbps:           res.NetworkMbps,
		VirtualizationSupport: res.VirtualizationSupport,
	}

	c := client.New(*controlPlaneURL, *token)
	node, err := c.Upsert(context.Background(), nodeID, nodeName, status)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	fmt.Printf("registered node %s (id=%s, resourceVersion=%d)\n",
		node.Metadata.Name, node.Metadata.ID, node.Metadata.ResourceVersion)
	return nil
}
