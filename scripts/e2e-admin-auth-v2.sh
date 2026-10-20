#!/usr/bin/env bash
# Admin Session Authentication v2 — end-to-end acceptance run.
#
# Starts two gateway instances (session mode and trusted-header mode), exercises
# every auth endpoint including the proxy-bypass and concurrent-refresh
# regressions, then shuts them down.
#
# Usage:  bash scripts/e2e-admin-auth-v2.sh
# Env:    REPO_ROOT  override the repository root (defaults to this script's parent)
set -u

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO_ROOT" || exit 1

B=http://127.0.0.1:18080
TH=http://127.0.0.1:18081
WORK="${TMPDIR:-/tmp}/e2e-v2"
rm -rf "$WORK"; mkdir -p "$WORK"

# Keep the run fast while still exercising the grace window.
REPLAY_GRACE="2s"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m  %s\n' "$1"; }
check(){ if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1 — expected $3, got $2"; fi; }
title(){ printf '\n\033[36m== %s\033[0m\n' "$1"; }

code()  { curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "$@"; }
body()  { curl -s --noproxy '*' "$@"; }
tok()   { grep -o '"access_token":"[^"]*"' | head -1 | cut -d'"' -f4; }
codeof(){ grep -o '"code":"[^"]*"' | head -1 | cut -d'"' -f4; }
# Cookie jars are kept outside the repository so a run never dirties the tree.
jar()   { echo "$WORK/$1"; }
cookieval(){ grep campus_admin_refresh "$1" 2>/dev/null | awk '{print $7}' | tail -1; }
hdr()   { printf '%s' "$1" | head -1 | awk '{print $2}'; }

# Container names depend on the compose project, so discover them.
PG_C=$(docker ps --filter 'name=postgres' --format '{{.Names}}' | head -1)
RD_C=$(docker ps --filter 'name=redis' --format '{{.Names}}' | head -1)
if [ -z "$PG_C" ] || [ -z "$RD_C" ]; then
  printf '\033[31mPostgreSQL / Redis containers not found — run scripts/dev.ps1 first.\033[0m\n'
  exit 1
fi

# ---------------------------------------------------------------------------
# Migrations are applied by the runner, not by replaying each file with psql.
# Replay-safe history means every statement still no-ops — but only a statement
# that names objects that still exist can no-op. 0014 retires columns that 0002
# indexes (ADR-0005), so a raw replay of the whole set can no longer succeed
# against a current database. The runner applies what the bookkeeping says is
# pending, and its own contract check is the gate; that is the idempotency the
# deployment relies on, and it is what dev.ps1 and CI run.
DB_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable"
title "应用数据库迁移"
if ! go run ./cmd/migrate -database-url "$DB_URL" -migrations ./migrations >/dev/null 2>&1; then
  bad "迁移失败"
  exit 1
fi
ok "migrations 已应用（幂等，可重复执行）"
# Re-running the runner must be a no-op, which is what makes the script safe to
# run against an existing database.
if ! go run ./cmd/migrate -database-url "$DB_URL" -migrations ./migrations >/dev/null 2>&1; then
  bad "迁移不幂等：runner 第二次运行失败"
  exit 1
fi
ok "migrations 重复执行通过（幂等）"

title "构建网关二进制"
go build -o "$WORK/campus-gateway-e2e.exe" ./cmd/server || { bad "go build"; exit 1; }
ok "go build"

title "启动网关（session 模式 + trusted-header 模式）"
DATABASE_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" \
REDIS_ADDR="127.0.0.1:6379" \
API_KEY_HMAC_SECRET="campus-local-test-secret" \
ADMIN_SESSION_SECRET="e2e-admin-session-secret-at-least-32-bytes-long" \
HTTP_ADDR=":18080" \
ADMIN_AUTH_MODE="session" \
ADMIN_REFRESH_REPLAY_GRACE="$REPLAY_GRACE" \
TRUSTED_PROXY_CIDRS="127.0.0.1/32" \
"$WORK/campus-gateway-e2e.exe" > "$WORK/server.log" 2>&1 &
SRV=$!

DATABASE_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" \
REDIS_ADDR="127.0.0.1:6379" \
API_KEY_HMAC_SECRET="campus-local-test-secret" \
ADMIN_SESSION_SECRET="e2e-admin-session-secret-at-least-32-bytes-long" \
HTTP_ADDR=":18081" \
ADMIN_AUTH_MODE="trusted-header" \
ADMIN_PROXY_SECRET="e2e-proxy-secret-0123456789abcdef" \
"$WORK/campus-gateway-e2e.exe" > "$WORK/server-th.log" 2>&1 &
SRV_TH=$!

trap 'kill $SRV $SRV_TH 2>/dev/null' EXIT
for _ in $(seq 1 40); do
  [ "$(code $B/healthz)" = "200" ] && [ "$(code $TH/healthz)" = "200" ] && break
  sleep 0.4
done
check "session 网关健康检查" "$(code $B/healthz)" "200"
check "trusted-header 网关健康检查" "$(code $TH/healthz)" "200"

# 清空可能残留的账号与限流计数，保证从「首次启动」状态开始。
docker exec "$PG_C" psql -U campus -d campus_gateway -q \
  -c "DELETE FROM gateway_admin_user;" >/dev/null 2>&1
docker exec "$RD_C" redis-cli --scan --pattern 'gw:admin:*' 2>/dev/null \
  | xargs -r docker exec -i "$RD_C" redis-cli DEL >/dev/null 2>&1

# ---------------------------------------------------------------------------
title "1. 首次启动：auth-config 不得出现任何 Token 概念"
CFG=$(body $B/admin/auth-config)
echo "     $CFG"
check "GET /admin/auth-config" "$(code $B/admin/auth-config)" "200"
case "$CFG" in
  *token*|*Token*|*TOKEN*) bad "auth-config 响应出现了 token 字样" ;;
  *) ok "auth-config 响应不含 token 字样" ;;
esac
case "$CFG" in
  *'"needs_setup":true'*) ok "needs_setup=true（首次启动）" ;;
  *) bad "needs_setup 应为 true，实际 $CFG" ;;
esac

title "2. P0：反向代理下的首次初始化不得被远程绕过"
# A reverse proxy on the same host also connects from 127.0.0.1. The gateway
# trusts 127.0.0.1 as a proxy, so the resolved client decides — not the hop.
PROXY=$(body -X POST $B/admin/setup -H 'Content-Type: application/json' \
  -H 'X-Forwarded-For: 203.0.113.10' \
  -d '{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}')
check "loopback peer + 公网 XFF 的 setup 被拒绝" "$(printf '%s' "$PROXY" | codeof)" "setup_required_but_remote_forbidden"
check "远程绕过尝试后仍无账号" "$(body $B/admin/auth-config | grep -o '"needs_setup":true' | head -1)" '"needs_setup":true'

# A loopback client through the trusted proxy must pass the locality gate. The
# body is deliberately invalid (mismatched confirmation) so the request proves
# it got past the gate without creating an account.
LOCAL=$(body -X POST $B/admin/setup -H 'Content-Type: application/json' \
  -H 'X-Forwarded-For: 127.0.0.1' \
  -d '{"username":"probe","password":"Campus#2026Gate","confirm_password":"mismatch"}')
check "loopback peer + loopback XFF 通过本机校验（仅因请求体无效而 400）" \
  "$(printf '%s' "$LOCAL" | codeof)" "invalid_request"
check "该请求未创建任何账号" \
  "$(body $B/admin/auth-config | grep -o '"needs_setup":true' | head -1)" '"needs_setup":true'

title "3. 首次 setup（本机 127.0.0.1 直连）"
SETUP_HDR=$(curl -s --noproxy '*' -i -c "$(jar cj1.txt)" -X POST $B/admin/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","display_name":"信息化中心管理员","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}')
check "POST /admin/setup" "$(hdr "$SETUP_HDR")" "201"
printf '%s' "$SETUP_HDR" | grep -qi 'set-cookie: campus_admin_refresh=' && ok "下发了 campus_admin_refresh Cookie" || bad "未下发 refresh Cookie"
printf '%s' "$SETUP_HDR" | grep -qi 'httponly' && ok "Cookie 为 HttpOnly" || bad "Cookie 不是 HttpOnly"
printf '%s' "$SETUP_HDR" | grep -qi 'samesite=strict' && ok "Cookie 为 SameSite=Strict" || bad "Cookie 不是 SameSite=Strict"
printf '%s' "$SETUP_HDR" | grep -qi 'path=/admin/auth' && ok "Cookie Path 限定 /admin/auth" || bad "Cookie Path 未限定"
printf '%s' "$SETUP_HDR" | grep -qi 'password_hash' && bad "响应泄露了 password_hash" || ok "响应不含 password_hash"
TOKEN_A=$(printf '%s' "$SETUP_HDR" | tail -1 | tok)

title "4. 认证响应必须 no-store"
for path in /admin/auth-config /admin/login /admin/auth/refresh /admin/auth/logout; do
  H=$(curl -s --noproxy '*' -i -o /dev/null -D - -X POST "$B$path" 2>/dev/null | tr -d '\r')
  if printf '%s' "$H" | grep -qi '^cache-control: no-store'; then ok "$path 带 Cache-Control: no-store"; else bad "$path 缺少 no-store"; fi
  if printf '%s' "$H" | grep -qi '^pragma: no-cache'; then ok "$path 带 Pragma: no-cache"; else bad "$path 缺少 Pragma"; fi
done
H=$(curl -s --noproxy '*' -i -o /dev/null -D - $B/admin/auth-config 2>/dev/null | tr -d '\r')
printf '%s' "$H" | grep -qi '^cache-control: no-store' && ok "GET /admin/auth-config 带 no-store" || bad "auth-config 缺少 no-store"

title "5. 会话可用性"
check "GET /admin/whoami（setup 后的凭证）" "$(code -H "Authorization: Bearer $TOKEN_A" $B/admin/whoami)" "200"
WHO=$(body -H "Authorization: Bearer $TOKEN_A" $B/admin/whoami)
case "$WHO" in *'"auth_mode":"session"'*) ok "whoami 报告 auth_mode=session" ;; *) bad "whoami auth_mode 异常: $WHO" ;; esac
H=$(curl -s --noproxy '*' -i -o /dev/null -D - -H "Authorization: Bearer $TOKEN_A" $B/admin/whoami 2>/dev/null | tr -d '\r')
printf '%s' "$H" | grep -qi '^cache-control: no-store' && ok "whoami 带 no-store" || bad "whoami 缺少 no-store"

title "6. 未认证请求必须 401（不得是 503）"
check "GET /admin/status 无凭证" "$(code $B/admin/status)" "401"
UNAUTH=$(body $B/admin/status)
check "错误码" "$(printf '%s' "$UNAUTH" | codeof)" "unauthenticated"
case "$UNAUTH" in *admin_api_disabled*) bad "鉴权失败被误报为 admin_api_disabled" ;; *) ok "未被误报为 admin_api_disabled" ;; esac
check "GET /admin/status 伪造凭证" "$(code -H 'Authorization: Bearer forged-token' $B/admin/status)" "401"

title "7. 登录失败信息不得区分用户名是否存在"
R1=$(body -X POST $B/admin/login -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"WrongPass#2026"}')
R2=$(body -X POST $B/admin/login -H 'Content-Type: application/json' -d '{"username":"no-such-user","password":"WrongPass#2026"}')
check "错误密码" "$(printf '%s' "$R1" | codeof)" "invalid_credentials"
check "不存在的用户名" "$(printf '%s' "$R2" | codeof)" "invalid_credentials"
M1=$(printf '%s' "$R1" | grep -o '"message":"[^"]*"' | cut -d'"' -f4)
M2=$(printf '%s' "$R2" | grep -o '"message":"[^"]*"' | cut -d'"' -f4)
[ "$M1" = "$M2" ] && ok "两种失败的提示文本一致：$M1" || bad "提示文本不同：[$M1] vs [$M2]"

title "8. 正常登录 + 多设备会话"
LOGIN=$(curl -s --noproxy '*' -i -c "$(jar cj2.txt)" -X POST $B/admin/login \
  -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"Campus#2026Gate"}')
TOKEN_B=$(printf '%s' "$LOGIN" | tail -1 | tok)
check "POST /admin/login" "$(hdr "$LOGIN")" "200"
SESS=$(body -H "Authorization: Bearer $TOKEN_B" $B/admin/sessions)
N=$(printf '%s' "$SESS" | grep -o '"id":"' | wc -l | tr -d ' ')
check "GET /admin/sessions 会话数" "$N" "2"
case "$SESS" in *refresh*) bad "会话列表泄露了 refresh 材料" ;; *) ok "会话列表不含 refresh 材料" ;; esac

title "9. P0：多标签页并发 refresh 必须都成功且收敛到同一 Cookie"
OLD_REFRESH=$(cookieval "$(jar cj1.txt)")
# Two browser tabs are separate JS runtimes, so the requests really do race.
curl -s --noproxy '*' -o "$WORK/race-a.json" -D "$WORK/race-a.hdr" \
  -b "campus_admin_refresh=$OLD_REFRESH" -X POST $B/admin/auth/refresh -H "Origin: $B" &
PA=$!
curl -s --noproxy '*' -o "$WORK/race-b.json" -D "$WORK/race-b.hdr" \
  -b "campus_admin_refresh=$OLD_REFRESH" -X POST $B/admin/auth/refresh -H "Origin: $B" &
PB=$!
wait $PA $PB
CA=$(hdr "$(tr -d '\r' < "$WORK/race-a.hdr")")
CB=$(hdr "$(tr -d '\r' < "$WORK/race-b.hdr")")
check "并发 refresh A" "$CA" "200"
check "并发 refresh B" "$CB" "200"
VA=$(tr -d '\r' < "$WORK/race-a.hdr" | grep -i '^set-cookie: campus_admin_refresh=' | sed 's/.*=\([^;]*\).*/\1/')
VB=$(tr -d '\r' < "$WORK/race-b.hdr" | grep -i '^set-cookie: campus_admin_refresh=' | sed 's/.*=\([^;]*\).*/\1/')
[ -n "$VA" ] && [ "$VA" = "$VB" ] && ok "两个标签页收敛到同一个 refresh token" || bad "并发结果不一致: [$VA] vs [$VB]"
TOKEN_A2=$(tok < "$WORK/race-a.json")
check "并发后新 access token 可用" "$(code -H "Authorization: Bearer $TOKEN_A2" $B/admin/whoami)" "200"
check "并发后另一设备 B 未被误撤销" "$(code -H "Authorization: Bearer $TOKEN_B" $B/admin/whoami)" "200"

title "10. P0：超出 grace window 的重用必须判定 replay 并撤销全部会话"
sleep 3   # grace window is $REPLAY_GRACE
REUSE=$(body -X POST $B/admin/auth/refresh -H "Cookie: campus_admin_refresh=$OLD_REFRESH" -H "Origin: $B")
check "重用旧 refresh token" "$(printf '%s' "$REUSE" | codeof)" "refresh_replay_detected"
check "replay 后 A 的 token 失效" "$(code -H "Authorization: Bearer $TOKEN_A2" $B/admin/whoami)" "401"
check "replay 后 B 的 token 一并失效" "$(code -H "Authorization: Bearer $TOKEN_B" $B/admin/whoami)" "401"
AUD=$(docker exec "$PG_C" psql -U campus -d campus_gateway -t -A -c \
  "SELECT count(*) FROM gateway_admin_audit_log WHERE action='admin_refresh_replay_detected';" 2>/dev/null | tr -d ' ')
[ "${AUD:-0}" -ge 1 ] && ok "审计记录了 admin_refresh_replay_detected" || bad "未记录 replay 审计"

title "11. 修改密码：需当前密码，成功后全会话失效"
LOGIN2=$(curl -s --noproxy '*' -i -c "$(jar cj3.txt)" -X POST $B/admin/login \
  -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"Campus#2026Gate"}')
TOKEN_C=$(printf '%s' "$LOGIN2" | tail -1 | tok)
check "修改密码（当前密码错误）" "$(code -X POST -H "Authorization: Bearer $TOKEN_C" -H 'Content-Type: application/json' \
  -d '{"current_password":"NotThePassword","new_password":"NewCampus#2027"}' $B/admin/password)" "401"
check "修改密码（新密码过弱）" "$(code -X POST -H "Authorization: Bearer $TOKEN_C" -H 'Content-Type: application/json' \
  -d '{"current_password":"Campus#2026Gate","new_password":"short"}' $B/admin/password)" "400"
check "修改密码（成功）" "$(code -X POST -H "Authorization: Bearer $TOKEN_C" -H 'Content-Type: application/json' \
  -d '{"current_password":"Campus#2026Gate","new_password":"NewCampus#2027"}' $B/admin/password)" "200"
check "改密后原凭证失效" "$(code -H "Authorization: Bearer $TOKEN_C" $B/admin/whoami)" "401"
check "旧密码不可再登录" "$(code -X POST $B/admin/login -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"Campus#2026Gate"}')" "401"
check "新密码可登录" "$(code -X POST $B/admin/login -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"NewCampus#2027"}')" "200"

title "12. 登出与 Cookie 清理"
LOGIN3=$(curl -s --noproxy '*' -i -c "$(jar cj4.txt)" -X POST $B/admin/login \
  -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"NewCampus#2027"}')
TOKEN_D=$(printf '%s' "$LOGIN3" | tail -1 | tok)
LOGOUT=$(curl -s --noproxy '*' -i -b "$(jar cj4.txt)" -X POST $B/admin/auth/logout -H "Origin: $B")
check "POST /admin/auth/logout" "$(hdr "$LOGOUT")" "200"
printf '%s' "$LOGOUT" | grep -qi 'campus_admin_refresh=;' && ok "登出清理了 Cookie" || bad "登出未清理 Cookie"
check "登出后凭证失效" "$(code -H "Authorization: Bearer $TOKEN_D" $B/admin/whoami)" "401"

title "13. CSRF：跨站刷新被拒绝"
check "跨站 Origin 的 refresh" "$(code -X POST -H 'Origin: http://evil.example' $B/admin/auth/refresh)" "403"
CSRF=$(body -X POST -H 'Origin: http://evil.example' $B/admin/auth/refresh)
check "CSRF 错误码" "$(printf '%s' "$CSRF" | codeof)" "csrf_rejected"

title "14. 重复初始化被拒绝"
check "已有账号时再次 setup" "$(code -X POST $B/admin/setup -H 'Content-Type: application/json' \
  -d '{"username":"second","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}')" "403"
CFG2=$(body $B/admin/auth-config)
case "$CFG2" in *'"needs_setup":false'*) ok "needs_setup 已变为 false" ;; *) bad "needs_setup 未更新: $CFG2" ;; esac

title "15. 登录限流：IP / IP+用户名 / 用户名 三个维度"
docker exec "$RD_C" redis-cli --scan --pattern 'gw:admin:*' 2>/dev/null \
  | xargs -r docker exec -i "$RD_C" redis-cli DEL >/dev/null 2>&1
for i in $(seq 1 8); do
  code -X POST $B/admin/login -H 'Content-Type: application/json' \
    -d '{"username":"campus-admin","password":"definitely-wrong"}' >/dev/null
done
RL=$(body -X POST $B/admin/login -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"NewCampus#2027"}')
check "第 9 次尝试被限流" "$(printf '%s' "$RL" | codeof)" "too_many_attempts"
KEYS=$(docker exec "$RD_C" redis-cli --scan --pattern 'gw:admin:login:*' 2>/dev/null | tr -d '\r')
case "$KEYS" in *gw:admin:login:ip:*) ok "存在 IP 维度计数" ;; *) bad "缺少 IP 维度计数" ;; esac
case "$KEYS" in *gw:admin:login:pair:*) ok "存在 IP+用户名 维度计数" ;; *) bad "缺少 pair 维度计数" ;; esac
case "$KEYS" in *gw:admin:login:user:*) ok "存在 用户名 维度计数（防分布式撞库）" ;; *) bad "缺少 username-only 维度计数" ;; esac

title "16. trusted-header 模式"
check "无 secret 的 whoami" "$(code $TH/admin/whoami)" "401"
check "伪造用户头但无 secret" "$(code -H 'X-Forwarded-User: attacker' -H 'X-Forwarded-Groups: super_admin' $TH/admin/whoami)" "401"
check "错误 secret" "$(code -H 'X-Campus-Auth-Secret: wrong' -H 'X-Forwarded-User: u' $TH/admin/whoami)" "401"
TH_OK=$(body -H 'X-Campus-Auth-Secret: e2e-proxy-secret-0123456789abcdef' -H 'X-Forwarded-User: zhang.san' \
  -H 'X-Forwarded-Groups: gateway_admin,auditor' $TH/admin/whoami)
check "有效身份头 whoami" "$(printf '%s' "$TH_OK" | codeof)" ""
case "$TH_OK" in *'"auth_mode":"trusted-header"'*) ok "whoami 报告 auth_mode=trusted-header" ;; *) bad "auth_mode 异常: $TH_OK" ;; esac
check "trusted-header 下 /admin/auth-config login_enabled=false" \
  "$(body $TH/admin/auth-config | grep -o '"login_enabled":false' | head -1)" '"login_enabled":false'
check "trusted-header 下 /admin/auth/refresh 不可用" "$(code -X POST $TH/admin/auth/refresh)" "503"

title "17. 五角色 RBAC 回归（trusted-header）"
rbac() {
  local role="$1" path="$2" method="$3" want="$4"
  local got
  got=$(code -X "$method" -H 'X-Campus-Auth-Secret: e2e-proxy-secret-0123456789abcdef' -H "X-Forwarded-User: u-$role" \
    -H "X-Forwarded-Groups: $role" -H 'Content-Type: application/json' -d '{}' "$TH$path")
  check "$role $method $path" "$got" "$want"
}
rbac super_admin    /admin/status        GET  200
rbac gateway_admin  /admin/tenants/00000000-0000-0000-0000-000000000000 PUT 403
# Phase 1 (P1-02): the global tenant_admin grant was withdrawn. The role now
# fails closed at the RBAC layer — 403 before the handler — instead of reaching
# the handler and being refused for its body (the old 400).
rbac tenant_admin   /admin/tenants/00000000-0000-0000-0000-000000000000 PUT 403
rbac auditor        /admin/security-events GET 200
rbac operator       /admin/model-test    POST 400

title "18. 前端不得把凭据写入浏览器持久化存储"
LEAK=$(grep -rnE 'localStorage\.(setItem|getItem)\(|sessionStorage\.|indexedDB' web/admin/src 2>/dev/null \
  | grep -vE 'campus_theme|campus_sidebar' || true)
if [ -z "$LEAK" ]; then ok "前端源码只持久化 campus_theme / campus_sidebar"; else bad "前端持久化了非主题数据: $LEAK"; fi
grep -rn "campus_admin_token" web/admin/src >/dev/null 2>&1 && bad "前端仍引用 campus_admin_token" || ok "前端不再引用 campus_admin_token"

title "19. 审计留痕"
docker exec "$PG_C" psql -U campus -d campus_gateway -t -A -F'|' -c \
  "SELECT action, count(*) FROM gateway_admin_audit_log GROUP BY action ORDER BY action;" 2>/dev/null \
  | while IFS='|' read -r a c; do [ -n "$a" ] && printf '     %-32s %s\n' "$a" "$c"; done

printf '\n\033[36m======== 结果：%d 通过 / %d 失败 ========\033[0m\n' "$PASS" "$FAIL"
kill $SRV $SRV_TH 2>/dev/null
[ "$FAIL" -eq 0 ]
