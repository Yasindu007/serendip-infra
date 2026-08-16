# ADR 0003: Go for control plane and agent; embedded SQLite default store

Status: Accepted
Date: 2026-08

## Context
Control plane must run in <256MB RSS on reference hardware and ship as
single binaries. Team stack is Go-friendly; Thunder/OpenChoreo ecosystem is Go.

## Decision
- Go 1.22+ for core/api/agent/providers. CGO-free where possible.
- Embedded SQLite (modernc.org/sqlite, pure Go) as default persistence.
  PostgreSQL optional via a database provider for larger installs.

## Consequences
- No ORM; hand-written queries or sqlc. Migrations embedded in binary.
- Store abstraction thin enough that SQLite to PostgreSQL is a provider swap.
- **2026-08 update (spec 001, T010)**: `modernc.org/sqlite` v1.56.0
  requires Go >= 1.25.0; adding it as a dependency auto-bumped `go.mod`'s
  `go` directive from 1.22 to 1.25.0 (Go's toolchain mechanism did this
  automatically, not a separate decision). Still satisfies this ADR's "Go
  1.22+" framing (a floor, not a ceiling) and CLAUDE.md's "Go 1.22+"
  stack line -- noted here so the effective minimum toolchain version
  isn't a surprise to a future contributor reading only CLAUDE.md.
