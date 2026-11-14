# Serendip Infra

[![CI](https://github.com/Serendip-Infra/serendip-infra/actions/workflows/ci.yml/badge.svg)](https://github.com/Serendip-Infra/serendip-infra/actions/workflows/ci.yml)
[![Roadmap](https://img.shields.io/badge/roadmap-project%20board-blue)](https://github.com/orgs/Serendip-Infra/projects/2)
[![License](https://img.shields.io/badge/license-Apache%202.0-green)](LICENSE)

An open-source, self-hostable **platform operating system**: one control
layer over composable open-source infrastructure. It gives you
cloud-provider-shaped capabilities (identity, secrets, storage, an
application platform, builds, observability) on hardware you own.

- **Small first:** it runs comfortably on one old laptop (a ThinkPad
  T470-class machine, 16 GB) and is meant to scale to a multi-node private
  cloud without reinstalling.
- **One control layer:** a resource model, capability and provider
  registries, reconciliation, authorization, approvals and audit, in one
  Go binary (`serendipd`) with embedded SQLite.
- **Bring your own:** every capability is an interface with a default
  provider (K3s, ThunderID, OpenBao, OpenChoreo, kgateway, local-path)
  that can be swapped without code changes.
- **One API for everyone:** the web console, the `serendip` CLI and the
  MCP server for AI assistants all use the same HTTP API, under the same
  roles and approvals.

## Status

Pre-v1. The proof of concept is done (specs 001–022); spec 023, the
optional observability package, is in progress. What's left for v1 is on
the [roadmap board](https://github.com/orgs/Serendip-Infra/projects/2)
under the **v1 GA** milestone. Footprint numbers so far come from a
simulated T470 (`profiles/edge-1/bench/`); the real-hardware run is still
to come.

## Getting started

- [`GETTING-STARTED.md`](GETTING-STARTED.md): build the console and
  `serendipd`, and run them against a cluster.
- [`docs/cli-and-mcp.md`](docs/cli-and-mcp.md): the CLI and the MCP
  server.

## How it's built

| Read | For |
|---|---|
| [`docs/constitution.md`](docs/constitution.md) | The non-negotiable rules. |
| [`docs/PRD.md`](docs/PRD.md) | Why, for whom, and the roadmap. |
| [`docs/architecture/`](docs/architecture/) | The design, one area per file. |
| [`docs/decisions/`](docs/decisions/) | Architecture decision records. |
| [`specs/`](specs/) | Each feature's spec, plan and tasks. |
| [`docs/risk-register.md`](docs/risk-register.md) | Known risks and accepted trade-offs. |
| [`docs/build-vs-adopt.md`](docs/build-vs-adopt.md) | What Serendip builds, adopts or rejected. |

| Directory | What |
|---|---|
| `core/` | The domain: resources, registries, reconciliation, policy, audit. Imports no providers. |
| `api/` | The HTTP API. |
| `providers/` | Capability implementations (OpenChoreo, OpenBao, ThunderID, kgateway, ...). |
| `agent/` | `serendip-agent`, which bootstraps nodes outside Kubernetes. |
| `cmd/` | `serendipd`, the `serendip` CLI and tools. |
| `mcp/` | The MCP server. |
| `ui/` | The web console (React + Vite). |
| `profiles/` | Topology profiles and their benchmarks. |

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Security problems:
[`SECURITY.md`](SECURITY.md).

## License

[Apache License 2.0](LICENSE).
