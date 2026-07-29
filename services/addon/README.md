# addon

Phase 6 — one-click managed add-ons marketplace (port **8021**).

## Engines

`postgres`, `mysql`, `mongodb`, `redis`, `rabbitmq`, `kafka`, `sqlite`

## Modes

| Mode | Env |
|------|-----|
| `simulate` (default) | Mint connection URLs/secrets without shared brokers |
| `shared` | Provision against Compose `addons` profile brokers; falls back to simulate on failure |

## API

- `GET /orgs/{org}/projects/{project}/addons/catalog`
- `GET|POST /orgs/{org}/projects/{project}/addons`
- `GET|DELETE /orgs/{org}/projects/{project}/addons/{id}`

Create body: `{ "engine": "redis", "name": "cache", "env": "development" }`
