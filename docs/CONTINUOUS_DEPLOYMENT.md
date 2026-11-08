# Continuous deployment and monorepos

JP Cloud deploys a project when its repository changes. Each connection (one project <-> one repository) has its own
**deployment settings** (*Project -> Repository -> Deployment settings*), modelled on what people know from GitHub Actions
workflows. Several projects can connect the **same repository**, each building its own folder.

## What you can configure

| Setting | Meaning | Default |
|---|---|---|
| **Root directory** | The folder of the repository this project builds (`backend`, `apps/web`, ...). Its `Dockerfile` / `jp.yaml` are read from there, the build context is that folder, and the paths in `jp.yaml` are relative to it. | repository root |
| **Auto-deploy** | A push to the production branch deploys to production. Off = pushes never deploy by themselves (manual deploys still work). | on |
| **Production branch** | Which branch is production. | the repository's default branch |
| **Wait for CI** | Hold the deployment until the repository's GitHub Actions run for that commit succeeded (see below). | off |
| **Preview other branches** | Pushes to any other branch create a preview deployment (never receives production secrets). | on |
| **Preview pull requests** | Each PR gets a preview, removed when the PR closes. Fork PRs stay blocked unless the connection allows forks. | on |
| **Also watch these paths** | Extra globs (relative to the repository root) that also trigger a deploy, e.g. shared code `packages/shared/**`. | none |
| **Ignore changes to** | Globs; a push that only touches these does not deploy, e.g. `**/*.md`, `docs/**`. | none |

A commit whose message contains `[skip ci]`, `[ci skip]`, `[skip deploy]`, `[skip cd]` or `[no deploy]` is never deployed.

The defaults reproduce the behaviour from before these settings existed.

## Monorepo: one repository, several projects

```
repo/
  backend/    Dockerfile            -> project "api"       root directory: backend
  frontend/   package.json          -> project "web"       root directory: frontend
  packages/shared/                  -> watch path of both
```

1. Create one project per deployable folder and connect the **same repository** to each.
2. Give each connection its **root directory** (type it, or press *Browse the repository* to pick a folder; the browser shows which
   folders contain a `Dockerfile`, `package.json`, `go.mod`, ...).
3. Push. A push that changes only `backend/**` deploys only *api*; one that changes only `frontend/**` deploys only *web*; a push that
   changes both deploys both; a push that changes neither (say `README.md`) deploys nothing. The webhook response lists, per project,
   why a deployment was skipped.
4. Shared code outside the folder: add it to **Also watch these paths** (`packages/shared/**`) so changing it redeploys the project.
   If the build itself needs files outside the folder, keep the root directory empty and point `jp.yaml` at the Dockerfile instead
   (`build.dockerfile: backend/Dockerfile`, `build.context: .`).

How the folder is built: after the clone the worker enters the root directory. `jp.yaml` is read from **there** (a `jp.yaml` at the
repository root does not leak into sub-projects), `build.dockerfile` / `build.context` are relative to it and cannot leave it, and a
Dockerfile is detected or generated inside it. A root directory that does not exist in the commit fails the deployment with a clear
message naming it; nothing is built.

Change detection uses the files listed in the push event (added, modified and removed). When GitHub does not list them (no commit list
or its 20 commit cap) the change set is **unknown** and every project deploys: skipping a needed deploy is worse than an extra one.
Pull-request previews are not path-filtered (GitHub does not list files in that event).

## Wait for CI (GitHub Actions)

With *Wait for CI* a push is remembered (24 h) instead of deployed. When GitHub reports the CI result for that exact commit
(`check_suite` of GitHub Actions, or `workflow_run`):

* **success** (`neutral` / `skipped` count as success for check suites): the held deployment is created, with the pushed commit, branch and message;
* **failure / cancelled / timed out**: the held deployment is dropped, so a commit whose CI failed never deploys.

Use `check_suite` when possible: it completes when *all* workflows of the commit are done, whereas the first successful `workflow_run`
releases the deployment. Check suites of other apps are ignored. A commit that has no CI configured never receives a result, so it
never deploys while this switch is on; use *Deploy* in the dashboard for those.

## Webhooks

Pushes reach JP Cloud through a signed webhook (`X-Hub-Signature-256`, replay-protected).

* **GitHub App connection**: events arrive through the App. Nothing to do.
* **Token connection**: press *Create the webhook on GitHub for me*. The token needs admin access to the repository (classic scope
  `admin:repo_hook`, or fine-grained *Webhooks: read and write*). The hook is created (or updated) with the events JP Cloud needs:
  `push`, `pull_request`, `check_suite`, `workflow_run`.
* **Public URL / deploy key connection**: add the webhook by hand with the URL and secret shown right after connecting. The secret
  cannot be shown again; *Show the webhook URL and a new secret* issues a new one (the old one stops working).

Each connection has its **own** hook URL (`/api/v1/webhooks/github?c=<connection id>`) and secret, so two projects of one repository
each get their own GitHub hook and one project's secret never verifies the other's deliveries.

## Where it lives

| Piece | Code |
|---|---|
| Rules (globs, relevance, skip markers, decisions) | `services/repository/internal/cd` |
| Settings API (`PATCH .../repos/{id}`), webhook install/rotate, folder browser | `services/repository/internal/handlers/cdhandlers.go` |
| Push / CI / PR handling | `services/repository/internal/handlers/webhook.go` |
| Build in the root directory | `services/worker/internal/worker/worker.go` (`resolveRootDir`) |
| UI | `apps/dashboard/src/features/cd.tsx` |

## Tests

* `services/repository/internal/cd`: path validation, glob matching, monorepo relevance, skip markers, every decision.
* `services/repository/internal/handlers/cd_test.go`: a two-project monorepo receiving the same pushes, watch/ignore paths, auto-deploy,
  custom production branch, preview switches, wait-for-CI (success, failure, other apps, repeats, `workflow_run`), settings API
  validation and RBAC, secret rotation, per-connection hooks, hook creation/update through a fake GitHub, the folder browser.
* `services/worker/internal/worker`: real git repositories: backend / frontend / nested folders, `jp.yaml` scoping, missing and hostile root directories.
* `apps/dashboard/e2e-ui/cd.spec.ts`: against the real stack: connect with a folder, the one-time secret, skipped and rejected
  deliveries, the settings dialog, folder browsing, secret rotation, and a real push into a folder that does not exist ending in a failed
  deployment with the clear message.

## Not built

* GitLab / Bitbucket (GitHub only).
* Deploying on tags / releases; a manual-approval gate.
* Path filters for pull-request previews.
* Running your own CI: JP Cloud waits for GitHub Actions, it does not run pipelines itself.
