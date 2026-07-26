# Adaptive host sizing for jp
#
# Tiers (auto-detected from RAM, or set JP_SIZE manually):
#   small   < 6 GB   — 1 worker, concurrency 1, tight limits (4GB VPS class)
#   medium  6–16 GB  — scale worker×2, concurrency 2, monitoring OK
#   large   16–32 GB — worker×4, concurrency 2, docker builds recommended
#   xlarge  32+ GB   — worker×8, concurrency 4, full stack
#
# Usage:
#   .\scripts\size-host.ps1          # writes .env.resources
#   .\scripts\jp-up.ps1 -Profiles platform,ui
#   ./scripts/size-host.sh && ./scripts/jp-up.sh platform,ui

| Tier | RAM guide | Worker replicas | Concurrency / worker | Suggested profiles |
|------|-----------|-----------------|----------------------|--------------------|
| small | ~4 GB | 1 | 1 | core + platform (skip monitoring) |
| medium | ~8–16 GB | 2 | 2 | platform + ui + data |
| large | ~16–32 GB | 4 | 2 | platform + ui + data + monitoring |
| xlarge | 32 GB+ | 8 | 4 | all profiles |

Override files live in this directory: `small.yml`, `medium.yml`, `large.yml`, `xlarge.yml`.
