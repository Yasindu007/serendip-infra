# Deployment profiles

edge-1 (1 node) -> small-3 (3 nodes, HA) -> standard-5 (3 control + 2 work).
Each profile: component list, pinned versions, resource budget, bench/ folder
with benchmark scripts + recorded results from reference T470 hardware.
Profiles are data, not code — the installer/topology planner consumes them.
