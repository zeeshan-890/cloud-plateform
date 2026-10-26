# JP Cloud Platform — Release Engineering & Distribution

This guide describes the release process, semantic versioning policy, multi-architecture image packaging, CLI distribution, and security supply chain verification (SBOM & signing) for the JP Open-Source Self-Hosted Cloud Platform.

---

## 1. Versioning Policy

The platform strictly follows [Semantic Versioning 2.0.0](https://semver.org/):
- **MAJOR (`vX.0.0`)**: Incompatible API changes, breaking database migrations, or core architecture overhauls.
- **MINOR (`v0.X.0`)**: New features, new one-click apps, backwards-compatible API additions, or performance enhancements.
- **PATCH (`v0.0.X`)**: Backwards-compatible bug fixes, security patches, or documentation updates.

---

## 2. Multi-Architecture Container Builds

All service images are built for both `linux/amd64` (x86_64 servers) and `linux/arm64` (ARM / AWS Graviton / Apple Silicon / Raspberry Pi 5) using `docker buildx`.

### Building Service Images:
```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/jp-cloud/addon:v1.2.0 \
  -t ghcr.io/jp-cloud/addon:latest \
  -f services/addon/Dockerfile \
  --push .
```

### Base Image Standard
- **Builders**: `golang:1.22-alpine`
- **Runtimes**: `gcr.io/distroless/static-debian12:nonroot` or `alpine:3.19`
- All containers run as an unprivileged user (`UID 10001:10001`) with read-only root filesystems where applicable.

---

## 3. CLI Multi-Platform Distribution

The `jp` CLI is built for multiple operating systems and architectures using Go cross-compilation:

```bash
# Linux AMD64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X 'github.com/jp-cloud/cli/cmd.Version=v1.2.0'" -o dist/jp-linux-amd64 ./apps/cli

# Linux ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X 'github.com/jp-cloud/cli/cmd.Version=v1.2.0'" -o dist/jp-linux-arm64 ./apps/cli

# macOS AMD64 (Intel)
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X 'github.com/jp-cloud/cli/cmd.Version=v1.2.0'" -o dist/jp-darwin-amd64 ./apps/cli

# macOS ARM64 (Apple Silicon)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X 'github.com/jp-cloud/cli/cmd.Version=v1.2.0'" -o dist/jp-darwin-arm64 ./apps/cli

# Windows AMD64
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X 'github.com/jp-cloud/cli/cmd.Version=v1.2.0'" -o dist/jp-windows-amd64.exe ./apps/cli
```

Generate SHA256 checksums:
```bash
cd dist && sha256sum * > checksums.txt
```

---

## 4. Software Bill of Materials (SBOM) & Signing

Supply chain security is verified for every release:

### 1. Generating CycloneDX / SPDX SBOMs with Syft
```bash
syft ghcr.io/jp-cloud/addon:v1.2.0 -o spdx-json=addon-sbom.spdx.json
```

### 2. Container Image Signing with Cosign
```bash
cosign sign --key cosign.key ghcr.io/jp-cloud/addon:v1.2.0
```

Consumers can verify signatures using the public key:
```bash
cosign verify --key cosign.pub ghcr.io/jp-cloud/addon:v1.2.0
```

---

## 5. Release Checklist

Before tagging and releasing a new version:

- [ ] **Tests Pass**: Run all package tests (`go test -v ./...`).
- [ ] **Lint Clean**: Run `golangci-lint run`.
- [ ] **Database Migrations Verified**: Verify forward migration and rollback idempotency.
- [ ] **Docs Updated**: Ensure `docs/FEATURE_STATUS.md` and release notes match merged changes.
- [ ] **Tag Created**: `git tag -a v1.2.0 -m "Release v1.2.0"` and push tag `git push origin v1.2.0`.
- [ ] **Images Pushed**: Verify all 22 microservices and dashboard images exist on GHCR with proper tags.
- [ ] **Release Notes Published**: Publish changelog with highlights, upgrade instructions, and binary checksums.
