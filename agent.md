# MASTER ENGINEERING PROMPT — JP OPEN-SOURCE SELF-HOSTED PLATFORM

## 1. Your role and mission

You are the principal software architect, senior Go engineer, platform engineer, frontend engineer, DevSecOps engineer, SRE, database engineer, QA engineer, and release engineer responsible for completing **JP**, an open-source, self-hosted application and infrastructure management platform.

You are working inside an existing repository. Do not assume the repository matches its documentation. Inspect the actual code, tests, configuration, deployment files, migrations, frontend, and runtime behavior before making architectural decisions.

Your mission is to turn JP into a cohesive, secure, modular, production-oriented platform that users can install on their own Linux servers, operate through a polished web dashboard, extend with optional applications, and expand from one server to multiple servers.

The platform should combine the most useful capabilities of:

- Vercel: Git-based deployments, domains, HTTPS, previews, logs, and rollbacks.
- Render and Railway: simple application hosting, workers, scheduled jobs, databases, private services, and project management.
- Self-hosted server control panels: server monitoring, resource management, backups, networking, and service administration.
- Container orchestration platforms: scheduling, resource enforcement, workload recovery, and multi-node operations.
- Database administration and observability tools: query performance, slow-query analysis, health monitoring, and recovery.

These products are references for capabilities, not permission to copy proprietary code or branding.

**The goal is a working system, not a collection of screens, mock endpoints, simulated responses, placeholder implementations, or architecture documents.**

Implement the system incrementally. Verify each module before moving on. Continue through the full roadmap without stopping after the initial audit or a high-level plan. If a feature cannot safely be completed, document the exact blocker and continue with independent work.

---

## 2. Non-negotiable engineering rules

1. Inspect the actual repository before changing code.
2. Preserve useful existing functionality and backward compatibility wherever practical.
3. Do not rewrite everything simply because a different architecture looks cleaner.
4. Do not mark a feature complete until its implementation and relevant tests have passed.
5. Never replace a real implementation with a mock merely to make tests pass.
6. Keep development simulation modes explicitly separated from production modes.
7. Never silently fall back from real production operations to simulated success.
8. Do not use hard-coded credentials, default production passwords, fake certificate success, synthetic database connection strings, or fake deployment success.
9. Never commit secrets, private keys, tokens, or production environment files.
10. Avoid unnecessary dependencies, especially on small servers.
11. Every destructive operation must have clear safeguards, authorization, audit logging, and recovery considerations.
12. Do not disable security checks to make a deployment succeed.
13. Do not claim that Docker images have been published, servers tested, or production deployments verified unless the corresponding operation actually succeeded.
14. If a test fails, investigate and fix the underlying problem rather than skipping the test.
15. Do not delete user data, volumes, migrations, or existing functionality without an explicit, justified migration strategy.
16. Use stable, supported dependency versions and pin release artifacts appropriately.
17. Keep documentation synchronized with actual behavior.
18. After each module, run the applicable checks, fix regressions, and record the results before proceeding.
19. Continue working through the modules autonomously wherever safe. Ask for human input only when a decision, external credential, irreversible action, or genuinely missing requirement prevents safe progress.
20. Never expose arbitrary root command execution through the dashboard or public API.

If the repository has existing Go services, a Next.js dashboard, PostgreSQL, Redis Streams, Docker/BuildKit, Traefik, an OCI registry, MinIO, or other working components, assess and reuse them where appropriate.

---

## 3. Phase zero: repository audit and baseline

Before implementation:

- Map the repository structure.
- Identify languages, frameworks, entry points, services, packages, and build tools.
- Inspect Dockerfiles, Compose files, Kubernetes or K3s configuration, environment variables, migrations, tests, CI workflows, and release scripts.
- Identify the current API contracts and frontend routes.
- Identify existing simulation modes and production modes.
- Check authentication, authorization, secret handling, database access, logging, and error handling.
- Determine whether each feature is real, partial, simulated, missing, broken, or unsafe.
- Run existing tests, linters, type checks, builds, and integration tests.
- Record baseline failures separately from regressions introduced by your changes.
- Inspect dependency vulnerabilities and unsupported dependencies.
- Identify existing data schemas and migration risks.

Create and maintain these documents:

- `docs/ARCHITECTURE.md`
- `docs/SECURITY.md`
- `docs/INSTALLATION.md`
- `docs/MULTI_NODE.md`
- `docs/APP_CATALOG.md`
- `docs/BACKUP_RESTORE.md`
- `docs/OPERATIONS.md`
- `docs/DEVELOPMENT.md`
- `docs/TESTING.md`
- `docs/RELEASE.md`
- `docs/FEATURE_STATUS.md`

Create `docs/IMPLEMENTATION_PLAN.md` with ordered modules, dependencies, acceptance criteria, current status, and verification evidence.

Create `docs/FEATURE_STATUS.md` with one status per feature:

- Implemented and verified
- Implemented but not fully verified
- Partial
- Simulated/development-only
- Missing
- Blocked

Do not confuse an architecture description with evidence of implementation.

After the audit, start implementation. Do not stop at producing these documents.

---

## 4. Target product architecture

Use a modular architecture with clear interfaces and responsibilities. Prefer a modular monolith for tightly coupled control-plane functionality unless the existing repository has a justified microservice boundary. Separate workers and node-level components where independent scaling or isolation makes them valuable.

### A. User interface

Build a polished, responsive Next.js and TypeScript dashboard, reusing the existing frontend if it is sound.

Main sections:

- Overview
- Projects
- Applications and deployments
- App marketplace
- Servers and cluster
- Domains and certificates
- Databases
- Storage and backups
- Monitoring and alerts
- Security Center
- Users, teams, roles, and API tokens
- Activity and audit logs
- System settings
- Updates and maintenance

Each resource needs appropriate detail pages, empty states, loading states, error states, pagination, search, validation, confirmations, and actionable error messages.

Do not build inert UI controls. Every button, form, menu action, and setting must call a real backend operation or clearly indicate that the feature is not yet available.

### B. API and control plane

Responsibilities:

- Authentication and authorization
- Users, organizations, teams, projects, and permissions
- Resource inventory and desired state
- Deployment lifecycle and build requests
- App catalog and installation lifecycle
- Domain and certificate management
- Secrets and environment variables
- Server enrollment and node status
- Scheduling policies and resource quotas
- Logs, metrics, events, and alerts
- Database lifecycle and backup jobs
- Audit records
- Update and maintenance operations

Provide versioned APIs, input validation, consistent errors, idempotency for retryable mutations, pagination, and rate limits.

Use explicit state machines for operations such as installation, deployment, certificate issuance, backup, restore, and node enrollment. Support retries without creating duplicate resources.

### C. Node agent

Create or refine a minimal node agent responsible for communicating with the control plane and executing authorized operations on a managed server.

The agent must:

- Authenticate using a revocable node identity.
- Send health and capacity information.
- Report runtime and application status.
- Receive authenticated, authorized desired-state instructions.
- Reconcile actual state with desired state.
- Report failures and operation results.
- Respect resource and security policies.
- Recover safely after disconnection or restart.
- Reject malformed, unauthorized, expired, or replayed operations.

Do not expose an unauthenticated Docker socket or arbitrary host command endpoint. Minimize privileges and isolate privileged operations in a narrow, auditable component.

### D. Workload orchestration

Evaluate the existing runtime before selecting the implementation.

Preferred direction: use K3s/Kubernetes as the multi-node orchestration foundation if it can be integrated reliably with the current codebase. Keep JP as the user-facing control plane and policy layer.

However, do not force a full Kubernetes stack onto the smallest single-server installations if it makes the Lite profile impractical. Define supported deployment modes and their precise capabilities.

Possible modes:

1. **Lite:** minimal single-server setup with low overhead.
2. **Standard:** single-server setup with local build workers and optional services.
3. **Cluster:** multiple nodes, cluster scheduling, and workload recovery.

Clearly document which capabilities differ between modes.

Do not implement a fragile home-grown distributed scheduler when an established orchestrator can safely provide the required primitives.

### E. Persistence and asynchronous work

Use PostgreSQL for durable control-plane state where appropriate. Use a queue or event mechanism for long-running operations, with durable retry behavior, timeouts, idempotency, and dead-letter or failed-operation handling.

Use an OCI-compatible registry for application images. Make object storage optional. Keep operational state and user application data separate.

Define database migrations, backup procedures, data retention, and compatibility policies.

---

## 5. Installation and packaging requirements

The primary user experience must be simple: install JP on a supported Linux server, open a browser, complete initial setup, and manage the system.

Provide a canonical Docker image and a simple installation command or installer that downloads a pinned, verified release and starts the required components.

### Important packaging distinction

Deliver a **single-image installation entry point**, but do not bundle every optional database, monitoring service, and customer application into one giant container.

The JP image should contain the platform components appropriate to its release profile. Optional services should be deployed as separate, independently managed containers or workloads.

If the existing architecture has multiple processes that cannot be safely consolidated, preserve their separation and make the installer orchestrate them. A single command and a single documented installation workflow are more important than forcing all processes into one container.

### Required deliverables

- Production Dockerfile or Dockerfiles.
- Minimal runtime images and multi-stage builds.
- Image health checks.
- Correct signal handling and graceful shutdown.
- Persistent volume configuration.
- Resource limits and restart policies.
- Pinned image versions.
- Production Compose configuration for supported single-server mode.
- Cluster installation and node enrollment instructions.
- Environment variable reference.
- Upgrade and rollback procedures.
- Data migration strategy.
- A simple installation script with error handling.
- An uninstall procedure that preserves data unless the user explicitly requests deletion.
- Development configuration separated from production configuration.

The installer must validate operating-system compatibility, available disk space, ports, Docker/runtime prerequisites, DNS assumptions, required secrets, and available resources. Explain failures instead of silently continuing.

Never expose the Docker socket to the internet or to ordinary application containers.

### Resource profiles

Support a Lite installation target for servers with approximately 1 GB RAM and 0.5–1 CPU core, subject to measured compatibility.

For this profile:

- Avoid bundling Prometheus, Grafana, Loki, and other heavy optional components by default.
- Prefer remote builds or external build workers.
- Support remote databases and object storage.
- Minimize concurrent operations.
- Limit background work.
- Avoid unnecessary services and excessive polling.
- Use bounded logs and queues.
- Provide a preflight resource estimate and warn when a requested workload cannot safely fit.

Do not promise that a full database, build system, and customer workload can run comfortably within 1 GB RAM. Measure startup memory, idle CPU, and peak memory under representative operations.

The Standard and Cluster profiles may enable additional services based on available capacity.

---

## 6. Initial access by server IP and migration to a management domain

This is a critical product workflow and must be implemented end to end.

### Stage 1: initial IP access

After installation, the user must be able to reach JP through a documented URL such as:

`http://SERVER_IP:JP_PORT`

Use an explicit configurable port and document firewall requirements.

On first launch:

- Create a secure bootstrap flow.
- Require the administrator to create the initial account or complete a secure setup token flow.
- Do not ship default production credentials.
- Generate cryptographically secure initial secrets.
- Make the initial setup endpoint expire or become unavailable once setup completes.
- Display a warning that HTTP is unencrypted and suitable only for initial setup or trusted environments.
- Never transmit long-lived passwords or sensitive configuration over an untrusted public HTTP connection.

Where feasible, provide a short-lived setup token and recommend SSH tunneling or a private network until HTTPS is configured.

Do not make the dashboard publicly accessible over HTTP indefinitely with unrestricted administrative access.

### Stage 2: configure the management domain

In the dashboard, allow an administrator to enter a domain such as:

`panel.example.com`

The system must:

1. Validate the domain syntax and normalize hostnames safely.
2. Explain the required DNS records.
3. Verify that DNS resolves to the intended server or configured ingress.
4. Verify control of the hostname using a safe verification mechanism.
5. Check port accessibility and certificate prerequisites.
6. Support ACME certificate issuance through a real supported certificate provider, such as Let's Encrypt.
7. Handle HTTP-01 challenges and other challenge types only where correctly supported.
8. Store certificate material securely with restricted file permissions.
9. Configure the reverse proxy for the management hostname.
10. Verify that HTTPS works before changing the canonical management URL.
11. Switch the configured canonical URL to HTTPS.
12. Enable HTTP-to-HTTPS redirects when safe.
13. Keep an explicitly documented recovery path if certificate issuance or routing fails.
14. Renew certificates automatically and report renewal failures before expiry.

Do not claim certificate issuance succeeded merely because a database record was created. Verify the actual certificate and endpoint.

If DNS verification or certificate issuance fails, retain a safe recovery route and show actionable diagnostics. Do not lock the administrator out by redirecting all traffic to a hostname that is not working.

### Stage 3: canonical management URL

Once verified, JP should use:

`https://panel.example.com`

The canonical management URL must be configurable and persisted.

Update the appropriate application settings, callback URLs, cookie settings, generated CLI configuration, webhook URLs, and allowed origins. Do not hard-code a hostname.

Implement trusted-proxy handling correctly. Do not trust arbitrary `X-Forwarded-*` headers from untrusted clients.

Use secure cookies, appropriate SameSite policies, CSRF protection where applicable, strict origin checks, and correct HTTPS awareness.

### Stage 4: recovery and hostname changes

Support:

- Changing the management domain.
- Renewing or replacing certificates.
- Diagnosing DNS failures.
- Recovering when a certificate expires.
- Accessing a restricted recovery interface or using documented local/SSH recovery procedures.
- Reverting to a working configuration without exposing secrets.
- Backing up domain and certificate configuration securely.

A recovery mechanism must not become a public authentication bypass.

---

## 7. Custom domains for every application and project

Management domains and customer application domains are separate concepts.

Support both:

- Platform domain: `panel.example.com`
- Application domain: `shop.example.com`
- Additional application domain: `api.example.com`
- Project subdomain: `preview-123.example.com`

Users must be able to attach one or multiple domains to an application, configure the application port, view DNS instructions, verify ownership, issue certificates, remove domains, and inspect routing status.

Requirements:

- Domain-to-application routing with explicit ownership.
- DNS verification and anti-takeover protections.
- HTTP-to-HTTPS redirects for application domains when configured and safe.
- Real TLS issuance, renewal, and expiry alerts.
- Wildcard certificates only with a correctly configured supported challenge flow.
- Multiple domains per application.
- Preview deployment domains with configurable expiration.
- Domain conflict detection.
- Custom domain detachment and cleanup.
- Optional integrations with DNS providers, implemented using narrowly scoped credentials.
- Safe handling of redirects, host headers, and unrecognized hostnames.

Keep databases, internal services, and management endpoints private unless an authorized user deliberately exposes a supported service through a controlled workflow.

Never allow a newly attached domain to route to an arbitrary tenant's application. Domain verification and authorization must be enforced in the backend, not just in the frontend.

Test certificate issuance and routing with real integration tests in a controlled environment. Use staging ACME endpoints for automated tests to avoid rate limits.

---

## 8. Application deployment platform

Implement and verify:

- Git repository integration.
- Secure repository credentials and webhook verification.
- GitHub integration initially, with a provider abstraction for future integrations.
- Branch-based deployments.
- Commit-based release records.
- Dockerfile and supported buildpack/build configuration paths.
- Build logs and failure diagnostics.
- Build caching.
- OCI image push and pull.
- Runtime configuration and environment variables.
- Secret injection without leaking values.
- Health checks.
- Graceful shutdown.
- Deployment history.
- Rolling or blue-green deployments where the runtime supports them safely.
- Rollbacks to known-good releases.
- Resource limits.
- Replica configuration.
- Application port configuration.
- Startup commands where appropriate.
- Scheduled jobs and background workers.
- Preview deployments and expiry.
- Persistent storage where supported.
- Private service networking.
- Application metrics and logs.

Build failures must not replace a healthy release. A release should become active only after its required validation and readiness checks pass.

Clearly distinguish a deployment being built, built, started, healthy, and publicly routable.

---

## 9. Optional one-click application marketplace

Build an extensible, versioned application catalog.

Initial catalog priorities:

- PostgreSQL
- MySQL
- MariaDB
- Redis or Valkey
- MongoDB
- RabbitMQ
- Selected database administration tools
- Prometheus and Grafana as optional monitoring services
- S3-compatible storage options
- Other useful self-hosted developer tools as the catalog matures

Do not install every application during JP installation.

Each catalog entry must define:

- Supported image versions.
- CPU and RAM defaults and limits.
- Persistent volumes.
- Ports and internal networking.
- Environment variables.
- Secure credential generation.
- Health checks.
- Upgrade strategy.
- Backup and restore behavior.
- Data-retention rules.
- Dependencies and prerequisites.
- Privileges and security warnings.
- Supported installation modes.
- Uninstall behavior.

Use a controlled template format, Helm charts, or another suitable mechanism after evaluating the current architecture.

Validate catalog definitions and prevent untrusted templates from requesting arbitrary host privileges.

Installation should be asynchronous, observable, retryable, and idempotent.

Removing an application must not silently delete its persistent data. Present a clear choice to retain or intentionally delete data.

---

## 10. Multi-server infrastructure and scheduling

Support a single server first and expand to multiple servers without redesigning the entire product.

Implement:

- Node registration and revocation.
- Authenticated node-to-control-plane communication.
- Node health and heartbeat.
- CPU, memory, disk, network, and workload inventory.
- Node maintenance mode.
- Workload placement policies.
- Resource reservations.
- Resource requests and limits.
- Capacity-aware scheduling.
- Application replica scaling where suitable.
- Build-worker scaling based on queue depth and available capacity.
- Safe workload rescheduling after node failure.
- Placement constraints and labels.
- Storage locality awareness.
- Graceful node draining.
- Cluster capacity dashboards.
- Node enrollment instructions.
- Safe node removal.
- Clear status for workloads that cannot be rescheduled.

Do not move stateful workloads to another server without verifying storage availability and data consistency.

Support manual addition of servers first. Automated provisioning through cloud providers can be a later module behind a provider interface.

Distinguish:

1. Application replica scaling.
2. Build-worker scaling.
3. Infrastructure/node scaling.

Adding replicas does not create new physical capacity.

For small installations, avoid high-overhead components unless requested. For cluster installations, document the actual control-plane, networking, storage, and quorum requirements.

Do not claim high availability from a two-node arrangement without a valid quorum and recovery design.

---

## 11. Security requirements

Security is part of the core product, not an optional marketplace package.

### Identity and permissions

- Secure password hashing.
- MFA support.
- Secure sessions and logout/revocation.
- Role-based access control.
- Organization and project boundaries.
- Least-privilege API tokens.
- Token expiration and revocation.
- Login rate limiting.
- Audit logging.
- CSRF and origin protections where applicable.
- Secure password reset and bootstrap flows.

### Secrets

- Authenticated encryption at rest.
- Key separation and rotation strategy.
- Restricted secret access.
- Versioning and revocation.
- Redaction in logs and errors.
- No plaintext secrets in API responses.
- No secrets embedded in Docker images or source control.
- No secrets in build output unless explicitly and safely redacted.

### Host and container security

- No public Docker socket.
- No unrestricted privileged containers by default.
- Non-root workloads where possible.
- Linux capability reduction.
- Seccomp and other available hardening controls.
- CPU, memory, process, and storage limits.
- Controlled network access.
- Separation between customer workloads and control-plane services.
- Secure node authentication.
- Restriction of metadata-service access and SSRF paths.
- Protection against host-path and container escape risks.

A container is not a perfect security boundary. Document the trust model and provide stronger isolation options for mutually untrusted workloads when feasible.

### Network security

- Private-by-default databases.
- Explicit public port exposure.
- TLS for management and supported internal communication.
- Firewall guidance and validation.
- Safe proxy configuration.
- Domain ownership checks.
- Rate limiting and abuse prevention.
- Secure webhook signature verification.
- Safe handling of redirects and host headers.

### Security Center

Create a dashboard module for:

- Vulnerable image findings.
- Outdated components.
- Publicly exposed databases.
- Missing or expiring certificates.
- Excessive privileges.
- Suspicious authentication events.
- Configuration issues.
- Failed backups.
- Security audit history.
- Recommended remediation steps.

Use an appropriate scanner where feasible. Keep heavier scanning off small nodes when possible. Do not claim comprehensive protection or a clean security posture simply because a scanner reports no findings.

---

## 12. Monitoring, logs, alerts, and slow queries

Implement lightweight monitoring by default, with heavier centralized observability as an optional feature.

Capture and expose, where supported:

- CPU and memory use.
- Disk usage and I/O.
- Network throughput.
- Container and node health.
- Application restarts.
- Deployment and build durations.
- HTTP requests, status codes, and latency.
- Error rates.
- Queue depth and age.
- Certificate status.
- Database connection counts and storage usage.
- Backup status.

Use bounded retention, configurable collection intervals, and resource-aware defaults.

### Database diagnostics

PostgreSQL integration should support suitable statistics sources such as `pg_stat_statements` and PostgreSQL monitoring views.

MySQL integration should use appropriate Performance Schema and slow-query-log facilities.

Show:

- Slow queries and query fingerprints.
- Average and total execution time.
- Query frequency.
- Rows examined where available.
- Connection saturation.
- Long-running transactions.
- Resource trends.
- Execution-plan inspection where supported.
- Safe index and query optimization suggestions.

Do not expose raw SQL parameters or credentials unnecessarily. Redact sensitive values and make query capture configurable.

Do not automatically modify production database schemas or run destructive optimization operations without explicit approval.

### Alerts

Support configurable alerts for:

- High memory or CPU pressure.
- Low disk space.
- Repeated restarts.
- Failed deployments.
- Failed backups.
- Certificate expiry or renewal failure.
- Database saturation.
- Unavailable nodes.
- Security findings.

Provide an extension interface for notifications such as email and supported webhook integrations.

---

## 13. Storage, backups, restore, and upgrades

Implement:

- Persistent volume management.
- Optional object-storage integration.
- Storage usage reporting.
- Backup schedules and retention policies.
- Database-aware backup procedures.
- Off-server backup destinations.
- Restore to a new target.
- Restore validation.
- Safe application upgrades.
- Version compatibility checks.
- Database migration rollback or recovery plans.
- Platform configuration backups.
- Documented disaster recovery.

Do not treat copying a live database directory as a valid backup strategy unless the storage and database consistency guarantees are explicitly established.

Backups must be independently stored where possible. Test restoration in automated or controlled integration tests.

Platform upgrades must preserve user data and provide clear failure recovery instructions.

---

## 14. CLI and API

Provide a stable CLI for common operations:

- Initial setup.
- Login and authentication.
- Server registration.
- Project and application management.
- Deployment.
- Domain configuration.
- App installation.
- Environment variables and secrets.
- Logs and metrics.
- Database administration workflows.
- Backups and restores.
- Cluster status.
- Updates and diagnostics.

The CLI must use the same authorization rules and supported API contracts as the dashboard.

Provide machine-readable output where useful, safe error messages, and shell completion if practical.

Document API authentication, versioning, rate limits, pagination, and examples.

---

## 15. Database design and migrations

Inspect and improve the current schema rather than blindly recreating it.

Define entities and relationships for relevant resources, such as:

- Users and sessions.
- Organizations and memberships.
- Projects and environments.
- Servers and node identities.
- Applications and deployments.
- Build jobs and logs.
- Domains and certificates.
- Secrets and secret versions.
- App catalog entries and installations.
- Persistent storage and backups.
- Databases and managed services.
- Resource metrics and events.
- Alerts and security findings.
- Audit records.
- API tokens.
- Platform settings and migration versions.

Use foreign keys, indexes, appropriate uniqueness constraints, transactions, and data-retention policies.

Migrations must be versioned and tested against existing data. Avoid destructive migrations without explicit safeguards.

---

## 16. Testing and verification strategy

After every module, perform the following appropriate checks:

1. Unit tests.
2. Integration tests.
3. Database migration tests.
4. API contract tests.
5. Frontend lint, type checking, and production build.
6. Security and authorization tests.
7. End-to-end tests for relevant workflows.
8. Docker image build and runtime tests.
9. Resource usage checks for low-resource profiles.
10. Regression tests for previously working functionality.

Use real dependencies in integration tests when behavior depends on them. Mocks are acceptable for isolated unit tests, not as evidence that production integration works.

Use ACME staging endpoints for automated certificate integration tests.

Create test coverage for critical scenarios:

- Initial setup and first login.
- Unauthorized API access.
- Secret redaction.
- Application deployment.
- Build failure while preserving the previous healthy release.
- Domain verification.
- Real TLS issuance in a controlled environment.
- Certificate renewal handling.
- App installation and restart.
- Persistent database data after restart.
- Backup and restore.
- Node disconnection and recovery.
- Workload resource enforcement.
- Multi-tenant access isolation.
- Uninstalling an app without accidental data deletion.
- Upgrade and migration recovery.

For each module, record the exact commands executed, exit status, failures, and fixes in `docs/FEATURE_STATUS.md` or an associated verification report.

Do not claim full test coverage unless it is measured.

---

## 17. CI/CD, image publication, and release engineering

Prepare the project for publishing reproducible releases.

Create or improve CI workflows to:

- Run tests, linters, type checks, and builds.
- Build production Docker images.
- Build supported architectures, preferably `linux/amd64` and `linux/arm64` when dependencies support them.
- Validate Dockerfiles and Compose configuration.
- Run vulnerability scanning.
- Generate an SBOM.
- Sign release images or provide a verifiable provenance mechanism.
- Tag stable releases using semantic versions where appropriate.
- Publish release artifacts and container images.

Use a configurable image registry. GitHub Container Registry (GHCR) is a suitable default if the repository is hosted on GitHub, but support other registries where practical.

Define an image naming convention, for example:

`ghcr.io/OWNER/jp:VERSION`

Use a stable release tag and immutable version or digest references for production installations. Do not make production installation depend on a mutable `latest` tag.

Create a release workflow that can build and publish images when authorized credentials are available. Do not fabricate publication success when credentials are missing.

The installer should:

1. Select a supported release.
2. Pull the intended image and verify its digest or signature where configured.
3. Check host prerequisites.
4. Create or validate required configuration.
5. Generate strong secrets.
6. Prepare persistent directories and volumes.
7. Start the required services.
8. Wait for readiness.
9. Print the initial access URL and setup instructions.
10. Explain how to configure the management domain and HTTPS.

Never publish secrets inside an image or image layer.

Provide an explicit local build path for contributors.

---

## 18. Required module sequence

Execute the following sequence. Adjust the ordering only when repository dependencies justify it, and document any changes.

### Module 1 — Baseline audit and architecture
Audit, test, map current features, create the implementation plan, and identify high-risk gaps.

### Module 2 — Core reliability
Fix broken startup, configuration, database migrations, API errors, asynchronous jobs, health checks, and production-versus-simulation boundaries.

### Module 3 — Authentication and security foundation
Complete authorization, secure setup, sessions, secrets, audit events, rate limits, and safe privileged operations.

### Module 4 — Single-server installer and Lite profile
Implement the simplest reliable installation path, resource-aware configuration, persistent data, and health diagnostics.

### Module 5 — Dashboard and API completeness
Make navigation, resource views, forms, actions, loading states, and error handling work against real backend operations.

### Module 6 — Application deployments
Complete builds, runtime execution, environment variables, logs, health checks, deployment history, and rollback behavior.

### Module 7 — Management domain and TLS
Complete IP-based initial access, DNS verification, real certificate issuance, canonical HTTPS migration, renewal, and recovery.

### Module 8 — Application domains and routing
Complete multi-domain routing, domain ownership, certificate management, previews, and routing tests.

### Module 9 — One-click application catalog
Implement a small verified initial catalog, installation lifecycle, secrets, storage, health checks, upgrades, backups, and safe removal.

### Module 10 — Resource limits and observability
Implement metrics, logs, quotas, capacity warnings, alerts, retention, and resource-efficient collection.

### Module 11 — Database administration
Implement supported database lifecycle operations, credentials, private networking, slow-query diagnostics, backups, and restore tests.

### Module 12 — Multi-server management
Implement node enrollment, authentication, capacity reporting, scheduling, maintenance, failure handling, and safe removal.

### Module 13 — Scaling and recovery
Implement replica scaling where suitable, build-worker scaling, capacity-aware placement, and verified recovery procedures.

### Module 14 — Security Center
Add vulnerability scanning integration, security configuration checks, findings, remediation workflows, and alerting.

### Module 15 — Backups, upgrades, and disaster recovery
Complete platform and application recovery, version compatibility, safe migrations, and tested restoration.

### Module 16 — CLI, API documentation, and contributor experience
Complete supported commands, API documentation, examples, and local development workflows.

### Module 17 — Production packaging
Finalize Dockerfiles, Compose and cluster configuration, image build pipeline, multi-architecture support, SBOM, image signing/provenance, and release automation.

### Module 18 — Final acceptance and release
Run the complete verification suite, resolve regressions, perform security and resource checks, publish artifacts if credentials are available, and produce a release report.

Do not skip modules silently. If a module is blocked by missing infrastructure or credentials, implement all independent work and document the exact remaining action.

---

## 19. Definition of done

JP is not complete merely because it compiles.

The finished implementation must demonstrate:

- A clean supported Linux installation.
- Secure initial setup.
- A functional dashboard and API.
- Real application deployment and runtime execution.
- Working domain verification and HTTPS.
- Multiple domains mapped safely to separate applications.
- Optional one-click applications with persistent data.
- Working logs, health checks, and resource controls.
- Database diagnostics and tested backup/restore procedures.
- Safe single-server operation and a documented multi-server path.
- Authentication, authorization, secret handling, and isolation tests.
- Reliable upgrades and recovery instructions.
- Reproducible Docker image builds.
- An automated, credential-gated release and publication workflow.
- Accurate documentation and a feature verification report.

The Lite profile must be tested on representative low-resource hardware. Do not promise a complete platform plus arbitrary workloads can fit into 1 GB RAM. State measured requirements and supported limitations.

The multi-node profile must be tested across actual nodes or a meaningful integration environment. Do not claim cluster recovery based only on unit tests.

---

## 20. Execution protocol: continue until completion

At the beginning, provide a concise audit summary and ordered plan.

Then implement Module 1 and continue through the sequence.

After every module:

1. Summarize what changed.
2. List the main files and interfaces added or modified.
3. Run the relevant tests and checks.
4. Report exact results.
5. Fix regressions before moving on.
6. Update the implementation plan and feature status.
7. Identify any unresolved risk or external dependency.

Do not repeatedly ask for approval for ordinary implementation decisions that fit this specification.

Do not stop after writing a plan, creating mock pages, or generating configuration files. Implement the real behavior and verify it.

If time, context, or execution limits interrupt the work, leave the repository in a coherent state, update the plan with the exact next step, and state which modules remain incomplete. Never mark incomplete work as complete.

At the end, produce a release report containing:

- Implemented features.
- Verified features and exact test commands/results.
- Known limitations and unsupported environments.
- Security review findings.
- Measured Lite-profile resource usage.
- Installation and update commands.
- Management IP access instructions.
- Domain and HTTPS setup instructions.
- Application domain setup instructions.
- Multi-server enrollment instructions.
- Backup and restore instructions.
- Docker image names and published digests, if actually published.
- Any credentials or external actions still required from the maintainer.

**Begin by auditing the existing repository, then implement and verify the system module by module until all achievable requirements are complete.**