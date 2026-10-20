#!/usr/bin/env bash
# End-to-end check of the three-state limit model, the tenant tree, the
# per-model quota endpoint and the scoped administrator.
#
# It runs against a live gateway on 127.0.0.1:8080 and mutates real rows, so it
# is a local acceptance script, not a unit test. It is idempotent: a tenant that
# already exists is looked up by code rather than created again, so a second run
# reports the same numbers instead of failing on a unique violation.
#
# The exit code is the number of failed checks.
set -u

BASE=http://127.0.0.1:8080
PY="C:/Users/le/.workbuddy-ai/binaries/python/versions/3.13.12/python.exe"
FAILS=0

# A dedicated alias keeps the script from touching a real model's caps. Rows for
# it are removed on exit as well as at the end of section 7, so a failed check
# halfway through does not leave residue in the campus pool.
PROBE=e2e-three-state-probe
TOKEN= ROOT= CHILD= GRAND=
SCOPED_USER_SEEDED=0

cleanup_scoped_user() {
  # The scoped administrator is seeded into the database, not created through
  # the API, so nothing else removes it. Leaving the account behind would keep
  # a tenant_admin with the campus-admin password alive after the test.
  docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_admin_user WHERE username='ops-cs'" >/dev/null 2>&1 || true
}

cleanup() {
  cleanup_scoped_user
  [ -n "$TOKEN" ] || return 0
  local t
  for t in "$CHILD" "$GRAND"; do
    [ -n "$t" ] || continue
    curl -s --noproxy '*' -o /dev/null -X DELETE \
      "$BASE/admin/model-quotas?tenant_id=$t&model_alias=$PROBE" \
      -H "Authorization: Bearer $TOKEN"
  done
  curl -s --noproxy '*' -o /dev/null -X DELETE \
    "$BASE/admin/model-quotas?model_alias=$PROBE" \
    -H "Authorization: Bearer $TOKEN"
}
trap cleanup EXIT

json() { "$PY" -c "import json,sys;d=json.load(sys.stdin);print(eval(sys.argv[1],{'d':d}))" "$1"; }

check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    printf '  PASS  %-46s %s\n' "$1" "$3"
  else
    printf '  FAIL  %-46s want=%s got=%s\n' "$1" "$2" "$3"
    FAILS=$((FAILS + 1))
  fi
}

# req runs in a command substitution, so the status code travels through a file
# rather than a variable -- a subshell cannot assign back to its caller.
req() { # req <method> <path> <token> [body] -> body on stdout, status in /tmp/e2e.code
  local m=$1 p=$2 t=$3 b=${4:-}
  local args=(-s --noproxy '*' -o /tmp/e2e.out -w '%{http_code}' -X "$m" "$BASE$p")
  [ -n "$t" ] && args+=(-H "Authorization: Bearer $t")
  [ -n "$b" ] && args+=(-H 'Content-Type: application/json' -d "$b")
  curl "${args[@]}" > /tmp/e2e.code
  cat /tmp/e2e.out
}

code() { cat /tmp/e2e.code; }

ensure_tenant() { # ensure_tenant <code> <name> <kind> [parent] -> id
  local code_=$1 name=$2 kind=$3 parent=${4:-}
  local id body
  id=$(req GET /admin/tenants "$TOKEN" | json "[t['id'] for t in d['data'] if t['code']=='$code_'][0] if any(t['code']=='$code_' for t in d['data']) else ''")
  if [ -n "$id" ]; then
    echo "$id"
    return
  fi
  if [ -n "$parent" ]; then
    body="{\"code\":\"$code_\",\"name\":\"$name\",\"status\":1,\"kind\":\"$kind\",\"parent_id\":\"$parent\"}"
  else
    body="{\"code\":\"$code_\",\"name\":\"$name\",\"status\":1,\"kind\":\"$kind\"}"
  fi
  req POST /admin/tenants "$TOKEN" "$body" | json "d['id']"
}

echo "== 0. login =="
TOKEN=$(curl -s --noproxy '*' -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"Campus#2026Gate"}' | json "d['access_token']")
[ -z "$TOKEN" ] && { echo "  FAIL  login returned no token"; exit 1; }
echo "  PASS  logged in"

echo "== 1. tenant tree =="
ROOT=$(ensure_tenant xust 西安科技大学 school)
check "root resolved" 1 "$([ -n "$ROOT" ] && echo 1 || echo 0)"
CHILD=$(ensure_tenant cs 计算机学院 dept "$ROOT")
check "child resolved" 1 "$([ -n "$CHILD" ] && echo 1 || echo 0)"
GRAND=$(ensure_tenant cs-ai 人工智能系 dept "$CHILD")
check "grandchild resolved" 1 "$([ -n "$GRAND" ] && echo 1 || echo 0)"
TREE=$(req GET /admin/tenants "$TOKEN")
check "child depth" 1 "$(echo "$TREE" | json "[t['depth'] for t in d['data'] if t['id']=='$CHILD'][0]")"
check "grandchild path is 3 segments" 3 "$(echo "$TREE" | json "[len(t['path'].strip('/').split('/')) for t in d['data'] if t['id']=='$GRAND'][0]")"
check "root has no parent" None "$(echo "$TREE" | json "[t['parent_id'] for t in d['data'] if t['id']=='$ROOT'][0]")"

echo "== 2. three-state allocation =="
OUT=$(req PUT "/admin/tenants/$ROOT/allocation" "$TOKEN" '{"daily_token_limit":5000000,"rpm_limit":600}')
check "root PUT allocation" 200 "$(code)"
check "root daily is a cap" 5000000 "$(echo "$OUT" | json "d['daily_token_limit']")"
check "root monthly inherits (null)" None "$(echo "$OUT" | json "d['monthly_token_limit']")"

# The case that used to fail: every field omitted but one, so monthly_budget
# arrives as JSON null while the column is NOT NULL.
OUT=$(req PUT "/admin/tenants/$CHILD/allocation" "$TOKEN" '{"daily_token_limit":null,"rpm_limit":0}')
check "child PUT allocation (null budget)" 200 "$(code)"
check "child daily inherits (null)" None "$(echo "$OUT" | json "d['daily_token_limit']")"
check "child rpm explicitly unlimited" 0 "$(echo "$OUT" | json "d['rpm_limit']")"

echo "== 3. inheritance resolution =="
# Reset the leaf first. An earlier run of this script allocated to it, and a
# leaf that declares its own cap correctly inherits nothing -- the check below
# is about the path, so the path has to start from "declares nothing".
req PUT "/admin/tenants/$GRAND/allocation" "$TOKEN" '{}' >/dev/null
check "leaf reset to inherit" 200 "$(code)"
U=$(req GET "/admin/tenants/$GRAND/usage" "$TOKEN")
check "usage HTTP" 200 "$(code)"
check "grandchild daily resolves to root's 5,000,000" 5000000 "$(echo "$U" | json "d['effective']['daily_token_limit']")"
# The child declared rpm 0 ("this level adds no cap"). That does not lift the
# root's 600: enforcement is inclusive, so the effective view must report 600.
check "grandchild rpm keeps root's 600" 600 "$(echo "$U" | json "d['effective']['rpm_limit']")"
check "grandchild monthly is unlimited" 0 "$(echo "$U" | json "d['effective']['monthly_token_limit']")"

# A descendant may tighten a ceiling but never widen one. Declaring a larger
# number must leave the root's cap in force, in both enforcement and display.
req PUT "/admin/tenants/$GRAND/allocation" "$TOKEN" '{"daily_token_limit":999999999}' >/dev/null
check "leaf may declare a larger cap" 200 "$(code)"
U=$(req GET "/admin/tenants/$GRAND/usage" "$TOKEN")
check "root's 5,000,000 still wins" 5000000 "$(echo "$U" | json "d['effective']['daily_token_limit']")"
req PUT "/admin/tenants/$GRAND/allocation" "$TOKEN" '{}' >/dev/null

echo "== 4. per-model quota =="
OUT=$(req PUT /admin/model-quotas "$TOKEN" "{\"tenant_id\":\"$CHILD\",\"model_alias\":\"$PROBE\",\"daily_token_limit\":100000}")
check "PUT model quota" 200 "$(code)"
check "quota daily limit" 100000 "$(echo "$OUT" | json "d['daily_token_limit']")"
check "quota listed" 1 "$(req GET /admin/model-quotas "$TOKEN" | json "1 if any(q['tenant_id']=='$CHILD' and q['model_alias']=='$PROBE' for q in d['data']) else 0")"
OUT=$(req PUT /admin/model-quotas "$TOKEN" "{\"tenant_id\":null,\"model_alias\":\"$PROBE\",\"daily_token_limit\":9000000}")
check "campus pool quota" 200 "$(code)"
check "pool row has no tenant" None "$(echo "$OUT" | json "d['tenant_id']")"

echo "== 5. validation =="
OUT=$(req PUT "/admin/tenants/$CHILD/allocation" "$TOKEN" '{"tpm_limit":-1}')
check "negative tpm rejected" 400 "$(code)"
check "message names tpm_limit" 1 "$(echo "$OUT" | json "1 if 'tpm_limit' in d['error']['message'] else 0")"
OUT=$(req PUT /admin/model-quotas "$TOKEN" '{"model_alias":"","daily_token_limit":1}')
check "empty alias rejected" 400 "$(code)"
OUT=$(req PUT /admin/model-quotas "$TOKEN" "{\"tenant_id\":\"$CHILD\",\"model_alias\":\"$PROBE\"}")
check "quota with no limit rejected" 400 "$(code)"

echo "== 5b. error semantics =="
# A code collision used to be reported as 404 "tenant was not found", which
# sends the operator looking for a row that is right in front of them.
OUT=$(req PUT "/admin/tenants/$CHILD" "$TOKEN" '{"code":"xust","name":"重名测试","status":1}')
check "duplicate code on update" 409 "$(code)"
check "duplicate code names the field" 1 "$(echo "$OUT" | json "1 if 'code' in d['error']['message'] else 0")"
OUT=$(req POST /admin/tenants "$TOKEN" '{"code":"xust","name":"dup","status":1}')
check "duplicate code on create" 409 "$(code)"
OUT=$(req POST /admin/tenants "$TOKEN" '{"code":"ghost","name":"ghost","status":1,"parent_id":"11111111-1111-1111-1111-111111111111"}')
check "missing parent rejected" 400 "$(code)"

echo "== 6. scoped administrator =="
# There is no create-user endpoint (public registration is off in this
# deployment and /admin/users is read-only), so the account is seeded through
# the database and then promoted through the API -- which is the part under
# test. Copying the existing password hash keeps the login honest without
# needing a second known credential.
docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
  "DELETE FROM gateway_admin_user WHERE username='ops-cs'" >/dev/null 2>&1
docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
  "INSERT INTO gateway_admin_user (username, display_name, password_hash, roles, status, auth_version)
   SELECT 'ops-cs', '计算机学院管理员', password_hash, '[\"member\"]'::jsonb, 1, 1
   FROM gateway_admin_user WHERE username='campus-admin'" >/dev/null
OPS=$(docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -tAc \
  "SELECT id::text FROM gateway_admin_user WHERE username='ops-cs'" | tr -d '[:space:]')
if [ -z "$OPS" ]; then
  echo "  SKIP  could not seed ops-cs; scoped admin not exercised"
else
  SCOPED_USER_SEEDED=1
  OUT=$(req PATCH "/admin/users/$OPS" "$TOKEN" "{\"roles\":[\"tenant_admin\"],\"scope_tenant_id\":\"$CHILD\"}")
  check "promote to scoped tenant_admin" 200 "$(code)"
  check "scope recorded" "$CHILD" "$(echo "$OUT" | json "d['data']['scope_tenant_id']")"
  OPS_TOKEN=$(curl -s --noproxy '*' -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
    -d '{"username":"ops-cs","password":"Campus#2026Gate"}' | json "d['access_token']")
  check "scoped admin can log in" 1 "$([ -n "$OPS_TOKEN" ] && echo 1 || echo 0)"
  SCOPED=$(req GET /admin/tenants "$OPS_TOKEN")
  check "scoped list hides the root" 0 "$(echo "$SCOPED" | json "1 if any(t['id']=='$ROOT' for t in d['data']) else 0")"
  check "scoped list shows own subtree" 1 "$(echo "$SCOPED" | json "1 if any(t['id']=='$CHILD' for t in d['data']) else 0")"
  req PUT "/admin/tenants/$CHILD/allocation" "$OPS_TOKEN" '{"daily_token_limit":99999999}' >/dev/null
  check "scoped may not raise its own ceiling" 403 "$(code)"
  req PUT "/admin/tenants/$GRAND/allocation" "$OPS_TOKEN" '{"daily_token_limit":1000}' >/dev/null
  check "scoped may allocate to a descendant" 200 "$(code)"
  req PUT "/admin/tenants/$ROOT/allocation" "$OPS_TOKEN" '{"daily_token_limit":1}' >/dev/null
  check "scoped may not touch an ancestor" 403 "$(code)"
  req GET /admin/status "$OPS_TOKEN" >/dev/null
  check "scoped has no dashboard" 403 "$(code)"
  req PUT /admin/model-quotas "$OPS_TOKEN" "{\"tenant_id\":null,\"model_alias\":\"$PROBE\",\"daily_token_limit\":1}" >/dev/null
  check "scoped may not write the campus pool" 403 "$(code)"
  # The scoped administrator's own rows are the ceilings their parent set.
  # Replacing or deleting one would raise the effective value, so both must be
  # refused while the descendant write below is allowed.
  req PUT /admin/model-quotas "$OPS_TOKEN" "{\"tenant_id\":\"$CHILD\",\"model_alias\":\"$PROBE\",\"daily_token_limit\":1}" >/dev/null
  check "scoped may not write its own model quota" 403 "$(code)"
  req DELETE "/admin/model-quotas?tenant_id=$CHILD&model_alias=$PROBE" "$OPS_TOKEN" >/dev/null
  check "scoped may not delete its own model quota" 403 "$(code)"
  check "own quota survived the refused delete" 1 "$(req GET /admin/model-quotas "$OPS_TOKEN" | json "1 if any(q['tenant_id']=='$CHILD' and q['model_alias']=='$PROBE' for q in d['data']) else 0")"
  req PUT /admin/model-quotas "$OPS_TOKEN" "{\"tenant_id\":\"$GRAND\",\"model_alias\":\"$PROBE\",\"daily_token_limit\":50000}" >/dev/null
  check "scoped may write a descendant's model quota" 200 "$(code)"
  req GET /admin/users "$OPS_TOKEN" >/dev/null
  check "scoped cannot manage users" 403 "$(code)"
fi

echo "== 7. cleanup =="
# Section 4 deliberately leaves the pool and child rows in place so the scoped
# administrator checks above can try to touch them. Remove every probe row here
# and assert it is gone; leaving the campus pool row behind would cap a real
# model for every tenant after the script ended.
req DELETE "/admin/model-quotas?tenant_id=$CHILD&model_alias=$PROBE" "$TOKEN" >/dev/null
check "DELETE child probe quota" 200 "$(code)"
req DELETE "/admin/model-quotas?tenant_id=$GRAND&model_alias=$PROBE" "$TOKEN" >/dev/null
check "DELETE grandchild probe quota" 200 "$(code)"
OUT=$(req DELETE "/admin/model-quotas?model_alias=$PROBE" "$TOKEN")
check "DELETE campus pool probe quota" 200 "$(code)"
check "pool probe row was removed" True "$(echo "$OUT" | json "d['removed']")"
check "no probe rows remain" 0 "$(req GET /admin/model-quotas "$TOKEN" | json "1 if any(q['model_alias']=='$PROBE' for q in d['data']) else 0")"
if [ "$SCOPED_USER_SEEDED" = 1 ]; then
  cleanup_scoped_user
  check "scoped test account removed" 0 "$(docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -tAc \
    "SELECT count(*) FROM gateway_admin_user WHERE username='ops-cs'" | tr -d '[:space:]')"
fi

echo
echo "failures: $FAILS"
exit "$FAILS"
