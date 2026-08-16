// Command bench-resource-model is spec 001's T016 smoke benchmark: create
// ~10k resources across several kinds, with realistic owner/relationship
// edges, in the SQLite store, then report process memory and elapsed
// time. This is a standalone binary rather than a `go test` benchmark so
// the number reflects what a real running process looks like (closer to
// constitution rule 4's actual framing: "control plane... < 256MB RSS
// combined at idle") rather than the test harness's own overhead.
//
// The reported number is directional only -- see
// profiles/edge-1/bench/001-resource-model.md and docs/PRD.md's Hardware
// section for why. Run it with: go run ./cmd/bench-resource-model
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/serendip-infra/serendip-infra/core/resource"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

const (
	projectCount     = 10
	appsPerProject   = 20
	componentsPerApp = 49 // 1 org + 10 projects + 200 apps + 9800 components = 10011
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bench-resource-model:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "serendip-bench-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "bench.db")

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()
	if err := sqlite.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	orgs := sqlite.NewStore[resource.OrganizationSpec, resource.OrganizationStatus](db, resource.KindOrganization)
	projects := sqlite.NewStore[resource.ProjectSpec, resource.ProjectStatus](db, resource.KindProject)
	apps := sqlite.NewStore[resource.ApplicationSpec, resource.ApplicationStatus](db, resource.KindApplication)
	components := sqlite.NewStore[resource.ComponentSpec, resource.ComponentStatus](db, resource.KindComponent)

	start := time.Now()
	count := 0

	org, err := orgs.Create(ctx, resource.Organization{Metadata: resource.Metadata{Name: "bench-org"}})
	if err != nil {
		return fmt.Errorf("create org: %w", err)
	}
	count++

	for p := range projectCount {
		project, err := projects.Create(ctx, resource.Project{
			Metadata: resource.Metadata{
				Name:      fmt.Sprintf("project-%d", p),
				OwnerRefs: []resource.OwnerRef{{Kind: resource.KindOrganization, ID: org.Metadata.ID}},
			},
		})
		if err != nil {
			return fmt.Errorf("create project %d: %w", p, err)
		}
		count++

		for a := range appsPerProject {
			app, err := apps.Create(ctx, resource.Application{
				Metadata: resource.Metadata{
					Name:      fmt.Sprintf("project-%d-app-%d", p, a),
					OwnerRefs: []resource.OwnerRef{{Kind: resource.KindProject, ID: project.Metadata.ID}},
				},
			})
			if err != nil {
				return fmt.Errorf("create app %d/%d: %w", p, a, err)
			}
			count++

			var previousComponentID string
			for c := range componentsPerApp {
				meta := resource.Metadata{
					Name:      fmt.Sprintf("project-%d-app-%d-component-%d", p, a, c),
					OwnerRefs: []resource.OwnerRef{{Kind: resource.KindApplication, ID: app.Metadata.ID}},
				}
				// Chain each component to the previous one via a
				// depends-on relationship, so the edges table grows
				// realistically alongside the resources table, not just
				// owns edges.
				if previousComponentID != "" {
					meta.Relationships = []resource.Relationship{
						{Type: resource.EdgeDependsOn, TargetKind: resource.KindComponent, TargetID: previousComponentID},
					}
				}
				component, err := components.Create(ctx, resource.Component{Metadata: meta})
				if err != nil {
					return fmt.Errorf("create component %d/%d/%d: %w", p, a, c, err)
				}
				count++
				previousComponentID = component.Metadata.ID
			}
		}
	}

	elapsed := time.Since(start)

	runtime.GC()
	rssBytes, rssSource, rssErr := processMemory()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	fmt.Printf("resources created: %d\n", count)
	fmt.Printf("elapsed: %s\n", elapsed)
	fmt.Printf("Go runtime Sys (memory obtained from OS, includes freed-but-unreturned): %.1f MB\n", float64(memStats.Sys)/(1<<20))
	fmt.Printf("Go runtime HeapAlloc (live heap objects): %.1f MB\n", float64(memStats.HeapAlloc)/(1<<20))
	if rssErr != nil {
		fmt.Printf("process memory: unavailable (%v)\n", rssErr)
	} else {
		fmt.Printf("process memory (%s): %.1f MB\n", rssSource, float64(rssBytes)/(1<<20))
	}
	return nil
}

// processMemory returns this process's OS-reported resident memory.
// Tries /proc/self/status first (Linux -- the actual T470/edge-1 target
// platform, ADR 0001), falling back to querying the Windows working set
// via PowerShell (no new Go dependency for a one-off benchmark tool) so
// the same binary works unmodified on both the dev machine now and real
// hardware in Phase 3b.
func processMemory() (uint64, string, error) {
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, "", fmt.Errorf("parse /proc/self/status VmRSS: %w", err)
			}
			return kb * 1024, "/proc/self/status VmRSS", nil
		}
		return 0, "", fmt.Errorf("VmRSS not found in /proc/self/status")
	}

	pid := os.Getpid()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("(Get-Process -Id %d).WorkingSet64", pid))
	out, err := cmd.Output()
	if err != nil {
		return 0, "", fmt.Errorf("query Windows working set: %w", err)
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("parse Windows working set output %q: %w", out, err)
	}
	return bytes, "Get-Process WorkingSet64 (Windows)", nil
}
