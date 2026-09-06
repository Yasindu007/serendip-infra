// Package client is a minimal net/http client that upserts this agent's
// own Node resource against a Serendip control plane's existing /v1/nodes
// API (spec 002). Defines its own wire structs rather than importing
// core/resource -- agent/ is just another API client, like any future
// UI/CLI/MCP consumer (constitution rule 7). See
// specs/004-bootstrap-agent/plan.md for the design.
package client
