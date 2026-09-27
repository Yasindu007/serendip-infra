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
	"time"

	"github.com/serendip-infra/serendip-infra/agent/bootstrap"
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
	case "bootstrap":
		return runBootstrap(args[1:])
	default:
		usage()
		os.Exit(2)
		return nil
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: serendip-agent <discover|register|bootstrap> [flags]")
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

// runBootstrap drives a real K3s single-node install/uninstall --
// the one host-mutating operation in this whole binary
// (specs/005-k3s-bootstrap/plan.md). --dry-run always works, no root
// required, no changes made. Without --confirm, both the install and
// uninstall paths print the same dry-run summary and return an error
// rather than mutating anything (agent/CLAUDE.md's hard rule).
func runBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print what would run, without making any changes (always safe, no root required)")
	confirm := fs.Bool("confirm", false, "required to actually install or uninstall -- host mutation gate")
	uninstall := fs.Bool("uninstall", false, "uninstall k3s instead of installing it")
	version := fs.String("version", bootstrap.DefaultVersion, "k3s version to install (INSTALL_K3S_VERSION)")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the node to become Ready after install")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := bootstrap.InstallOptions{Version: *version}

	if *dryRun {
		printBootstrapPlan(opts, *uninstall)
		return nil
	}
	if !*confirm {
		printBootstrapPlan(opts, *uninstall)
		return fmt.Errorf("bootstrap: refusing to make host changes without --confirm")
	}

	ctx := context.Background()

	if *uninstall {
		if err := bootstrap.CheckLinux(); err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		if err := bootstrap.CheckRoot(); err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		if err := bootstrap.Uninstall(ctx); err != nil {
			return err
		}
		fmt.Println("bootstrap: k3s uninstalled")
		return nil
	}

	if err := bootstrap.CheckLinux(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	if err := bootstrap.CheckRoot(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	if warning, err := bootstrap.CheckSystemd(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	} else if warning != "" {
		fmt.Fprintln(os.Stderr, "bootstrap: warning:", warning)
	}
	if warning, err := bootstrap.CheckIPTablesMode(); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	} else if warning != "" {
		fmt.Fprintln(os.Stderr, "bootstrap: warning:", warning)
	}

	if err := bootstrap.Install(ctx, opts); err != nil {
		return err
	}
	if err := bootstrap.WaitForReady(ctx, *timeout); err != nil {
		return err
	}
	fmt.Println("bootstrap: k3s installed and ready")
	return nil
}

func printBootstrapPlan(opts bootstrap.InstallOptions, uninstall bool) {
	if uninstall {
		fmt.Println("Would run the official k3s uninstall script (/usr/local/bin/k3s-uninstall.sh).")
		fmt.Println("No changes made (dry-run, or --confirm not given).")
		return
	}
	fmt.Println("Would run the official K3s install script (https://get.k3s.io) with:")
	for _, e := range bootstrap.InstallEnv(opts) {
		fmt.Println("  " + e)
	}
	fmt.Println("No changes made (dry-run, or --confirm not given).")
}
