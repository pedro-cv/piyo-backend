#!/usr/bin/env bash
# Secret and sensitive-file scan for Piyo. Used by the pre-commit hook, the /ship and
# /public-release-check skills, and CI (next to gitleaks). Exit 1 on any finding.
#
# Usage: bash scripts/check-secrets.sh [--staged]
#   default : scan every tracked file
#   --staged: scan only files staged for commit (fast, for the hook)
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1

mode="${1:-}"
if [ "$mode" = "--staged" ]; then
  files=$(git diff --cached --name-only --diff-filter=ACMR)
else
  files=$(git ls-files)
fi
[ -z "$files" ] && exit 0

status=0

# 1. Files that must never be committed, whatever their content.
forbidden='(^|/)\.env(\.[A-Za-z0-9_-]+)?$|(^|/)keys\.properties$|(^|/)secrets\.properties$|(^|/)local\.properties$|(^|/)google-services\.json$|\.(jks|keystore|p12|pem|pfx|key)$|(^|/)settings\.local\.json$'
bad=$(printf '%s\n' "$files" | grep -Ei "$forbidden" | grep -Ev '\.example$' || true)
if [ -n "$bad" ]; then
  echo "FAIL: sensitive files in the commit set:"; printf '  %s\n' $bad; status=1
fi

# 2. Generated data and private notes that do not belong in a public repo.
private='^deploy/data/(?!\.gitkeep).+|^(PROGRESS|PLAN)\.md$|^design/|^docs/audit-screens/'
if git remote get-url origin 2>/dev/null | grep -qiE 'piyo-(app|backend)'; then
  bad=$(printf '%s\n' "$files" | grep -P "$private" || true)
  if [ -n "$bad" ]; then
    echo "FAIL: private material staged for a public repo:"; printf '  %s\n' $bad; status=1
  fi
fi

# 3. Content patterns (binary and large data files skipped).
patterns='AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|gho_[A-Za-z0-9]{36}|sk-[A-Za-z0-9]{32,}|sk-ant-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY|(ADMIN_TOKEN|HASH_SALT|POSTGRES_PASSWORD)\s*[:=]\s*["'"'"']?[A-Za-z0-9+/=_-]{24,}|postgres(ql)?://[^:/\s]+:[^@\s$<{]{8,}@|eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}'
scan=$(printf '%s\n' "$files" | grep -Ev '\.(png|jpg|jpeg|webp|gif|ico|jar|pmtiles|gz|ttf|pbf|woff2?)$|dataset-seed\.json$|static/lines-v[0-9]+\.json$|design/canvas/' || true)
if [ -n "$scan" ]; then
  if [ "$mode" = "--staged" ]; then
    hits=$(for f in $scan; do git show ":$f" 2>/dev/null | grep -nE "$patterns" | sed "s|^|$f:|"; done)
  else
    hits=$(grep -nIE "$patterns" $scan 2>/dev/null || true)
  fi
  # Allow documented placeholders.
  hits=$(printf '%s\n' "$hits" | grep -Ev 'change-me|dev-admin-token|dev-salt|example|placeholder|openssl rand|<[A-Z_ ]+>|\$\{' || true)
  if [ -n "$hits" ]; then
    echo "FAIL: secret-looking content:"; printf '%s\n' "$hits" | sed 's/\(.\{160\}\).*/\1…/' | sed 's/^/  /'; status=1
  fi
fi

[ $status -eq 0 ] && echo "check-secrets: OK ($(printf '%s\n' "$files" | wc -l | tr -d ' ') files)"
exit $status
