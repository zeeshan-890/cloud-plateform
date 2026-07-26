# jp Dashboard

Next.js console for the **jp** cloud platform.

## Requirements

- Node.js 20+
- API gateway running (default `http://localhost:8000/api/v1`)

## Setup

```bash
cd apps/dashboard
cp .env.example .env.local
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000).

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `NEXT_PUBLIC_API_URL` | `http://localhost:8000/api/v1` | Gateway API base URL |

## Scripts

| Script | Description |
|--------|-------------|
| `npm run dev` | Start development server |
| `npm run build` | Production build |
| `npm start` | Serve production build |

## Auth

Access and refresh tokens are stored in `localStorage`. The API client sends `Authorization: Bearer <access_token>` and refreshes on `401`.

## Routes

| Path | Description |
|------|-------------|
| `/` | Landing |
| `/login` | Sign in |
| `/register` | Create account |
| `/projects` | Project list |
| `/projects/new` | Create project |
| `/projects/[projectId]` | Project detail |
| `/team` | Members + invite |
| `/keys` | Personal access tokens |
| `/sessions` | Active sessions |
| `/orgs/new` | Create organization |
| `/invite/accept` | Accept org invite |
