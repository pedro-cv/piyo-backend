# Starting at $0: free-tier stack for the backend

Written 2026-11-01. Goal: launch with no monthly bill and no VPS, then move to `DEPLOY_VPS.md` when traffic
or cold starts justify it. Nothing in the app changes between the two setups except two values in
`app/keys.properties`. Free-tier limits quoted here are what the providers advertised in late 2026; check
the pricing page at signup, they change.

## The stack

| Piece | Free choice | Why this one | Limit that matters |
|---|---|---|---|
| Database | **Neon** (Postgres, PostGIS extension available) | The store is pgx + `CREATE EXTENSION postgis`, works unchanged; branches for a staging DB; no credit card | 0.5 GB storage, compute suspends after 5 min idle (first query after idle ≈ 0.5–1 s) |
| API | **Google Cloud Run** (container, scale to zero) | Runs the existing Dockerfile as is; 2M requests/month free; HTTPS and a `*.run.app` domain included; custom domain free | Needs a Google billing account (card) even if the bill is $0; cold start ≈ 1–2 s for this Go binary |
| API, no-card alternative | **Render** free web service | Deploys the Dockerfile from the public repo; no card | Sleeps after 15 min idle, wake-up ≈ 30–60 s; 750 instance-hours/month |
| Map tiles and style | **OpenFreeMap** (`https://tiles.openfreemap.org/styles/liberty`) | Free, keyless, OSM data, already supported by `MAP_STYLE_URL`; attribution is already shown | Fair use, no SLA; keep the OSM attribution |
| Address search | **Photon** public (`https://photon.komoot.io/api/`) | Already the default | Fair use; self-host past ~10k MAU |
| Dataset files | **Baked into the API image** (`backend/releases/` → `/data/releases`) | No object storage needed; `/v1/manifest` and `/v1/datasets/N/...` work as today | A new dataset release means a new deploy (one command) |
| Landing | **GitHub Pages** | Static Astro site, `PUBLIC_SITE`/`PUBLIC_BASE` already wired | 100 GB/month soft limit |
| CI | **GitHub Actions** | Free for public repos | 2,000 min/month if a repo ever goes private |

Not recommended: **Vercel** for this backend. The API is a long-running `net/http` server that reads the
release files from disk; Vercel runs Go only as per-route serverless functions with a read-only filesystem
and 10 s limits, so it would mean rewriting the entry point and moving the files to storage. Cloud Run is
the equivalent for containers. **Fly.io** and **Railway** no longer have a permanent free tier.
**Supabase** free Postgres also works (PostGIS included) but pauses projects after 7 days without traffic.

What is skipped on the free stack and comes back with the VPS: your own tile server (Martin + PMTiles) and
the self-hosted style, the daily `pg_dump` cron (Neon keeps 24 h of point-in-time restore on the free plan)
and Caddy.

## 1. Neon

1. https://neon.tech → New project `piyo`, region **US East (N. Virginia)** or **São Paulo** if offered
   (closest to Bolivia), Postgres 17.
2. SQL editor: `CREATE EXTENSION IF NOT EXISTS postgis;` (the API also runs it on start, this just confirms
   the plan allows it).
3. Copy the **pooled** connection string (it ends in `?sslmode=require`) and keep it as `DATABASE_URL`.
   pgx handles `sslmode=require` without extra code.
4. Load the dataset into it from your PC (one time per release):
   ```sh
   cd piyo/backend
   go run ./cmd/piyo-import -source ../../santa-cruz-transit-data/data-microcruz/lines.json \
     -out ./releases -version 15 -db "$DATABASE_URL"
   ```
   This writes `releases/v15/` locally (needed for the image) and fills the transit tables.

## 2. Cloud Run

```sh
# once
gcloud auth login && gcloud projects create piyo-prod && gcloud config set project piyo-prod
gcloud billing accounts list && gcloud billing projects link piyo-prod --billing-account <ID>
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com
# budget alert so a surprise never goes unnoticed (console: Billing → Budgets → $1/month, email at 50/100 %)

# every deploy, from backend/ with releases/vN present
cd piyo/backend
gcloud run deploy piyo-api --source . --region us-east1 --allow-unauthenticated \
  --min-instances 0 --max-instances 2 --memory 256Mi --cpu 1 --concurrency 80 \
  --set-env-vars DEV_MODE=false,STORE=postgres,PUBLIC_BASE_URL=https://api.<domain> \
  --set-secrets DATABASE_URL=piyo-database-url:latest,ADMIN_TOKEN=piyo-admin-token:latest,HASH_SALT=piyo-hash-salt:latest
```

Create the three secrets first (`gcloud secrets create piyo-hash-salt --data-file=-` and paste
`openssl rand -hex 32`); `HASH_SALT` is permanent, see `backend/.env.example`. `PORT` is injected by Cloud Run
and the API reads it. Until a custom domain exists, leave `PUBLIC_BASE_URL` unset: the API derives absolute
URLs from the request host (`*.run.app`). Region: `us-east1` is in Cloud Run's free-tier (tier 1) pricing;
`southamerica-east1` is closer but tier 2, so it eats the free allowance faster.

Custom domain: Cloud Run → Manage custom domains → `api.<domain>` → add the CNAME it shows. Certificate is
automatic and free.

Easier: `bash deploy/cloudrun/deploy.sh <project-id>` does all of the above (APIs, secrets, IAM, deploy,
image cleanup, health check). `backend/.gcloudignore` makes the source upload include `releases/` (which
`.gitignore` excludes) and keep `.env`/keys out of Cloud Build.

Smoke test: `backend/bruno`, environment `server`, `baseUrl` = the run.app URL, secret `adminToken`.
`/v1/health` must say `store: postgres`, `dataset_source: releases`.

## 2b. Push notifications (FCM, optional, $0)

Firebase lives in its **own project** (e.g. `piyo-push`), separate from the Cloud Run project (`piyo-prod`),
so billing and access stay apart. The Firebase project needs no billing: FCM is free on the Spark plan.

1. https://console.firebase.google.com → **Add project** → a new name (not the Cloud Run project) → Google
   Analytics **off**. Leave it on Spark. Note its project id.
2. Project settings → **Add app** → Android, package `bo.piyo.app`. Skip the google-services.json download
   step; copy App ID, Web API key, Project ID and Project number into the `FIREBASE_*` keys of
   `app/keys.properties`. In Google Cloud → Credentials (Firebase project), restrict that API key to
   Android apps → `bo.piyo.app`.
3. Redeploy with `FCM_PROJECT_ID=<firebase-project-id> bash deploy/cloudrun/deploy.sh <run-project-id>`. It
   enables `fcm.googleapis.com` in both projects and grants the Cloud Run service account
   (`<run-project-number>-compute@developer.gserviceaccount.com`) *Firebase Cloud Messaging API Admin* on the
   Firebase project only. No key file is created; the API gets tokens from the metadata server. The gcloud
   account running the script must be Owner of both projects (it is, if you created them).
4. `/v1/health` logs `push enabled=true` at start. After publishing a release:
   `curl -X POST -H "X-Admin-Token: …" $URL/v1/admin/push/dataset`.

## 2c. Staying at $0: the limits that bite first

| Limit (free tier) | What eats it | Guard |
|---|---|---|
| Cloud Run: 2M requests, 180k vCPU-s, 360k GiB-s / month | API traffic; CPU is billed only during requests (default) | `--max-instances 2`, budget alert |
| Network egress: about 1 GB/month free | Dataset downloads (`dataset-vN.json.gz` ≈ 650 KB; ~1,500 updates/month) | Seed in the APK covers first run; if releases get frequent, host the package on a zero-egress bucket (R2) |
| Artifact Registry: 0.5 GB | One image (~25 MB) per deploy | deploy.sh sets a keep-3 cleanup policy |
| Secret Manager: 6 active versions, 10k accesses / month | A new version per deploy; 3 reads per cold start | deploy.sh only adds a version when the value changed; disable old versions |
| Cloud Build: 2,500 build-min / month | ~1–2 min per deploy | — |
| Neon: 0.5 GB, compute auto-suspends | Contributions with polylines | first request after idle ≈ 1 s |

Set the budget alert before the first deploy: Billing → Budgets → $1, email at 50/100 %.

## 3. Render (if you prefer no card)

New → Web Service → connect `pedro-cv/piyo-backend` → Runtime Docker → Free instance. Environment:
`DEV_MODE=false`, `STORE=postgres`, `DATABASE_URL`, `ADMIN_TOKEN`, `HASH_SALT` (`RELEASES_DIR=/data/releases` comes from the Dockerfile; do not pass it from Git Bash, which rewrites it to `C:/Program Files/Git/data/releases`).
Because Render builds from git and `releases/` is ignored, commit the release package to a branch Render
deploys from, or build the release in a Render build command from a public copy of the source file. The
app tolerates the sleep: the manifest check runs in the background, reports queue in the offline outbox.

## 4. App configuration for the free stack

```properties
# app/keys.properties
PIYO_API_BASE_URL=https://piyo-api-xxxxx-ue.a.run.app      # or https://api.<domain>
MAP_STYLE_URL=https://tiles.openfreemap.org/styles/liberty
GEOCODER_URL=https://photon.komoot.io/api/
```

Set `manifestPlaceholders["usesCleartextTraffic"] = "false"` for the release build. No Google Maps key, no
`google-services.json`, no analytics or accounts. Push is optional: the four `FIREBASE_*` keys (§2b); blank
keys build an app without push.

## 5. Landing on GitHub Pages

```sh
cd landing
PUBLIC_SITE=https://<user>.github.io PUBLIC_BASE=/piyo-landing/ npm run build
```
Publish `dist/` with the Pages action of a `piyo-landing` repo (or a `gh-pages` branch). Set `playStore.url`
in `src/data/site.json` when the listing exists.

## 6. When to move to the VPS

Signals: Cloud Run bill above a few dollars (tiles are not on it, so this means real API traffic), map
complaints about OpenFreeMap latency or fair use, Neon storage near 0.5 GB (contributions with polylines are
the only thing that grows), or you want the self-hosted map. `DEPLOY_VPS.md` is the same code with Compose;
export Neon with `pg_dump`, restore into the PostGIS container, keep the same `HASH_SALT`.
