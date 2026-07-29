# jp Go HTTP starter

Minimal Go service scaffolded by `jp init --runtime go`.

## Run locally

```bash
go run .
# GET http://localhost:8080/healthz
```

## Deploy with jp

```bash
jp login
jp org use <slug>
jp apply --project <project-id>
jp deploy --project <project-id>
```

Provision add-ons (Redis, Postgres, …) from the dashboard **Add-ons** page or `jp addon create`.
