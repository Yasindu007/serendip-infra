# Security

## Reporting a problem

Please report security problems **privately**, through a
[security advisory](https://github.com/Serendip-Infra/serendip-infra/security/advisories/new),
not in a public issue. Say what's affected, how to reproduce it, and what
an attacker could do with it.

You'll get an answer within 7 days. A fix and an advisory follow once the
problem is confirmed; you'll be credited unless you'd rather not be.

## Supported versions

Serendip is pre-v1: only the latest `main` gets fixes.

## What's in scope

`serendipd` and its API, the console, the CLI and MCP server,
`serendip-agent`, and how Serendip installs and configures the projects it
adopts. A problem inside an adopted project itself (K3s, OpenChoreo,
OpenBao, ThunderID, kgateway, ...) is best reported to that project; tell
us too if Serendip's defaults make it worse.

Known risks Serendip has accepted are listed in
[`docs/risk-register.md`](docs/risk-register.md).
