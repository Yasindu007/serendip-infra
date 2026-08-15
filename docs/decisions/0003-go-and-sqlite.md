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
