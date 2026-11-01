#!/usr/bin/env python3
"""PreToolUse guard for Piyo.

Blocks (exit 2 = deny; the message goes back to the model):
  * reading/editing/writing secret files (.env, keys.properties, keystores, tokens)
  * shell commands that print those files or `git add` them
  * direct pushes or force pushes to main/master, history rewrites, --no-verify
Reads the hook payload (JSON) from stdin. Never prints file contents.
"""
import json
import re
import sys

SECRET_PATH = re.compile(
    r"(^|[\\/])(\.env(\.[A-Za-z0-9_-]+)?|keys\.properties|secrets\.properties|local\.properties|"
    r"google-services\.json|settings\.local\.json|[^\\/]*\.(jks|keystore|p12|pem|pfx|key))$",
    re.IGNORECASE,
)
ALLOWED = re.compile(r"\.env\.example$|keys\.properties\.example$", re.IGNORECASE)
HARMLESS = re.compile(r"\b(ls|dir|test|stat|Test-Path|rm|del|cp|copy|Copy-Item)\b")


def is_secret(path: str) -> bool:
    path = path.strip().strip("'\"")
    return bool(path) and bool(SECRET_PATH.search(path)) and not ALLOWED.search(path)


def deny(msg: str) -> None:
    sys.stderr.write(msg + "\n")
    sys.exit(2)


def main() -> None:
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return
    tool = payload.get("tool_name", "")
    inp = payload.get("tool_input", {}) or {}

    if tool in ("Read", "Edit", "Write", "MultiEdit", "NotebookEdit"):
        p = inp.get("file_path") or inp.get("path") or inp.get("notebook_path") or ""
        if is_secret(p):
            deny(f"Blocked: {p} holds local secrets. Use the matching .example file instead.")
        return

    if tool in ("Bash", "PowerShell"):
        cmd = inp.get("command", "") or ""
        low = cmd.lower()
        for tok in re.split(r"[\s;&|<>()]+", cmd):
            if is_secret(tok) and not HARMLESS.search(cmd):
                deny(f"Blocked: command reads secret file {tok}. Read the .example file instead.")
        if re.search(r"\bgit\s+add\b", low):
            for tok in re.split(r"\s+", cmd):
                if is_secret(tok):
                    deny(f"Blocked: {tok} must never be committed.")
        if re.search(r"\bgit\s+push\b", low):
            if re.search(r"--force|\s-f\b|\s\+[a-z]", low):
                deny("Blocked: force push. Rebase on a branch and open a PR instead.")
            if re.search(r"\b(origin|upstream)\s+(main|master)\b|:(main|master)\b|\bhead:(main|master)\b", low):
                deny("Blocked: direct push to main/master. Push a feature branch and open a PR.")
        if re.search(r"\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f|push\s+--delete|filter-branch)", low):
            deny("Blocked: destructive git command. Ask the user to run it if really needed.")
        if re.search(r"\bgit\s+commit\b.*--no-verify", low):
            deny("Blocked: --no-verify skips the secret-scan hook.")
        if re.search(r"docker\s+compose\b.*\bdown\b.*-v", low):
            deny("Blocked: `docker compose down -v` deletes the database volume. Ask the user.")


if __name__ == "__main__":
    main()
