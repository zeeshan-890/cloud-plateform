# Releasing

1. Make sure `main` is green (CI: Go vet/race tests, govulncheck, dashboard lint/tsc/build, compose, shellcheck, images).
2. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. `.github/workflows/release.yml` then, for each of the 21 services, the worker and the dashboard:
   * builds `linux/amd64` + `linux/arm64` with Buildx (Go cross-compiled natively, no QEMU for compilation),
   * pushes `ghcr.io/<owner>/<repo>/<name>:<version>`, `:<major>.<minor>`, `:sha-<short>` with BuildKit SBOM and provenance attestations,
   * produces a standalone SPDX SBOM per image (uploaded as workflow artifact and attached to the release),
   * signs the image with keyless cosign **only if** the repository variable `COSIGN_ENABLED` is `true`,
   * creates the GitHub release with `install.sh`, `install.ps1`, `uninstall.*`, a tarball of `infra/`+`scripts/`, and `SHA256SUMS`.
4. The compose file builds images locally by default (`scripts/upgrade.sh` rebuilds from the checkout); the published images
   are for operators who prefer pulling, e.g. by adding `image:` overrides in a compose override file.

Verify a release: `sha256sum -c SHA256SUMS`; `cosign verify <image>` when signing is enabled.
Versioning: semantic versions; database migrations are forward-only, so downgrades need a backup restore.
