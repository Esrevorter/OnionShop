#!/usr/bin/env python3
"""
deploy.py - full deployment helper for Onionshop.

Verified against this repository (docker-compose.yml, install/install.sh,
Makefile, Dockerfile). Key facts that drive the design:

  * There is NO db/bootstrap_app_role.sql in this repo. The app connects to
    Postgres as the single ${DB_USER} role defined in .env / compose. So no
    role-bootstrap step is attempted; instead we verify the DB is reachable.
  * DATABASE_URL is NOT stored in .env. Compose builds it internally from
    DB_USER / DB_PASSWORD / DB_NAME (postgres://...@postgres:5432/...).
    Host-side psql cannot reach the DB anyway (internal-only network), so all
    SQL runs through `docker compose exec postgres psql`.
  * Migrations are applied with plain psql + a schema_migrations bookkeeping
    table, exactly mirroring install/install.sh phase 4 (no host `migrate`
    binary and no DATABASE_URL required).
  * HONEYPOT_FIELD generation already exists in install/install.sh; this
    script only tops up missing secrets if you skipped install.sh.
  * go.mod declares `go 1.25.0` (required by pgx v5.9.x). Older host Go
    toolchains (<1.21) cannot even parse that directive and abort with
    "invalid go version". Therefore local build/test/vet are SKIPPED unless
    the host `go` is >= 1.21; compilation is verified via the container
    image build (`docker compose build`, which uses golang:1.25-alpine per
    the Dockerfile).
  * The health endpoint is /healthz (router.go), not /health.

Usage:
  python3 deploy.py                 # full run: pull, build, test, migrate, restart, verify
  python3 deploy.py --no-pull       # skip git pull
  python3 deploy.py --skip-tests    # skip go test
  python3 deploy.py --db-only       # only check DB readiness + apply migrations
  python3 deploy.py --restart-only  # only rebuild/restart containers + verify
"""

import argparse
import os
import re
import secrets
import shlex
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def run(cmd, check=True, capture=False):
    """Run a shell command from the repo root."""
    print(f"  $ {cmd}")
    return subprocess.run(
        cmd, shell=True, cwd=ROOT, check=check,
        capture_output=capture, text=True,
    )


def sh_out(cmd):
    """Run a command, return stripped stdout (empty string on failure)."""
    try:
        return run(cmd, capture=True).stdout.strip()
    except subprocess.CalledProcessError:
        return ""


def load_env():
    """Parse .env into os.environ (existing env vars win)."""
    env_path = ROOT / ".env"
    if not env_path.exists():
        die(".env not found. Run install/install.sh first (it generates .env).")
    for line in env_path.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            os.environ.setdefault(k.strip(), v.strip().strip("'\""))


def die(msg):
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(1)


COMPOSE = None  # resolved lazily: "docker compose" or "docker-compose"


def compose_cmd():
    global COMPOSE
    if COMPOSE is None:
        probe = run("docker compose version", check=False, capture=True)
        COMPOSE = "docker compose" if probe.returncode == 0 else "docker-compose"
    return COMPOSE


def compose(*args, check=True):
    return run(f"{compose_cmd()} {' '.join(args)}", capture=True, check=check)


# --- Steps ---------------------------------------------------------------

def step_preflight(skip_pull):
    print("\n[1/6] Pre-flight checks")
    if not (ROOT / ".git").exists():
        die("Not a git repository.")
    status = sh_out("git status --porcelain")
    if status:
        print("  Warning: working tree has uncommitted changes:")
        for line in status.splitlines()[:10]:
            print(f"    {line}")
        if input("  Continue? (y/n): ").lower() != "y":
            sys.exit(1)
    if not skip_pull:
        branch = sh_out("git rev-parse --abbrev-ref HEAD") or "main"
        run(f"git fetch origin && git pull origin {branch}")
    for tool in ("go", "docker"):
        if run(f"which {tool}", check=False, capture=True).returncode != 0:
            die(f"Required tool '{tool}' not found.")
    if run("which psql", check=False, capture=True).returncode != 0:
        print("  Note: no local psql needed; SQL runs via docker compose exec.")
    print("  OK")


def go_version_ok():
    """Return True if host `go` can handle this module's go.mod directive.

    Go < 1.21 fails to *parse* 'go 1.25.0' in go.mod with
    "invalid go version '1.25.0': must match format 1.23", aborting the
    whole build. Go >= 1.21 understands three-part versions and will
    auto-download the required toolchain (or use a newer installed one).
    """
    out = sh_out("go env GOVERSION")  # e.g. "go1.19.8" or "go1.25.0"
    m = re.match(r"go(\d+)\.(\d+)", out)
    if not m:
        return False
    major, minor = int(m.group(1)), int(m.group(2))
    return (major, minor) >= (1, 21)


def step_build_test(skip_tests):
    print("\n[2/6] Build & test")
    host_go = sh_out("go version")
    if go_version_ok():
        run("go build ./...")
        run("go vet ./...")
        if not skip_tests:
            run("go test ./...")
        else:
            print("  (tests skipped)")
    else:
        # Do NOT touch go.mod to "fix" this - pgx v5.9.x genuinely requires
        # go >= 1.25.0. The container build (golang:1.25-alpine, see
        # Dockerfile) compiles + tests the code during `compose up --build`,
        # so we verify compilation there instead of failing on an old host
        # toolchain.
        print(f"  Note: {host_go} is too old for this module "
              "(go.mod needs >= 1.21 to parse; project targets 1.25).")
        print("  Skipping local build/test/vet; compilation will be verified "
              "via the Docker image build (golang:1.25).")
        print("  Optional: install Go >= 1.25 locally, or set "
              "GOTOOLCHAIN=auto with network access.")
    print("  OK")


def psql_script(text):
    """Execute a full SQL script inside the postgres container.

    Method: pipe the script through psql's STDIN with `psql -f -`, exactly
    like install/install.sh does (`PSQL -q < "$f"`). This works even when
    the container root filesystem is marked read-only (which breaks
    `docker compose cp`).

    Why `-f -` instead of bare stdin: without an explicit `-f -`, psql
    reads interactive-mode rules from the pipe and mis-parses some files,
    producing e.g. `error: invalid command \n--`. We also normalize the
    text here (strip CR, drop literal backslash-n sequences that can leak
    in from JSON-quoted edits) so the bytes reaching psql are clean SQL.
    """
    user = os.environ.get("DB_USER") or die("DB_USER missing from .env")
    name = os.environ.get("DB_NAME") or die("DB_NAME missing from .env")
    # Normalize line endings and defend against corrupted content.
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    if "\\n" in text:
        # A literal two-character backslash-n sequence means the file was
        # damaged (e.g. saved from a JSON-escaped string). Real SQL files
        # never contain it outside string literals; migration files don't.
        print("  note: normalizing literal \\n sequences found in SQL input")
        text = text.replace("\\n", "\n")
    proc = subprocess.run(
        f'{compose_cmd()} exec -T postgres psql -v ON_ERROR_STOP=1 '
        f'-U {user} -d {name} -f -',
        shell=True, cwd=ROOT, input=text, text=True, capture_output=True)
    if proc.returncode != 0:
        print(proc.stdout)
        die(f"psql failed: {proc.stderr.strip()}")
    return proc.stdout


def psql(sql, want_stderr=False):
    """Run SQL inside the postgres container as ${DB_USER}/${DB_NAME}.

    Uses `-c` for single statements (install.sh style) and falls back to
    `psql_script` (-f via temp file) for multi-line scripts.
    Returns stdout on success. On failure: dies with the psql error unless
    want_stderr=True, in which case it returns (stdout, stderr) so the caller
    can decide how to handle it.
    """
    user = os.environ.get("DB_USER") or die("DB_USER missing from .env")
    name = os.environ.get("DB_NAME") or die("DB_NAME missing from .env")
    if "\n" in sql.strip():
        # Multi-line script: use file-based execution to avoid stdin issues.
        out = psql_script(sql)
        return (out, "") if want_stderr else out
    proc = subprocess.run(
        f'{compose_cmd()} exec -T postgres psql -v ON_ERROR_STOP=1 '
        f'-U {user} -d {name} -c {shlex.quote(sql)}',
        shell=True, cwd=ROOT, capture_output=True, text=True,
    )
    if proc.returncode != 0:
        if want_stderr:
            return proc.stdout, proc.stderr
        print(proc.stdout)
        die(f"psql failed: {proc.stderr.strip()}")
    if want_stderr:
        return proc.stdout, ""
    return proc.stdout


def psql_val(sql, default=""):
    """Run a scalar query via the postgres container and return just the value.

    Uses `psql -t -A` (tuples-only, unaligned) so stdout is exactly the value.
    On failure returns `default` and notes the error on stderr; callers decide
    whether absence matters.
    """
    user = os.environ.get("DB_USER") or die("DB_USER missing from .env")
    name = os.environ.get("DB_NAME") or die("DB_NAME missing from .env")
    proc = subprocess.run(
        f'{compose_cmd()} exec -T postgres psql -v ON_ERROR_STOP=1 -t -A '
        f'-U {user} -d {name}',
        shell=True, cwd=ROOT, input=sql, text=True, capture_output=True,
    )
    if proc.returncode != 0:
        print(f"  note: query failed ({proc.stderr.strip()[:200]})",
              file=sys.stderr)
        return default
    out = proc.stdout.strip()
    return out if out else default


def repair_account_balances_view():
    """Idempotent self-heal for the account_balances view drift (AUDIT.md §2).

    On databases where 0009 renamed ledger_entries.amount_sat ->
    amount_piconero but left the old view in place, the live view still
    exposes column "balance_sat". PostgreSQL refuses to rename a view column
    via CREATE OR REPLACE VIEW ("cannot change name of view column ..."), so
    migration 0023 must DROP the view first -- which the committed 0023 does.

    But if an OLDER copy of 0023 (without the DROP) ran here already, its
    explicit BEGIN/COMMIT block got committed while deploy.py's bookkeeping
    INSERT rolled back: version 23 is unrecorded, every retry fails with the
    same error, and re-running even the fixed 0023 would abort at the CREATE
    because psql replays the stale BEGIN...COMMIT verbatim. So before applying
    any migration we drop + recreate the view ourselves, as plain autocommit
    statements (no transaction that could half-apply). This is safe because no
    Go code reads the view (verified: zero references under internal/ and
    cmd/), and it makes fresh, drifted, and half-applied databases all
    converge to the correct definition.
    """
    exists = psql_val(
        "SELECT count(*) FROM information_schema.views "
        "WHERE table_name = 'account_balances';", default="0")
    if exists.strip() != "1":
        return  # view absent (fresh DB pre-schema.sql): nothing to repair
    cols = psql_val(
        "SELECT COALESCE(string_agg(column_name, ','), '') "
        "FROM information_schema.columns "
        "WHERE table_name = 'account_balances';")
    if "balance_piconero" in cols:
        return  # already correct, nothing to repair
    print(f"  repairing stale account_balances view (columns: {cols or '?'})")
    user = os.environ.get("DB_USER")
    name = os.environ.get("DB_NAME")
    proc = subprocess.run(
        f'{compose_cmd()} exec -T postgres psql -v ON_ERROR_STOP=1 '
        f'-U {user} -d {name}',
        shell=True, cwd=ROOT, text=True, capture_output=True,
        input="DROP VIEW IF EXISTS account_balances;\n")
    if proc.returncode != 0:
        die(f"could not drop stale view account_balances "
            f"(dependent objects? run manually): {proc.stderr.strip()}")
    psql(
        "CREATE OR REPLACE VIEW account_balances AS "
        "SELECT account_id, "
        "COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'debit'),  0) "
        "- COALESCE(SUM(amount_piconero) FILTER (WHERE direction = 'credit'), 0) "
        "AS balance_piconero "
        "FROM ledger_entries GROUP BY account_id;")
    print("  view repaired.")


def step_db_ready():
    print("\n[3/6] Database readiness")
    # Bring up only postgres/redis if needed, then wait for pg_isready,
    # same strategy as install/install.sh phase 4.
    compose("up", "-d", "postgres", "redis", check=False)
    user = os.environ.get("DB_USER", "onionshop")
    ready = False
    for _ in range(30):
        r = subprocess.run(
            f'{compose_cmd()} exec -T postgres pg_isready -U {user}',
            shell=True, cwd=ROOT, capture_output=True, text=True)
        if r.returncode == 0:
            ready = True
            break
        time.sleep(2)
    if not ready:
        die("postgres did not become ready within 60s (check `docker compose logs postgres`).")
    out = psql("SELECT current_database();").strip().splitlines()
    print(f"  Connected to database: {out[-1] if out else '?'}")


def step_migrations():
    print("\n[4/6] Apply migrations (psql + schema_migrations, mirrors install.sh)")
    psql("CREATE TABLE IF NOT EXISTS schema_migrations ("
         "version bigint PRIMARY KEY, dirty boolean NOT NULL DEFAULT false, "
         "applied_at timestamptz NOT NULL DEFAULT now());")
    maxv_raw = psql("SELECT COALESCE(max(version),0) FROM schema_migrations;")
    maxv = int(re.search(r"\d+", maxv_raw).group())
    files = sorted((ROOT / "db" / "migrations").glob("*.up.sql"))
    if not files:
        die("No migration files found under db/migrations/.")
    # Self-heal the known view drift BEFORE running anything, so a half-applied
    # older 0023 (body committed, version row rolled back) cannot wedge retries.
    repair_account_balances_view()
    applied = 0
    for f in files:
        version = int(f.name.split("_", 1)[0])
        if version <= maxv:
            continue  # already recorded as applied
        print(f"  applying {f.name}")
        psql_script(f.read_text())
        psql(f"INSERT INTO schema_migrations(version) VALUES ({version}) "
             "ON CONFLICT DO NOTHING;")
        applied += 1
    print(f"  Schema at version {maxv}; {applied} new migration(s) applied."
          if applied else f"  Schema already at version {maxv}; nothing to do.")


def step_env_hygiene():
    print("\n[5/6] Environment hygiene (.env)")
    env_path = ROOT / ".env"
    keys = {}
    for line in env_path.read_text().splitlines():
        if "=" in line and not line.strip().startswith("#"):
            k, v = line.split("=", 1)
            keys[k.strip()] = v.strip()
    additions = []
    # Only secrets used by compose interpolation. DATABASE_URL is intentionally
    # NOT written: compose constructs it from DB_* (see header comment).
    if not keys.get("HONEYPOT_FIELD"):
        val = secrets.token_hex(6)
        additions.append(f"HONEYPOT_FIELD={val}")
        print(f"  Generated HONEYPOT_FIELD={val}")
    if not keys.get("SESSION_HASH_KEY"):
        additions.append(f"SESSION_HASH_KEY={secrets.token_hex(32)}")
        print("  Generated SESSION_HASH_KEY")
    for req in ("DB_USER", "DB_PASSWORD", "DB_NAME", "REDIS_PASSWORD"):
        if not keys.get(req):
            die(f"{req} missing from .env - run install/install.sh.")
    if additions:
        with open(env_path, "a") as fh:
            fh.write("\n# Added by deploy.py\n")
            for a in additions:
                fh.write(a + "\n")
        print("  .env updated.")
    else:
        print("  .env complete.")


def step_restart(restart_only):
    print("\n[6/6] Restart services")
    if restart_only:
        compose("restart")
    else:
        compose("up", "-d", "--build")
    print("  Services started.")


def step_verify():
    print("\n[Verify] Container health & HTTP")
    out = compose("ps").stdout
    print(out)
    base = os.environ.get("APP_URL", "http://127.0.0.1:8080")
    ok = False
    for _ in range(15):
        code = sh_out(f"curl -sk -o /dev/null -w '%{{http_code}}' {base}/healthz")
        if code == "200":
            ok = True
            break
        time.sleep(2)
    print("  /healthz -> 200 OK" if ok else
          "  WARNING: /healthz did not return 200 (check `docker compose logs app nginx`)")
    code = sh_out(f"curl -sk -o /dev/null -w '%{{http_code}}' {base}/")
    print(f"  / -> HTTP {code}")


# --- Main ----------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description="Deploy Onionshop (compose-based)")
    ap.add_argument("--no-pull", action="store_true", help="Skip git pull")
    ap.add_argument("--skip-tests", action="store_true", help="Skip go test")
    ap.add_argument("--restart-only", action="store_true", help="Restart + verify only")
    ap.add_argument("--db-only", action="store_true", help="DB readiness + migrations only")
    args = ap.parse_args()

    if not (ROOT / "docker-compose.yml").exists():
        die("Run this script from the OnionShop repo root.")

    load_env()

    if args.db_only:
        step_db_ready()
        step_migrations()
        return
    if args.restart_only:
        step_restart(restart_only=True)
        step_verify()
        return

    step_preflight(skip_pull=args.no_pull)
    step_build_test(skip_tests=args.skip_tests)
    step_db_ready()
    step_migrations()
    step_env_hygiene()
    step_restart(restart_only=False)
    step_verify()
    print("\nDeployment complete.")


if __name__ == "__main__":
    main()
