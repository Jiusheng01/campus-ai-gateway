#!/usr/bin/env bash
# End-to-end check of the data plane: channel -> model -> route -> key -> /v1.
#
# The other acceptance script (verify-three-state.sh) exercises the control
# plane against whatever gateway is already running. This one needs a channel
# whose credential exists in the gateway's own environment, so it starts its
# own instance on a spare port with a key that only it knows, plus a fake
# upstream on another. Nothing here touches 127.0.0.1:8080.
#
# It also covers the two features that only a real request can prove: the
# upstream model list (GET /admin/providers/{id}/upstream-models) and the
# per-model quota actually rejecting traffic.
#
# Exit code is the number of failed checks.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT=${GATEWAY_PORT:-18090}
UPSTREAM_PORT=${UPSTREAM_PORT:-18080}
BASE="http://127.0.0.1:$PORT"
UPSTREAM="http://127.0.0.1:$UPSTREAM_PORT"
PY="C:/Users/le/.workbuddy-ai/binaries/python/versions/3.13.12/python.exe"
FAILS=0

CHANNEL=e2e-dp-channel
ALIAS=e2e-dp-model
KEYNAME=e2e-dp-key
UPSTREAM_KEY_NAME=E2E_UPSTREAM_KEY
UPSTREAM_KEY_VALUE=e2e-upstream-secret

GATEWAY_PID=
UPSTREAM_PID=
TOKEN=
TENANT=

psql_exec() { docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q "$@"; }
json() { "$PY" -c "import json,sys;d=json.load(sys.stdin);print(eval(sys.argv[1],{'d':d}))" "$1"; }
# The response body is read through a pipe rather than reopened by path: this
# python is a native Windows build and cannot resolve the shell's /tmp.
chat_field() { cat /tmp/dp.chat | "$PY" -c "import json,sys;d=json.load(sys.stdin);print(eval(sys.argv[1],{'d':d}))" "$1"; }

check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    printf '  PASS  %-46s %s\n' "$1" "$3"
  else
    printf '  FAIL  %-46s want=%s got=%s\n' "$1" "$2" "$3"
    FAILS=$((FAILS + 1))
  fi
}

req() { # req <method> <path> <token> [body] -> body on stdout, status in /tmp/dp.code
  local m=$1 p=$2 t=$3 b=${4:-}
  local args=(-s --noproxy '*' -o /tmp/dp.out -w '%{http_code}' -X "$m" "$BASE$p")
  [ -n "$t" ] && args+=(-H "Authorization: Bearer $t")
  [ -n "$b" ] && args+=(-H 'Content-Type: application/json' -d "$b")
  curl "${args[@]}" > /tmp/dp.code
  cat /tmp/dp.out
}
code() { cat /tmp/dp.code; }

stop_pid() { # stop_pid <pid>
  [ -n "${1:-}" ] || return 0
  kill "$1" 2>/dev/null || true
  powershell -NoProfile -Command "Stop-Process -Id $1 -Force -ErrorAction SilentlyContinue" >/dev/null 2>&1 || true
}

cleanup() {
  # Quotas reference the model, so they go first; the route references the
  # channel, so the channel goes last. Deleting by name keeps the cleanup
  # correct even when a check failed before an id was captured.
  psql_exec -c "
    DELETE FROM gateway_model_quota WHERE model_alias='$ALIAS';
    DELETE FROM gateway_model_route WHERE model_id IN (SELECT id FROM gateway_model WHERE alias='$ALIAS');
    DELETE FROM gateway_model WHERE alias='$ALIAS';
    DELETE FROM gateway_api_key WHERE name='$KEYNAME';
    DELETE FROM gateway_provider WHERE name='$CHANNEL';" >/dev/null 2>&1 || true
  stop_pid "$GATEWAY_PID"
  stop_pid "$UPSTREAM_PID"
}
trap cleanup EXIT

echo "== 0. start the fake upstream and a private gateway =="
"$PY" "$ROOT/scripts/fake-upstream.py" "$UPSTREAM_PORT" >/tmp/dp-upstream.log 2>&1 &
UPSTREAM_PID=$!
sleep 1
check "fake upstream answers" 200 \
  "$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $UPSTREAM_KEY_VALUE" "$UPSTREAM/v1/models")"

(
  export HTTP_ADDR=":$PORT"
  export DATABASE_URL='postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable'
  export REDIS_ADDR=127.0.0.1:6379
  export API_KEY_HMAC_SECRET=campus-local-test-secret
  export ADMIN_AUTH_MODE=session
  export ADMIN_SETUP_LOCAL_ONLY=true
  export TRUSTED_PROXY_CIDRS=127.0.0.1/32
  export "$UPSTREAM_KEY_NAME=$UPSTREAM_KEY_VALUE"
  exec "$ROOT/bin/campus-gateway.exe"
) >/tmp/dp-gateway.log 2>&1 &
GATEWAY_PID=$!

for _ in $(seq 1 40); do
  if [ "$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/healthz")" = "200" ]; then break; fi
  sleep 0.5
done
check "private gateway is up" 200 \
  "$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/healthz")"
[ "$FAILS" -gt 0 ] && { echo "gateway did not start; see /tmp/dp-gateway.log"; exit "$FAILS"; }

echo "== 1. login =="
TOKEN=$(curl -s --noproxy '*' -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"Campus#2026Gate"}' | json "d['access_token']")
[ -z "$TOKEN" ] && { echo "  FAIL  login returned no token"; exit 1; }
echo "  PASS  logged in"

echo "== 2. channel -> model -> route =="
OUT=$(req POST /admin/providers "$TOKEN" \
  "{\"name\":\"$CHANNEL\",\"base_url\":\"$UPSTREAM/v1\",\"protocol\":\"openai-chat\",\"credential_env\":\"$UPSTREAM_KEY_NAME\",\"status\":1}")
PROVIDER=$(echo "$OUT" | json "d['id']")
check "channel created" 201 "$(code)"

OUT=$(req POST /admin/models "$TOKEN" "{\"alias\":\"$ALIAS\",\"display_name\":\"E2E data plane\",\"status\":1}")
MODEL=$(echo "$OUT" | json "d['id']")
check "catalog entry created" 201 "$(code)"
check "catalog entry carries no binding" None "$(echo "$OUT" | json "d.get('provider_id')")"

req POST "/admin/models/$MODEL/routes" "$TOKEN" \
  "{\"provider_id\":\"$PROVIDER\",\"upstream_model\":\"fake-model-a\",\"priority\":1,\"weight\":100,\"timeout_ms\":60000,\"status\":1}" >/dev/null
check "route created" 201 "$(code)"

echo "== 3. upstream model list =="
OUT=$(req GET "/admin/providers/$PROVIDER/upstream-models" "$TOKEN")
check "upstream list" 200 "$(code)"
check "upstream names come back" "fake-model-a,fake-model-b" "$(echo "$OUT" | json "','.join(d['data'])")"

echo "== 4. a real /v1 request =="
TENANT=$(req GET /admin/tenants "$TOKEN" | json "d['data'][0]['id']")
OUT=$(req POST /admin/api-keys "$TOKEN" \
  "{\"tenant_id\":\"$TENANT\",\"name\":\"$KEYNAME\",\"status\":1,\"allowed_models\":[\"$ALIAS\"]}")
APIKEY=$(echo "$OUT" | json "d['api_key']")
check "api key issued" 201 "$(code)"
check "key is scoped to the alias" "$ALIAS" "$(echo "$OUT" | json "','.join(d['allowed_models'])")"

call() {
  curl -s --noproxy '*' -o /tmp/dp.chat -w '%{http_code}' -X POST "$BASE/v1/chat/completions" \
    -H "Authorization: Bearer $APIKEY" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$ALIAS\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}]}"
}

STATUS=$(call)
check "request accepted" 200 "$STATUS"
check "upstream answered through the gateway" pong "$(chat_field "d['choices'][0]['message']['content']")"
check "usage is reported" 15 "$(chat_field "d['usage']['total_tokens']")"

OUT=$(req GET "/admin/request-logs?limit=20" "$TOKEN")
check "request was logged" 1 "$(echo "$OUT" | json "1 if any(r.get('requested_model')=='$ALIAS' for r in d['data']) else 0")"

echo "== 5. a model quota actually stops traffic =="
req PUT /admin/model-quotas "$TOKEN" \
  "{\"tenant_id\":\"$TENANT\",\"model_alias\":\"$ALIAS\",\"daily_token_limit\":20}" >/dev/null
check "quota stored" 200 "$(code)"

# The limiter may admit the request that crosses the cap and reject the next
# one, so the check is "it stops", not "the second call fails".
REJECTED=0
for _ in $(seq 1 5); do
  STATUS=$(call)
  if [ "$STATUS" = "429" ]; then REJECTED=1; break; fi
done
check "cap rejects further traffic" 1 "$REJECTED"
check "rejection names the model quota" model_token_quota_exceeded "$(chat_field "d['error']['code']")"

echo "== 6. cleanup =="
psql_exec -c "
  DELETE FROM gateway_model_quota WHERE model_alias='$ALIAS';
  DELETE FROM gateway_model_route WHERE model_id IN (SELECT id FROM gateway_model WHERE alias='$ALIAS');
  DELETE FROM gateway_model WHERE alias='$ALIAS';
  DELETE FROM gateway_api_key WHERE name='$KEYNAME';
  DELETE FROM gateway_provider WHERE name='$CHANNEL';" >/dev/null 2>&1
check "no channel left behind" 0 \
  "$(psql_exec -tAc "SELECT count(*) FROM gateway_provider WHERE name='$CHANNEL'" 2>/dev/null | tr -d '[:space:]')"
check "no model left behind" 0 \
  "$(psql_exec -tAc "SELECT count(*) FROM gateway_model WHERE alias='$ALIAS'" 2>/dev/null | tr -d '[:space:]')"
check "no key left behind" 0 \
  "$(psql_exec -tAc "SELECT count(*) FROM gateway_api_key WHERE name='$KEYNAME'" 2>/dev/null | tr -d '[:space:]')"

echo
echo "failures: $FAILS"
exit "$FAILS"
