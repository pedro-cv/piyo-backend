# Security policy

## Reporting a vulnerability

Please do **not** open a public issue for security problems.

1. Preferred: GitHub private vulnerability reporting — *Security* tab → *Report a vulnerability*
   (enabled on both `piyo-app` and `piyo-backend`).
2. Alternative: contact a maintainer listed in `.github/CODEOWNERS` privately.

Include what you found, how to reproduce it, the version or commit, and the impact you believe it has.
You will get an acknowledgement within 7 days and a status update at least every 14 days until it is
resolved. Coordinated disclosure after a fix is released is appreciated; credit is given unless you prefer
to stay anonymous.

## Scope

- `piyo-backend`: the Go API (`/v1/*`), the importer, the Docker Compose stack and Caddy configuration.
- `piyo-app`: the Android app, including how it stores data on the device and talks to the API.

Especially relevant: anything that links a report, NPS answer or contribution to a person or device,
bypasses quotas, blocks or the admin token, reads outside `deploy/data`, or lets a dataset release be
tampered with (checksums, manifest).

Out of scope: the upstream transit data itself (report data errors in the app or with the *Data error*
issue template), denial of service by volume against a self-hosted instance, and issues in third-party
services the app calls (Photon geocoder, tile hosts).

## Supported versions

The latest release of each repository and the `main` branch. Older APKs are asked to update through
`min_app_version` in the API manifest.

## What we do on our side

- No accounts, analytics or ads; the API keeps only `sha256(HASH_SALT + install_id)`, never IPs or raw ids.
- Secrets live in environment files that are git-ignored; CI runs gitleaks and a custom scan on every push,
  and GitHub push protection is enabled.
- Dependencies are updated by Dependabot; the API runs as a non-root distroless container behind Caddy TLS.
