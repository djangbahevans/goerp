#!/usr/bin/env bash
# Provision a dev tenant with a usable admin login and enable modules for it.
# Needs `make infra` and a running `make engine`. Run from the repo root.
#
#   .claude/skills/run-goerp/bootstrap-tenant.sh <slug> <admin-email> <password> [module...]
#
# Afterwards sign in at http://<slug>.localhost:5173 (with `make shell` running).
# A module must already be built into GOERP_MODULE_DIR (`make module`) and
# loaded by the engine for enabling it to show anything.
#
# Users are global across tenants: pick an email no other tenant uses, or the
# account keeps its existing password and <password> is ignored.
set -euo pipefail

usage="usage: bootstrap-tenant.sh <slug> <admin-email> <password> [module...]"
slug=${1:?$usage}
email=${2:?$usage}
password=${3:?$usage}
shift 3

name_re='^[a-z0-9][a-z0-9_-]*$'
[[ $slug =~ $name_re ]] || { echo "invalid slug: $slug" >&2; exit 2; }
[[ $email =~ ^[^\'\"[:space:]]+@[^\'\"[:space:]]+$ ]] || { echo "invalid email: $email" >&2; exit 2; }
for module in "$@"; do [[ $module =~ $name_re ]] || { echo "invalid module name: $module" >&2; exit 2; }; done

admin=(go run ./cmd/goerp --admin-url http://localhost:8081 --admin-token "${GOERP_ADMIN_TOKEN:-dev-admin-token}")
psql() { docker compose -f compose.dev.yml exec -T postgres psql -U goerp -d goerp_dev -At -c "$1"; }
redis() { docker compose -f compose.dev.yml exec -T redis redis-cli "$@"; }

if [ -n "$(psql "select 1 from system.users where email = '$email' and password_hash is not null")" ]; then
  echo "note: $email already has an account; it keeps its existing password" >&2
fi

if [ -z "$(psql "select 1 from system.tenants where slug = '$slug'")" ]; then
  "${admin[@]}" tenant create "$slug" --admin-email "$email"
fi
echo "waiting for tenant $slug to become active..."
for _ in $(seq 1 150); do
  [ "$(psql "select status from system.tenants where slug = '$slug'")" = active ] && break
  sleep 2
done
[ "$(psql "select status from system.tenants where slug = '$slug'")" = active ] || { echo "tenant $slug never became active" >&2; exit 1; }
tenant_id=$(psql "select id from system.tenants where slug = '$slug'")

# Accept this tenant's invite for the admin, if one is waiting in Mailpit and
# still unused. Re-running the script after a failed acceptance retries it.
echo "accepting the admin invite from Mailpit..."
python3 - "$slug" "$email" "$password" <<'EOF'
import json, re, sys, time, urllib.error, urllib.parse, urllib.request

slug, email, password = sys.argv[1:]
mailpit = "http://localhost:8025/api/v1"

def invite_token():
    query = urllib.parse.quote(f"to:{email}")
    for message in json.load(urllib.request.urlopen(f"{mailpit}/search?query={query}"))["messages"]:
        body = json.load(urllib.request.urlopen(f"{mailpit}/message/{message['ID']}"))["Text"]
        match = re.search(r"accept-invite\?token=([0-9a-f]+)&tenant=" + re.escape(slug) + r"\b", body)
        if match:
            return match.group(1)
    return None

token = None
for _ in range(30):
    token = invite_token()
    if token:
        break
    time.sleep(1)
if not token:
    sys.exit(f"no invite email for {email} to tenant {slug} in Mailpit")

request = urllib.request.Request(
    "http://localhost:8080/auth/accept-invite",
    data=json.dumps({"token": token, "tenant": slug, "password": password, "device_id": "run-goerp-bootstrap"}).encode(),
    headers={"Host": f"{slug}.localhost", "Content-Type": "application/json"},
    method="POST",
)
try:
    urllib.request.urlopen(request)
except urllib.error.HTTPError as err:
    detail = err.read().decode(errors="replace")
    if "already" in detail or "used" in detail or "invalid" in detail:
        print(f"invite not accepted ({err.code} {detail.strip()}); assuming it was accepted on an earlier run")
    else:
        sys.exit(f"accepting the invite failed: {err.code} {detail.strip()}")
EOF

for module in "$@"; do
  psql "insert into system.tenant_entitlement_overrides (tenant_id, feature, value, reason)
        values ('$tenant_id', 'module.$module', 'true', 'local dev')
        on conflict (tenant_id, feature) do update set value = 'true'" >/dev/null
  echo "enabled module $module"
done
if [ $# -gt 0 ]; then
  # The engine caches entitlements in Redis; dropping the key applies the
  # overrides on the next request, with no engine restart.
  redis del "tenant:entitlements:$tenant_id" >/dev/null
fi

echo "tenant $slug ready: http://$slug.localhost:5173 as $email"
