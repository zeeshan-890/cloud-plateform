# Feature status

Every row names the Go test file(s) that back it. Statuses (only these are used):

* **Unit-tested** - behaviour exercised by the listed tests (fakes / `httptest`, no real Docker, DNS, GitHub or Let's Encrypt).
* **Implemented** - code exists but no automated test covers the claim yet. Treat as unverified.
* **Removed** - not provided (any earlier claim was withdrawn).

The **e2e** column is filled from the black-box suite in `e2e/` run against a deployed server (empty = no e2e test covers it yet). Last full run on a real server: 32/32 backend e2e tests and 40/40 UI Playwright tests passed.
Paths are relative to the repository root.

## Platform and operations

| Feature | Status | Verified by | e2e |
|---|---|---|---|
| 21 Go services + dashboard + CLI build in one workspace and standalone (`GOWORK=off`) | Implemented | CI jobs `go`, `images` | |
| Compose: required secrets enforced, loopback-only datastores, no insecure defaults | Implemented | CI job `compose` (config, grep, port check) | |
| Env contract (code vs compose vs `.env.example`) | Implemented | `scripts/verify-env.sh` (CI) | |
| Installer, firewall incl. Oracle REJECT rule, secret generation | Implemented | shellcheck / `bash -n` in CI | `TestSecurity_InfrastructurePortsNotPublic`; host isolation: `TestSecurity_TenantContainerCannotReachPlatform` |
| Backup / restore / upgrade with verification and rollback | Implemented | shellcheck only | |
| Config loading: production refuses weak `JWT_SECRET` / missing `INTERNAL_TOKEN` | Unit-tested | `packages/go-common/config/config_test.go` | |

## Identity, users and access

| Feature | Status | Verified by | e2e |
|---|---|---|---|
| Bootstrap first admin, registration modes, login, refresh, logout | Unit-tested | `services/identity/internal/handlers/handlers_test.go` | `TestAuth_BootstrapClosedAndRegistrationRejected`, `TestAuth_RefreshRotationAndLogout`, `TestAuth_LoginRateLimit`; UI `auth.spec.ts` |
| Admin-created users, generated one-time passwords, forced password change, session revocation | Unit-tested | `services/identity/internal/handlers/managed_test.go`, `services/organization/internal/handlers/users_test.go` | `TestUsers_RestrictedAccessAndIsolation`, `TestAuth_PasswordChangeRevokesSessions`; UI `access.spec.ts` |
| TOTP MFA | Unit-tested | `services/identity/internal/totp/totp_test.go`, `handlers_test.go` | `TestAuth_MFA`; UI `mfa.spec.ts` |
| Personal access tokens | Unit-tested | `services/identity/internal/handlers/handlers_test.go` | |
| JWT, token revocation, middleware | Unit-tested | `packages/go-common/jwt/jwt_test.go`, `revocation/revocation_test.go`, `middleware/middleware_test.go` | |
| Fixed-window rate limiting (Redis) | Unit-tested | `packages/go-common/ratelimit/ratelimit_test.go` | `TestAuth_LoginRateLimit` |
| Trusted-proxy client IP | Unit-tested | `packages/go-common/clientip/clientip_test.go` | |
| Internal-token protection of `/internal/*` | Unit-tested | `packages/go-common/internalauth/internalauth_test.go`, `httpx/httpx_test.go` | `TestSecurity_InternalEndpointsNotExposed` |
| Per-project area grants (fail-closed `RequireArea`), presets, secrets-reveal flag | Unit-tested | `packages/go-common/membership/membership_test.go`, `services/organization/internal/handlers/handlers_test.go` | `TestUsers_RestrictedAccessAndIsolation`; UI `access.spec.ts` |
| Area enforcement per service | Unit-tested | `services/{addon,ai,build,certificate,database,deployment,domain,registry,repository,runtime,scheduler,secret,storage}/internal/handlers/{authz,access}_test.go` | `TestUsers_RestrictedAccessAndIsolation`, `TestSecurity_AuthenticationRequiredEverywhere` |
| Organizations, invitations | Unit-tested | `services/organization/internal/handlers/handlers_test.go` | `TestOrgDelete_RemovesProjectsAndResources`; UI `navigation.spec.ts` |

## Projects, Git, builds, deployments

| Feature | Status | Verified by | e2e |
|---|---|---|---|
| Projects, `jp.yaml` desired state and drift | Unit-tested | `services/project/internal/handlers/handlers_test.go`, `packages/go-common/jpconfig/jpconfig_test.go` | `TestProjectDelete_CascadesEverywhere`; UI `workspace.spec.ts` |
| GitHub App client | Unit-tested | `services/repository/internal/githubapp/client_test.go` | |
| Repository connect, webhooks (per-repo secret), encrypted tokens | Unit-tested | `services/repository/internal/handlers/handlers_test.go`, `store/store_test.go`, `secbox/secbox_test.go` | `TestGit_ConnectValidationRejectsDangerousURLs`, `TestGit_PublicRepoBuildDeployWebhook` |
| Continuous deployment: root directory (monorepo), auto-deploy, production branch, path filters, `[skip ci]`, wait for CI, per-connection webhooks, webhook install/rotate, folder browser | Unit-tested | `services/repository/internal/cd/cd_test.go`, `handlers/cd_test.go`, `services/worker/internal/worker/worker_test.go` | `apps/dashboard/e2e-ui/cd.spec.ts` (real stack). Not built: GitLab/Bitbucket, tag deploys, approval gates. See [CONTINUOUS_DEPLOYMENT.md](CONTINUOUS_DEPLOYMENT.md) |
| Git clone safety (allow-list, no option injection, no token in URL) | Unit-tested | `services/worker/internal/gitx/gitx_test.go` | `TestGit_PublicRepoBuildDeployWebhook` |
| Build records / logs | Unit-tested | `services/build/internal/handlers/handlers_test.go` | |
| Worker pipeline, Dockerfile generation, `jp.yaml` build config, log redaction | Unit-tested | `services/worker/internal/worker/worker_test.go`, `dockerfile/dockerfile_test.go`, `jpyaml/jpyaml_test.go`, `redact/redact_test.go` | `TestGit_PublicRepoBuildDeployWebhook` |
| Trivy image scan (statuses disabled / passed / failed) | Unit-tested | `services/worker/internal/scan/trivy_test.go` | |
| Deployments, rollback, previews (real PR head / branch) | Unit-tested | `services/deployment/internal/handlers/handlers_test.go` | `TestDeploy_RollbackToPreviousImage`, `TestDeploy_StopStart` |
| Rolling / blue-green with health gate, automatic keep-old on failure | Unit-tested | `services/runtime/internal/manager/manager_test.go`, `handlers/handlers_test.go` | `TestDeploy_RollingRedeployZeroDowntime`, `TestDeploy_FailedVersionKeepsOldServing` |
| Docker API client and tenant container hardening | Unit-tested | `services/runtime/internal/dockerx/dockerx_test.go` | `TestDeploy_ContainerHardeningAndRouting`, `TestSecurity_TenantContainerCannotReachPlatform` |
| Scheduler: cron, cleanup loop | Unit-tested | `services/scheduler/internal/cron/parse_test.go`, `loop/loop_test.go`, `handlers/handlers_test.go` | |
| Registry API (list, delete, garbage-collect refs) | Unit-tested | `services/registry/internal/handlers/handlers_test.go` | |

## Edge, domains, secrets

| Feature | Status | Verified by | e2e |
|---|---|---|---|
| Hostname validation | Unit-tested | `services/domain/internal/hostname/hostname_test.go` | `TestDomains_HostnameValidation` |
| DNS verification (TXT / CNAME / A), no force | Unit-tested | `services/domain/internal/dnscheck/dnscheck_test.go`, `handlers/handlers_test.go` | `TestDomains_CustomDomainDNSVerification` |
| Traefik router generation (file provider, atomic) | Unit-tested | `services/domain/internal/traefikcfg/traefikcfg_test.go` | `TestDomains_SystemSubdomainRoutes`, `TestDeploy_RollingRedeployZeroDowntime` |
| Let's Encrypt (Traefik HTTP-01), certificate status | Unit-tested | `services/certificate/internal/prober/prober_test.go`, `handlers/handlers_test.go` (issuance itself needs public DNS) | |
| Encrypted secrets, rotation, env merge, preview isolation | Unit-tested | `services/secret/internal/crypto/envelope_test.go`, `handlers/handlers_test.go`, `packages/go-common/secretbox/secretbox_test.go` | `TestSecrets_Lifecycle`, `TestSecrets_InjectionAndEnvironmentIsolation` |

## Data and operations services

| Feature | Status | Verified by | e2e |
|---|---|---|---|
| Add-on catalog (241 entries, 22 provisionable), specs, credentials, readiness | Unit-tested | `services/addon/internal/catalog/catalog_test.go`, `driver/driver_test.go`, `service/service_test.go`, `handlers/handlers_test.go` | `TestAddons_RealEnginesReachableFromApp` (5 real engines from a Node app) |
| Managed databases (dedicated container), diagnostics | Unit-tested | `services/database/internal/handlers/handlers_test.go`, `diagnostics/diagnostics_test.go` | `TestDatabases_ManagedPostgres_ConnectBackupRestore` |
| Storage buckets, quotas, backups | Unit-tested | `services/storage/internal/handlers/handlers_test.go`, `backup/backup_test.go` | `TestStorage_BucketObjectsAndIsolation` |
| Logging / metrics | Unit-tested | `services/logging/internal/handlers/handlers_test.go`, `services/metrics/internal/handlers/handlers_test.go` | `TestResources_NoLeaksNoRestartsUnderLoad` |
| Notifications (SMTP) | Unit-tested | `services/notification/internal/handlers/handlers_test.go` | |
| AI explanations (OpenAI-compatible, Gemini, OpenRouter), key encryption, redaction | Unit-tested | `services/ai/internal/llm/llm_test.go`, `crypt/crypt_test.go`, `redact/redact_test.go`, `handlers/handlers_test.go` | |
| Server inventory and one-command join (Cluster page): join token + script, firewall opening, peer connectivity tests, plain-language diagnosis (blocked port, closed port, private-network mismatch), configuration and on-demand checks only (no resident agent) | Unit-tested | `services/gateway/internal/gateway/servers_test.go`, `join_test.go` | `apps/dashboard/e2e-ui/servers.spec.ts`; run for real on two Oracle servers (clean join, port blocked by Oracle NSG, closed port, other private network, all green) |
| Project placement on other servers: node agent, remote runtime (fixed private-IP ports, per-server reconcile/delete, fail-closed), upstream routes, join command with node mode, project "Runs on" setting | Unit-tested | `services/node/internal/agent/agent_test.go`, `services/runtime/internal/manager/nodes_test.go`, `services/runtime/internal/handlers/nodes_test.go`, `services/domain/internal/traefikcfg/upstreams_test.go`, `services/gateway/internal/gateway/join_test.go` | `e2e/multiserver_test.go` (real second server: deploy, edge routing, remote add-on, move guard, 0 failures in 1,013 requests during a rolling redeploy, delete cleanup); UI `servers.spec.ts` |
| Gateway routing, error shape, platform admin pages | Unit-tested | `services/gateway/internal/gateway/gateway_test.go`, `proxy/proxy_test.go`, `static/static_test.go` | `TestSystem_AdminEndpoints`, `TestSecurity_NoStackTraceOrSecretsInErrors`, `TestSecurity_BodyLimits`; UI `ui.spec.ts` |
| CLI API client | Unit-tested | `apps/cli/internal/api/client_test.go`, `openapi_test.go` | |
| Events (Redis Streams) | Unit-tested | `packages/events/events_test.go` | |

## Not provided

| Claim | Status |
|---|---|
| Billing, plans, usage metering | Removed (self-hosted; no billing) |
| Automatic scheduling, replicas across servers, load balancing, control-plane HA | Not implemented (projects are placed on one server each; see [MULTI_SERVER.md](MULTI_SERVER.md) and [ROADMAP.md](ROADMAP.md)) |
| "Lite" profile for 1-2 GB hosts | Removed (minimum 4 GB) |
| Simulated builds / runtime / storage / DNS / certificates as a fallback | Removed from production paths |
| Domain verification override (`force`) | Removed |
| 219 of 241 catalog entries as one-click add-ons | Not provisionable (reasons in [ADDONS.md](ADDONS.md)) |
| Payment processing, SSO, HA | Not implemented |
