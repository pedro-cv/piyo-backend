#!/usr/bin/env bash
# Deploy the Piyo API to Google Cloud Run on the free tier, with Neon as the database.
# See docs/implementation/FREE_TIER.md. Run from the repo root in Git Bash:
#
#   export DATABASE_URL='postgresql://...neon.tech/piyo?sslmode=require'   # Neon pooled connection string
#   export FCM_PROJECT_ID=<firebase-project-id>   # optional: enables push. May be a separate Firebase project (its
#                                                 # own billing, Spark plan is enough); the runtime service account
#                                                 # of <gcp-project-id> gets the FCM role on it, no key file
#   bash deploy/cloudrun/deploy.sh <gcp-project-id> [region]
#
# First run: enables the APIs, creates the three secrets (ADMIN_TOKEN and HASH_SALT are generated here and
# stored only in Secret Manager; DATABASE_URL comes from the environment), deploys, prints the service URL.
# Later runs: adds a new DATABASE_URL version only if the value changed (Secret Manager bills active versions
# beyond 6), redeploys the current code and the releases/ folder, keeps only the 3 newest images (Artifact
# Registry: 0.5 GB free). Secrets are never printed. HASH_SALT is created once and never replaced by this script.
set -euo pipefail

project="${1:?usage: deploy.sh <gcp-project-id> [region]}"
region="${2:-us-east1}"
service="piyo-api"
root="$(cd "$(dirname "$0")/../.." && pwd)"
# Workspace: <root>/backend. Public piyo-backend repo: the backend is the repo root.
backend="$root/backend"; [ -f "$backend/go.mod" ] || backend="$root"

command -v gcloud >/dev/null || { echo "gcloud not found: install the Google Cloud CLI and open a new shell"; exit 1; }
gcloud config set project "$project" >/dev/null

if [ ! -f "$backend/releases/latest.json" ]; then
  echo "no dataset release in backend/releases: run piyo-import first (FREE_TIER.md §1 step 4)"; exit 1
fi

echo "== APIs"
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com >/dev/null
# FCM: enabled in the Firebase project and in this one, because calls made with this project's service account
# are attributed (quota only; FCM has no per-message charge) to the account's own project.
if [ -n "${FCM_PROJECT_ID:-}" ]; then
  gcloud services enable fcm.googleapis.com --project "$FCM_PROJECT_ID" >/dev/null
  [ "$FCM_PROJECT_ID" = "$project" ] || gcloud services enable fcm.googleapis.com --project "$project" >/dev/null
fi

secret_exists() { gcloud secrets describe "$1" >/dev/null 2>&1; }
ensure_secret() { # ensure_secret <name> <value>; value never echoed
  if secret_exists "$1"; then
    if [ "$(gcloud secrets versions access latest --secret="$1" 2>/dev/null)" = "$2" ]; then
      echo "   $1: unchanged"; return
    fi
    printf '%s' "$2" | gcloud secrets versions add "$1" --data-file=- >/dev/null
    echo "   $1: new version added (disable the old one: gcloud secrets versions list $1)"
  else
    printf '%s' "$2" | gcloud secrets create "$1" --replication-policy=automatic --data-file=- >/dev/null
    echo "   $1: created"
  fi
}

echo "== Secrets"
if [ -n "${DATABASE_URL:-}" ]; then
  ensure_secret piyo-database-url "$DATABASE_URL"
elif ! secret_exists piyo-database-url; then
  echo "DATABASE_URL is not set and the secret does not exist yet: export the Neon connection string first"; exit 1
else
  echo "   piyo-database-url: kept"
fi
if ! secret_exists piyo-admin-token; then
  ensure_secret piyo-admin-token "$(openssl rand -hex 32)"
else
  echo "   piyo-admin-token: kept (rotate with: openssl rand -hex 32 | gcloud secrets versions add piyo-admin-token --data-file=-)"
fi
if ! secret_exists piyo-hash-salt; then
  ensure_secret piyo-hash-salt "$(openssl rand -hex 32)"
  echo "   piyo-hash-salt: generated once; NEVER rotate it (every device hash, quota and block depends on it)"
else
  echo "   piyo-hash-salt: kept"
fi

# The Cloud Run runtime service account must be allowed to read the secrets.
project_number="$(gcloud projects describe "$project" --format='value(projectNumber)')"
runtime_sa="${project_number}-compute@developer.gserviceaccount.com"
for s in piyo-database-url piyo-admin-token piyo-hash-salt; do
  gcloud secrets add-iam-policy-binding "$s" --member="serviceAccount:$runtime_sa" --role=roles/secretmanager.secretAccessor >/dev/null
done
# Push: the same service account sends FCM messages through the metadata server, so no key file exists. The
# binding is on the Firebase project, which may differ from this one (cross-project IAM; the gcloud account
# running this needs Owner or Project IAM Admin there).
if [ -n "${FCM_PROJECT_ID:-}" ]; then
  gcloud projects add-iam-policy-binding "$FCM_PROJECT_ID" --member="serviceAccount:$runtime_sa"     --role=roles/firebasecloudmessaging.admin --condition=None >/dev/null
  echo "   FCM: $runtime_sa may send to $FCM_PROJECT_ID"
fi

echo "== Deploy ($region)"
# RELEASES_DIR is not set here: the Dockerfile ENV already points it at /data/releases, and Git Bash would
# rewrite "/data/releases" in this argument into "C:/Program Files/Git/data/releases".
env_vars="DEV_MODE=false,STORE=postgres"
if [ -n "${PUBLIC_BASE_URL:-}" ]; then env_vars="$env_vars,PUBLIC_BASE_URL=$PUBLIC_BASE_URL"; fi
if [ -n "${FCM_PROJECT_ID:-}" ]; then env_vars="$env_vars,FCM_PROJECT_ID=$FCM_PROJECT_ID"; fi
gcloud run deploy "$service" --source "$backend" --region "$region" --allow-unauthenticated \
  --min-instances 0 --max-instances 2 --memory 256Mi --cpu 1 --concurrency 80 --timeout 60 \
  --set-env-vars "$env_vars" \
  --set-secrets "DATABASE_URL=piyo-database-url:latest,ADMIN_TOKEN=piyo-admin-token:latest,HASH_SALT=piyo-hash-salt:latest" \
  --quiet

# Source deploys push every image to this repository; keep the newest 3 so storage stays under 0.5 GB.
policy="$(mktemp)"
cat > "$policy" <<'JSON'
[{"name":"keep-3","action":{"type":"Keep"},"mostRecentVersions":{"keepCount":3}},
 {"name":"delete-rest","action":{"type":"Delete"},"condition":{"tagState":"any"}}]
JSON
gcloud artifacts repositories set-cleanup-policies cloud-run-source-deploy --location "$region"   --policy "$policy" --no-dry-run >/dev/null 2>&1 && echo "   images: cleanup policy keep-3"   || echo "   images: could not set the cleanup policy; delete old images in Artifact Registry by hand"
rm -f "$policy"

url="$(gcloud run services describe "$service" --region "$region" --format='value(status.url)')"
echo "== Health"
curl -fsS "$url/v1/health" && echo
echo
echo "Service URL: $url"
echo "Admin token for Bruno: gcloud secrets versions access latest --secret=piyo-admin-token"
echo "Next: bruno environment 'server' with baseUrl=$url, then PIYO_API_BASE_URL=$url in app/keys.properties"
