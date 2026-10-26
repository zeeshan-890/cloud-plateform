# JP Security Architecture & Policy Specification

> **Security, Cryptography, Identity, and Container Hardening Standards for JP.**

---

## 1. Identity & Access Control

- **Password Cryptography:** All passwords hashed with bcrypt (`cost = 10`) using `golang.org/x/crypto/bcrypt`.
- **JWT Architecture:** Dual-token model with HMAC-SHA256 (`HS256`):
  - Access Token: Short lifetime (`15m`). Contains `user_id`, `email`, `name`, `type = "access"`.
  - Refresh Token: Extended lifetime (`168h` / 7 days). Tracks `user_id` and random UUID `jti`.
- **Session Revocation:** Every refresh check verifies that the session row in PostgreSQL has `revoked_at IS NULL`.
- **Personal Access Tokens (PAT):** Tokens follow the format `jp_pat_<32_random_hex>`.
  - Displayed **once** upon generation.
  - Stored strictly as SHA-256 hashes (`token_hash`) with a visible prefix (`token_prefix`).
- **Multi-Tenant RBAC:**
  - Roles: `owner`, `admin`, `member`, `viewer`.
  - Every project endpoint checks membership in the parent organization before processing.

---

## 2. Secrets Encryption at Rest

- **Envelope Encryption:** Secrets encrypted using **AES-256-GCM** authenticated encryption with associated data.
- **Key Separation:** Managed via 32-byte `SECRETS_MASTER_KEY`.
- **Nonce Strategy:** A unique, cryptographically random 12-byte nonce is generated for every write operation using `crypto/rand`.
- **Zero-Plaintext Leak Rule:**
  - Plaintext values are never returned by API endpoints.
  - The API exposes only `value_hint` (e.g. `sk_l...4a9f`), version number, and timestamps.
  - Decryption occurs only inside the internal container runtime when injecting environment variables into container instances.

---

## 3. Host & Container Hardening

- **Docker Socket Isolation:** The Docker daemon socket (`/var/run/docker.sock`) is accessible **only** to the internal `worker` and `runtime` microservices over internal bridge networks. It is never exposed over TCP, to user applications, or to the public internet.
- **Non-Root Execution:** Control plane services execute as unprivileged system users inside containers.
- **Resource Constraints:** Every runtime container is launched with strict CPU limits and memory limits to prevent host denial of service.

---

## 4. Network Security & Ingress

- **Private by Default:** Databases, Redis, MinIO, and internal microservices listen strictly on private Docker networks (`jp`).
- **Controlled Exposure:** Only Traefik ports (`80`, `443`, `8080`) and local Docker registry (`5000`) are mapped to the host.
- **Webhook Signature Verification:** GitHub webhooks are verified using HMAC-SHA256 (`X-Hub-Signature-256`) against `GITHUB_WEBHOOK_SECRET`.
- **CORS Allowlist:** Cross-Origin Resource Sharing is restricted strictly to configured origins in `CORS_ORIGINS`.

---

## 5. Security Center (Planned Module 14)

- Integrated vulnerability scanning for container images.
- Outdated component detection.
- Misconfiguration and public database exposure alerts.
- Immutable security audit logging (`audit_logs` and `secret_audit`).
