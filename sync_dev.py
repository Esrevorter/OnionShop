#!/usr/bin/env python3
"""
sync_dev.py — frictionless end-to-end dev sync for OnionShop.

Flow:
  1. Read GitHub credentials from ./CRED (line 1 = username, line 2 = PAT).
  2. Fetch the branch this workspace pushes to (default below) and verify the
     exact tested commits are present BEFORE touching your working tree.
  3. Fast-forward your local checkout onto that branch (stash/restore if dirty).
  4. Run go build / vet / test with a safe TMPDIR (fixes noexec /tmp).
  5. Restart the Docker app container (templates parse at startup; CSS is
     bind-mounted so nginx only needs its cache headers revalidated).
  6. Print a green/red summary. Nothing is restarted if tests fail.

Usage (from repo root):
    python3 sync_dev.py                 # full run
    python3 sync_dev.py --no-docker     # skip container restart
    python3 sync_dev.py --branch main   # sync a different branch
"""

import argparse
import base64
import os
import subprocess
import sys
import time

# --------------------------------------------------------------------------
# Configuration
# --------------------------------------------------------------------------
REPO_ROOT   = os.path.dirname(os.path.abspath(__file__))
CRED_FILE   = os.path.join(REPO_ROOT, "CRED")
REMOTE_NAME = "origin"

# The agent workspace branch. Update when a new task branch is opened.
DEFAULT_BRANCH = "qwen-code-9e2a012c-b9a9-4d88-8425-f5a0d0929430"

# Commits that MUST be present in the fetched branch before we sync.
# These are the ones verified green in the agent workspace. If you see a
# "missing required commit" error, the PR was merged without the latest push
# or you pointed --branch at the wrong ref.
REQUIRED_COMMITS = [
    "3f039b5",  # P1d — robust web/ path resolution in contract test
    "1d540cb",  # P1c — quote-aware tokenizer (kills the 148 false positives)
    "b460ab0",  # P1b — contract parser rewrite
    "ef86c9e",  # P1  — design-system merge (shop.css)
    "15c06a1",  # P0  — XMR address render, CSP-safe confirms, price hierarchy
]

GO_MIN_VERSION = (1, 25)          # module requirement
TMPDIR_LOCAL   = os.path.expanduser("~/gotmp")  # avoids noexec /tmp failures


def sh(cmd, cwd=REPO_ROOT, env=None, check=True, capture=True, auth=None):
    """Run a shell command list, echoing it (auth args masked), returning stdout.

    `auth` is the list of git -c args carrying the base64 Authorization header;
    it is never printed and never persisted."""
    printable = " ".join(
        a if "extraHeader" not in a else "[auth-header]" for a in cmd
    )
    print(f"  $ {printable}")
    full = list(cmd)
    if auth:
        # insert -c args right after 'git'
        idx = full.index("git") + 1 if "git" in full[:1] else 1
        full = full[:idx] + list(auth) + full[idx:]
    e = os.environ.copy()
    if env:
        e.update(env)
    r = subprocess.run(
        full, cwd=cwd, env=e,
        capture_output=capture, text=True,
    )
    if check and r.returncode != 0:
        if capture and r.stdout:
            print(r.stdout[-4000:])
        if capture and r.stderr:
            print(r.stderr[-4000:], file=sys.stderr)
        raise SystemExit(f"command failed ({r.returncode}): {' '.join(cmd)}")
    return (r.stdout or "").strip() if capture else ""


def die(msg):
    print(f"\n\033[31m✗ {msg}\033[0m", file=sys.stderr)
    sys.exit(1)


def ok(msg):
    print(f"\033[32m✓ {msg}\033[0m")


def step(n, title):
    print(f"\n\033[1m[{n}] {title}\033[0m")


# --------------------------------------------------------------------------
# Credentials
# --------------------------------------------------------------------------
def load_creds():
    """Return (username, PAT). Reads ./CRED first; falls back to GH_USER/GH_TOKEN.
    The PAT is NEVER echoed or written anywhere by this script."""
    if os.path.isfile(CRED_FILE):
        with open(CRED_FILE) as f:
            lines = [l.strip() for l in f if l.strip() and not l.startswith("#")]
        if len(lines) >= 2:
            return lines[0], lines[1]
        die("CRED must contain username on line 1 and PAT on line 2.")
    user, tok = os.environ.get("GH_USER"), os.environ.get("GH_TOKEN")
    if user and tok:
        return user, tok
    die(f"No credentials found.\n"
        f"Create {CRED_FILE} with two lines: GitHub username, then PAT "
        f"(or export GH_USER / GH_TOKEN).")


def b64(value: str) -> str:
    return base64.b64encode(value.encode("utf-8")).decode("ascii")


def git_auth_args(user, pat):
    """Base64-only authentication: credentials go into an Authorization header
    (Basic base64(user:pat)) via per-invocation -c config — never into the
    remote URL, .git/config, or credential files."""
    basic = b64(f"{user}:{pat}")
    return ["-c", f"http.extraHeader=Authorization: Basic {basic}"]


def clean_remote_url():
    """Normalize origin to an https URL WITHOUT embedded credentials, so no
    plaintext secret can ever land in .git/config."""
    current = sh(["git", "remote", "get-url", REMOTE_NAME])
    if current.startswith("https://"):
        host_path = current.split("://", 1)[1].split("@", 1)[-1]
    elif current.startswith("git@"):
        host, path = current[4:].split(":", 1)
        host_path = f"{host}/{path}"
    elif current.startswith("http://"):
        host_path = current.split("://", 1)[1]
    else:
        die(f"Unsupported remote URL scheme: {current!r}")
    clean = f"https://{host_path}"
    if current != clean:
        sh(["git", "remote", "set-url", REMOTE_NAME, clean])
    return clean


# --------------------------------------------------------------------------
# Main pipeline
# --------------------------------------------------------------------------
def main():
    ap = argparse.ArgumentParser(description="OnionShop dev sync + verify + restart")
    ap.add_argument("--branch", default=DEFAULT_BRANCH,
                    help=f"Remote branch to sync (default: {DEFAULT_BRANCH})")
    ap.add_argument("--no-docker", action="store_true", help="Skip container restart")
    args = ap.parse_args()

    os.chdir(REPO_ROOT)
    t0 = time.time()

    # ---- 1. Credentials (base64-only auth) -------------------------------
    step(1, "Load GitHub credentials from CRED → base64 Authorization header")
    user, pat = load_creds()
    clean_remote_url()                       # strip any creds from .git/config
    auth = git_auth_args(user, pat)          # Basic base64(user:pat), in-memory only
    ok(f"authenticated as {user} (PAT never written to disk or URL)")

    # ---- 2. Fetch & verify tested commits EXIST remotely ----------------
    step(2, f"Fetch origin/{args.branch} and verify tested commits")
    sh(["git", "fetch", "--prune", REMOTE_NAME], auth=auth)
    remote_ref = f"{REMOTE_NAME}/{args.branch}"
    try:
        log = sh(["git", "log", "--format=%h", remote_ref])
    except SystemExit:
        die(f"{remote_ref} does not exist on origin.\n"
            f"Has the PR been pushed/merged? Try: git branch -r")
    short_hashes = set(log.split())
    missing = [c for c in REQUIRED_COMMITS if not any(h.startswith(c) for h in short_hashes)]
    if missing:
        die(f"Refusing to sync: {remote_ref} is MISSING tested commits {missing}.\n"
            f"This means your dev copy would NOT match what was verified in the\n"
            f"agent workspace. Merge/push first, then re-run this script.")
    head = sh(["git", "rev-parse", "--short", remote_ref])
    ok(f"{remote_ref} @ {head} contains all {len(REQUIRED_COMMITS)} tested commits")

    # ---- 3. Sync working tree (fast-forward, stash if dirty) ------------
    step(3, "Update local checkout")
    dirty = bool(sh(["git", "status", "--porcelain"]))
    stashed = False
    if dirty:
        sh(["git", "stash", "push", "-u", "-m", f"sync_dev auto-stash {int(time.time())}"])
        stashed = True
        print("  (local changes were stashed)")
    cur = sh(["git", "branch", "--show-current"])
    if cur == args.branch:
        sh(["git", "merge", "--ff-only", remote_ref])
    else:
        # Prefer being ON the synced branch so future pulls are trivial.
        local_exists = subprocess.run(
            ["git", "show-ref", "--verify", "--quiet", f"refs/heads/{args.branch}"]
        ).returncode == 0
        if local_exists:
            sh(["git", "checkout", args.branch])
            sh(["git", "merge", "--ff-only", remote_ref])
        else:
            sh(["git", "checkout", "-B", args.branch, remote_ref])
    ok(f"HEAD now at {sh(['git', 'rev-parse', '--short', 'HEAD'])} on {args.branch}")
    if stashed:
        r = subprocess.run(["git", "stash", "pop"], cwd=REPO_ROOT,
                           capture_output=True, text=True)
        if r.returncode != 0:
            print("\033[33m⚠ stash pop conflicted — your changes are still in "
                  "'git stash list'. Resolve manually; the sync itself succeeded.\033[0m")
        else:
            ok("restored your stashed local changes")

    # Sanity: P1 artifact must physically exist.
    if not os.path.isfile(os.path.join(REPO_ROOT, "web", "static", "shop.css")):
        die("web/static/shop.css missing after sync — checkout is inconsistent.")
    ok("web/static/shop.css present")

    # ---- 4. Go toolchain checks -----------------------------------------
    step(4, "Go build / vet / test (with executable TMPDIR)")
    if not os.path.isdir(TMPDIR_LOCAL):
        os.makedirs(TMPDIR_LOCAL)
    genv = {"TMPDIR": TMPDIR_LOCAL, "GOFLAGS": "-mod=mod"}

    ver = sh(["go", "version"])
    try:
        parts = ver.split("go1.")[1].split()[0].split(".")
        got = (1, int(parts[0]))
    except Exception:
        die(f"Could not parse go version: {ver!r}. Is Go on PATH?")
    if got < GO_MIN_VERSION:
        print(f"\033[33m⚠ go{got} < required {GO_MIN_VERSION[0]}.{GO_MIN_VERSION[1]} — "
              f"build may fail. Upgrade Go before trusting results.\033[0m")

    sh(["go", "build", "./..."], env=genv)
    ok("go build")
    sh(["go", "vet", "./..."], env=genv)
    ok("go vet")

    # The guardrail that has been failing for you — run it first, verbosely.
    out = subprocess.run(
        ["go", "test", "./internal/server/", "-run",
         "TestCSSClassContract|TestNoRetiredStylesheetRefs", "-v", "-count=1"],
        cwd=REPO_ROOT, env={**os.environ, **genv}, capture_output=True, text=True)
    print(out.stdout[-3000:] if out.stdout else "")
    if out.returncode != 0:
        print(out.stderr[-2000:], file=sys.stderr)
        die("CSS contract guardrail FAILED even after sync.\n"
            "Checklist:\n"
            "  1. `git log --oneline -5` — is 3f039b5 visible? If not, step 2 lied?\n"
            "     (impossible — it verifies); you likely have a SECOND clone.\n"
            "  2. Confirm you're editing the same dir this script runs from:\n"
            f"     {REPO_ROOT}\n"
            "  3. `go clean -cache` then re-run (stale compiled test binary).\n"
            "If all three pass and it still fails, paste the FULL output back to me.")
    ok("TestCSSClassContract + TestNoRetiredStylesheetRefs PASS")

    rc = subprocess.run(["go", "test", "./...", "-count=1"],
                        cwd=REPO_ROOT, env={**os.environ, **genv}).returncode
    if rc != 0:
        die("Full test suite FAILED — skipping Docker restart so dev keeps the "
            "last-known-good build.")
    ok("full go test ./... PASS")

    # ---- 5. Docker restart ----------------------------------------------
    if args.no_docker:
        step(5, "Docker restart SKIPPED (--no-docker)")
    else:
        step(5, "Restart containers")
        compose = None
        for cand in (["docker", "compose"], ["docker-compose"]):
            if subprocess.run([*cand, "version"], capture_output=True).returncode == 0:
                compose = cand
                break
        if compose is None:
            print("\033[33m⚠ docker compose not found — restarting via systemctl if available\033[0m")
            subprocess.run(["sudo", "systemctl", "restart", "onionshop"], capture_output=True)
        else:
            # app: templates are parsed at startup -> MUST restart.
            # nginx: serves bind-mounted /var/www/static with no-cache+ETag ->
            #        reload picks up new files immediately.
            sh(compose + ["restart", "app"])
            subprocess.run(compose + ["exec", "-T", "nginx", "nginx", "-s", "reload"],
                           capture_output=True)
            ok("app restarted, nginx reloaded")

    # ---- 6. Summary ------------------------------------------------------
    el = time.time() - t0
    print(f"\n\033[32m\033[1m✔ SYNC COMPLETE in {el:.1f}s\033[0m")
    print(f"  branch : {args.branch} @ {sh(['git','rev-parse','--short','HEAD'])}")
    print(f"  tested : build ✓  vet ✓  contract ✓  full suite ✓")
    print(f"  browser: hard-refresh (Ctrl+Shift+R) — ETag handles the rest.")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        die("aborted by user")
