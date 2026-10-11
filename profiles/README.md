# Deployment profiles

edge-1 (1 node) -> small-3 (3 nodes, HA) -> standard-5 (3 control + 2 work).
Each profile: component list, pinned versions, resource budget, bench/ folder
with benchmark scripts + recorded results from reference T470 hardware.
Profiles are data, not code — the installer/topology planner consumes them.

A bench result may come from a *simulation* of reference hardware (e.g. a
CPU-throttled kind node) only if it is labeled as one at the top of the
file and in every summary. Simulations inform decisions; they never satisfy
the reference-hardware gate (constitution rules 6 and 9).
`edge-1/bench/README.md` covers how to run the footprint harness.
