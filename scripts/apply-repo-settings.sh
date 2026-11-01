#!/usr/bin/env bash
# Apply the public-repo protections and settings to a GitHub repository. Idempotent.
#
#   bash scripts/apply-repo-settings.sh <owner>/<repo>
#
# Requires: gh (logged in as an admin of the repo), jq. Run it from the repo that holds
# .github/rulesets/main-branch.json (the workspace or an exported public repo).
set -euo pipefail
repo="${1:?usage: apply-repo-settings.sh owner/repo}"
root="$(git rev-parse --show-toplevel)"
template="$root/.github/rulesets/main-branch.json"

# Required status checks depend on which workflows the repo ships.
checks='[{"context":"Secret scan"}'
case "$repo" in
  *piyo-app*)     checks="$checks,{\"context\":\"App unit tests\"}" ;;
  *piyo-backend*) checks="$checks,{\"context\":\"Backend tests\"}" ;;
  *)              checks="$checks,{\"context\":\"App unit tests\"},{\"context\":\"Backend tests\"}" ;;
esac
checks="$checks]"

echo "== $repo: general settings"
gh repo edit "$repo" \
  --enable-squash-merge --enable-merge-commit=false --enable-rebase-merge=false \
  --delete-branch-on-merge --enable-auto-merge \
  --enable-issues --enable-wiki=false --enable-projects=false \
  --allow-update-branch >/dev/null
gh api -X PATCH "repos/$repo" -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=PR_BODY >/dev/null

echo "== $repo: security features"
gh api -X PATCH "repos/$repo" --input - >/dev/null <<'JSON'
{"security_and_analysis":{"secret_scanning":{"status":"enabled"},"secret_scanning_push_protection":{"status":"enabled"},"dependabot_security_updates":{"status":"enabled"}}}
JSON
gh api -X PUT "repos/$repo/vulnerability-alerts" >/dev/null
gh api -X PUT "repos/$repo/private-vulnerability-reporting" >/dev/null || echo "   (private vulnerability reporting: enable manually under Settings → Security if this failed)"

echo "== $repo: Actions permissions (read-only token, no approval needed for first-time contributors? keep default)"
gh api -X PUT "repos/$repo/actions/permissions/workflow" --input - >/dev/null <<'JSON'
{"default_workflow_permissions":"read","can_approve_pull_request_reviews":false}
JSON

echo "== $repo: branch ruleset"
payload=$(sed "s|__CHECKS__|$checks|" "$template")
existing=$(gh api "repos/$repo/rulesets" --jq '.[] | select(.name=="protect-main") | .id' 2>/dev/null || true)
if [ -n "$existing" ]; then
  echo "$payload" | gh api -X PUT "repos/$repo/rulesets/$existing" --input - >/dev/null
  echo "   updated ruleset $existing"
else
  echo "$payload" | gh api -X POST "repos/$repo/rulesets" --input - >/dev/null
  echo "   created ruleset"
fi

echo "== $repo: labels"
for l in "bug:d73a4a" "enhancement:a2eeef" "data:0e8a16" "dependencies:0366d6" "ci:ededed" "backend:5319e7" "app:1d76db" "deploy:fbca04" "triage:fef2c0" "good first issue:7057ff" "help wanted:008672" "security:b60205"; do
  gh label create "${l%%:*}" --color "${l##*:}" --repo "$repo" --force >/dev/null 2>&1 || true
done

echo "== done. Verify:"
echo "   gh ruleset list --repo $repo"
echo "   gh repo view $repo --json securityAndAnalysis,deleteBranchOnMerge,squashMergeAllowed"
echo "Note: required_approving_review_count is 0 (solo maintainer; GitHub does not let you approve your own PR)."
echo "      Raise it to 1 and set require_code_owner_review=true once a second maintainer joins."
