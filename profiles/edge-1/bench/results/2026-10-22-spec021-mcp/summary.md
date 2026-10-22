# Spec 021 footprint (T010)

Measured 2026-10-22 on the development machine: AMD Ryzen 7 7840HS, 31 GiB,
Windows 11 (see `go-version.txt`).

## Results

- **Rule 4: PASS.** `serendipd` grows by +0.7 MiB private memory at idle,
  and stays far below 256 MiB.
- **`serendip mcp`**, the stdio server an AI client starts, idles at
  17.8 MiB private (12.9 MiB working set).

## `serendipd` idle memory (`idle-windows.txt`)

**Method:** spec 017's and 019's.
- **Each run:** a fresh store, no cluster, one API key, four
  authenticated reads, then 30 s idle, then three samples 5 s apart.
- **Order:** three runs per build, alternating.
- **Each line** gives private bytes, then the working set, in MiB.

| Build | Private bytes (median) | Working set (median) |
|---|---|---|
| Before spec 021 (`e227e9b`) | 16.0 MiB | 14.2 MiB |
| Spec 021 (`d6f9980` plus the T011 fix) | 16.7 MiB | 15.2 MiB |
| Growth | **+0.7 MiB** | **+1.0 MiB** |

**Caveat:** without a cluster, `serendipd` doesn't register `/mcp`, since
that needs identity. These runs measure what linking the MCP SDK, the
adapter and the catalogue costs at idle.

**With everything live:** `serendipd` on `serendip-t018` (kind), with
identity, `/mcp` and the resource metadata document, after spec 021 T011's
real use, measured **22.8 MiB private / 30.8 MiB working set**. Spec 019's
simulated-T470 harness run had `serendipd` at 27.4 MiB.

## Binary size

| Binary (unstripped, Windows) | Size |
|---|---|
| `serendipd` before spec 021 | 20.3 MB |
| `serendipd` with spec 021 | 23.8 MB (+3.4 MB) |
| `serendip` (CLI and `serendip mcp`) | 13.2 MB |

ADR 0017 estimated at most +2.4 MB *stripped* for the SDK. The unstripped
growth also counts symbols, and the catalogue, `/mcp` and resource
metadata code.

## `serendip mcp` idle memory

**Method:** started as an AI client starts it, with standard input held
open. After one `tools/list`, it sat idle for 10 s, then three samples 5 s
apart: **17.8 MiB private / 12.9 MiB working set** each time.

It runs only while an AI client is connected, on the person's machine, not
on the platform.
