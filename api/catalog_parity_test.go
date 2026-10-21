package api

import (
	"maps"
	"slices"
	"testing"

	"github.com/serendip-infra/serendip-infra/api/catalog"
)

// parityErrors checks the catalogue against a route table (spec 021
// acceptance criterion 2 and scope item 19): every route is on the CLI and
// on MCP, or excluded from that surface with a reason, and the catalogue
// names no route the table doesn't have.
func parityErrors(table map[string]rule) []string {
	onCLI, onMCP := map[string]bool{}, map[string]bool{}
	var errs []string
	for _, op := range catalog.Operations() {
		for _, r := range op.Routes() {
			if _, ok := table[r]; !ok {
				errs = append(errs, "operation "+op.Name+" names "+r+", which isn't in the route table")
			}
			if op.CLI != nil {
				onCLI[r] = true
			}
			if op.MCP {
				onMCP[r] = true
			}
		}
	}
	excluded := catalog.Excluded()
	for r, ex := range excluded {
		if _, ok := table[r]; !ok {
			errs = append(errs, "exclusion for "+r+", which isn't in the route table")
		}
		if ex.CLI != "" && onCLI[r] {
			errs = append(errs, r+" is excluded from the CLI but a command covers it")
		}
		if ex.MCP != "" && onMCP[r] {
			errs = append(errs, r+" is excluded from MCP but a tool covers it")
		}
	}
	for _, r := range slices.Sorted(maps.Keys(table)) {
		if !onCLI[r] && excluded[r].CLI == "" {
			errs = append(errs, r+" has no CLI command and no reason it's left off")
		}
		if !onMCP[r] && excluded[r].MCP == "" {
			errs = append(errs, r+" has no MCP tool and no reason it's left off")
		}
	}
	return errs
}

func TestCatalogueCoversEveryRoute(t *testing.T) {
	for _, e := range parityErrors(routeTable()) {
		t.Error(e)
	}
}

func TestCatalogueParityCatchesANewRoute(t *testing.T) {
	table := routeTable()
	table["GET /v1/unmapped"] = rule{authOnly: true}
	errs := parityErrors(table)
	want := []string{
		"GET /v1/unmapped has no CLI command and no reason it's left off",
		"GET /v1/unmapped has no MCP tool and no reason it's left off",
	}
	if !slices.Equal(errs, want) {
		t.Errorf("errors = %q, want %q", errs, want)
	}
}

func TestCatalogueParityCatchesStaleEntries(t *testing.T) {
	table := routeTable()
	delete(table, "GET /v1/readiness")                      // covered by readiness_get
	delete(table, "POST /v1/authorization/roleAssignments") // excluded from MCP
	errs := parityErrors(table)
	for _, want := range []string{
		"operation readiness_get names GET /v1/readiness, which isn't in the route table",
		"exclusion for POST /v1/authorization/roleAssignments, which isn't in the route table",
	} {
		if !slices.Contains(errs, want) {
			t.Errorf("errors %q lack %q", errs, want)
		}
	}
}

func TestCatalogueKindsMatchTheAPI(t *testing.T) {
	want := map[string]string{}
	for k, p := range resourcePaths {
		want[string(k)] = p
	}
	if got := catalog.KindPaths(); !maps.Equal(got, want) {
		t.Errorf("catalog kinds = %v, api resourcePaths = %v", got, want)
	}
}
