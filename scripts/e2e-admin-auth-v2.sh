#!/usr/bin/env bash
# Admin Session Authentication v2 — end-to-end acceptance run.
# Starts the gateway, exercises every auth endpoint, then shuts it down.
set -u

B=http://127.0.0.1:18080
WORK=/tmp/e2e-v2
rm -rf "$WORK"; mkdir -p "$WORK"

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

# ---------------------------------------------------------------------------
title "启动网关"
cd /c/Users/le/ai-gateway
DATABASE_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" \
REDIS_ADDR="127.0.0.1:6379" \
API_KEY_HMAC_SECRET="campus-local-test-secret" \
HTTP_ADDR=":18080" \
ADMIN_AUTH_MODE="session" \
TRUSTED_PROXY_CIDRS="127.0.0.1/32" \
/tmp/campus-gateway-v2.exe > "$WORK/server.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
for _ in $(seq 1 30); do
  [ "$(code $B/healthz)" = "200" ] && break
  sleep 0.4
done
check "网关健康检查" "$(code $B/healthz)" "200"

# 清空可能残留的账号与限流计数，保证从「首次启动」状态开始。
# 限流键必须清掉，否则上一轮积累的失败次数会让本轮的 setup 直接 429。
docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q \
  -c "DELETE FROM gateway_admin_user;" >/dev/null 2>&1
docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:admin:*' 2>/dev/null \
  | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
docker exec deploy-redis-1 redis-cli DBSIZE >/dev/null 2>&1

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

title "2. 首次 setup（本机 127.0.0.1）"
SETUP_HDR=$(curl -s --noproxy '*' -i -c "$(jar cj1.txt)" -X POST $B/admin/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","display_name":"信息化中心管理员","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}')
check "POST /admin/setup" "$(printf '%s' "$SETUP_HDR" | head -1 | awk '{print $2}')" "201"
printf '%s' "$SETUP_HDR" | grep -qi 'set-cookie: campus_admin_refresh=' && ok "下发了 campus_admin_refresh Cookie" || bad "未下发 refresh Cookie"
printf '%s' "$SETUP_HDR" | grep -qi 'httponly' && ok "Cookie 为 HttpOnly" || bad "Cookie 不是 HttpOnly"
printf '%s' "$SETUP_HDR" | grep -qi 'samesite=strict' && ok "Cookie 为 SameSite=Strict" || bad "Cookie 不是 SameSite=Strict"
printf '%s' "$SETUP_HDR" | grep -qi 'path=/admin/auth' && ok "Cookie Path 限定 /admin/auth" || bad "Cookie Path 未限定"
printf '%s' "$SETUP_HDR" | grep -qi 'password_hash' && bad "响应泄露了 password_hash" || ok "响应不含 password_hash"
TOKEN_A=$(printf '%s' "$SETUP_HDR" | tail -1 | tok)

title "3. 会话可用性"
check "GET /admin/whoami（setup 后的凭证）" "$(code -H "Authorization: Bearer $TOKEN_A" $B/admin/whoami)" "200"
WHO=$(body -H "Authorization: Bearer $TOKEN_A" $B/admin/whoami)
case "$WHO" in *'"auth_mode":"session"'*) ok "whoami 报告 auth_mode=session" ;; *) bad "whoami auth_mode 异常: $WHO" ;; esac

title "4. 未认证请求必须 401（不得是 503）"
check "GET /admin/status 无凭证" "$(code $B/admin/status)" "401"
UNAUTH=$(body $B/admin/status)
check "错误码" "$(printf '%s' "$UNAUTH" | codeof)" "unauthenticated"
case "$UNAUTH" in *admin_api_disabled*) bad "鉴权失败被误报为 admin_api_disabled" ;; *) ok "未被误报为 admin_api_disabled" ;; esac
check "GET /admin/status 伪造凭证" "$(code -H 'Authorization: Bearer forged-token' $B/admin/status)" "401"

title "5. 登录失败信息不得区分用户名是否存在"
R1=$(body -X POST $B/admin/login -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"WrongPass#2026"}')
R2=$(body -X POST $B/admin/login -H 'Content-Type: application/json' -d '{"username":"no-such-user","password":"WrongPass#2026"}')
check "错误密码" "$(printf '%s' "$R1" | codeof)" "invalid_credentials"
check "不存在的用户名" "$(printf '%s' "$R2" | codeof)" "invalid_credentials"
M1=$(printf '%s' "$R1" | grep -o '"message":"[^"]*"' | cut -d'"' -f4)
M2=$(printf '%s' "$R2" | grep -o '"message":"[^"]*"' | cut -d'"' -f4)
[ "$M1" = "$M2" ] && ok "两种失败的提示文本一致：$M1" || bad "提示文本不同：[$M1] vs [$M2]"

title "6. 正常登录 + 多设备会话"
LOGIN=$(curl -s --noproxy '*' -i -c "$(jar cj2.txt)" -X POST $B/admin/login \
  -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"Campus#2026Gate"}')
TOKEN_B=$(printf '%s' "$LOGIN" | tail -1 | tok)
check "POST /admin/login" "$(printf '%s' "$LOGIN" | head -1 | awk '{print $2}')" "200"
SESS=$(body -H "Authorization: Bearer $TOKEN_B" $B/admin/sessions)
N=$(printf '%s' "$SESS" | grep -o '"id":"' | wc -l | tr -d ' ')
check "GET /admin/sessions 会话数" "$N" "2"
case "$SESS" in *refresh*) bad "会话列表泄露了 refresh 材料" ;; *) ok "会话列表不含 refresh 材料" ;; esac

title "7. Refresh Rotation"
OLD_REFRESH=$(cookieval "$(jar cj1.txt)")
REFRESH=$(curl -s --noproxy '*' -i -b "$(jar cj1.txt)" -c "$(jar cj1.txt)" -X POST $B/admin/auth/refresh -H "Origin: $B")
check "POST /admin/auth/refresh" "$(printf '%s' "$REFRESH" | head -1 | awk '{print $2}')" "200"
NEW_REFRESH=$(cookieval "$(jar cj1.txt)")
[ -n "$NEW_REFRESH" ] && [ "$NEW_REFRESH" != "$OLD_REFRESH" ] && ok "refresh token 已轮换" || bad "refresh token 未轮换"
TOKEN_A2=$(printf '%s' "$REFRESH" | tail -1 | tok)
check "轮换后新 access token 可用" "$(code -H "Authorization: Bearer $TOKEN_A2" $B/admin/whoami)" "200"

title "8. 重用检测：旧 refresh token 必须触发全会话撤销"
REUSE=$(body -X POST $B/admin/auth/refresh -H "Cookie: campus_admin_refresh=$OLD_REFRESH" -H "Origin: $B")
check "重用旧 refresh token" "$(printf '%s' "$REUSE" | codeof)" "session_revoked"
check "重用后 A 的新 token 失效" "$(code -H "Authorization: Bearer $TOKEN_A2" $B/admin/whoami)" "401"
check "重用后 B 的 token 一并失效" "$(code -H "Authorization: Bearer $TOKEN_B" $B/admin/whoami)" "401"

title "9. 修改密码：需当前密码，成功后全会话失效"
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

title "10. 登出与 Cookie 清理"
LOGIN3=$(curl -s --noproxy '*' -i -c "$(jar cj4.txt)" -X POST $B/admin/login \
  -H 'Content-Type: application/json' -d '{"username":"campus-admin","password":"NewCampus#2027"}')
TOKEN_D=$(printf '%s' "$LOGIN3" | tail -1 | tok)
LOGOUT=$(curl -s --noproxy '*' -i -b "$(jar cj4.txt)" -X POST $B/admin/auth/logout -H "Origin: $B")
check "POST /admin/auth/logout" "$(printf '%s' "$LOGOUT" | head -1 | awk '{print $2}')" "200"
printf '%s' "$LOGOUT" | grep -qi 'campus_admin_refresh=;' && ok "登出清理了 Cookie" || bad "登出未清理 Cookie"
check "登出后凭证失效" "$(code -H "Authorization: Bearer $TOKEN_D" $B/admin/whoami)" "401"

title "11. CSRF：跨站刷新被拒绝"
check "跨站 Origin 的 refresh" "$(code -X POST -H 'Origin: http://evil.example' $B/admin/auth/refresh)" "403"
CSRF=$(body -X POST -H 'Origin: http://evil.example' $B/admin/auth/refresh)
check "CSRF 错误码" "$(printf '%s' "$CSRF" | codeof)" "csrf_rejected"

title "12. 重复初始化被拒绝"
check "已有账号时再次 setup" "$(code -X POST $B/admin/setup -H 'Content-Type: application/json' \
  -d '{"username":"second","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}')" "403"
CFG2=$(body $B/admin/auth-config)
case "$CFG2" in *'"needs_setup":false'*) ok "needs_setup 已变为 false" ;; *) bad "needs_setup 未更新: $CFG2" ;; esac

title "13. 登录限流（IP 维度 8 次/窗口）"
for i in $(seq 1 8); do
  code -X POST $B/admin/login -H 'Content-Type: application/json' \
    -d '{"username":"campus-admin","password":"definitely-wrong"}' >/dev/null
done
RL=$(body -X POST $B/admin/login -H 'Content-Type: application/json' \
  -d '{"username":"campus-admin","password":"NewCampus#2027"}')
check "第 9 次尝试被限流" "$(printf '%s' "$RL" | codeof)" "too_many_attempts"

title "14. 审计留痕"
docker exec deploy-postgres-1 psql -U campus -d campus_gateway -t -A -F'|' -c \
  "SELECT action, count(*) FROM gateway_admin_audit_log GROUP BY action ORDER BY action;" 2>/dev/null \
  | while IFS='|' read -r a c; do [ -n "$a" ] && printf '     %-32s %s\n' "$a" "$c"; done

printf '\n\033[36m======== 结果：%d 通过 / %d 失败 ========\033[0m\n' "$PASS" "$FAIL"
kill $SRV 2>/dev/null
[ "$FAIL" -eq 0 ]
