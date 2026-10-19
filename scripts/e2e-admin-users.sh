#!/usr/bin/env bash
# 管理员账号管理 · 端到端验证
#
# 覆盖：首次 setup → 超管建号 → 角色分配 → 权限边界 → 自锁保护 → 改角色即时生效
#       → 停用踢下线 → 审计留痕
#
# 用法：bash scripts/e2e-admin-users.sh
set -u
cd "$(dirname "$0")/.." || exit 1

WORK=/tmp/e2e-admin-users
rm -rf "$WORK"; mkdir -p "$WORK"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "  PASS  $1"; }
no(){ FAIL=$((FAIL+1)); echo "  FAIL  $1 (期望 $3 / 实际 $2)"; }
chk(){ if [ "$2" = "$3" ]; then ok "$1"; else no "$1" "$2" "$3"; fi }
J=(-s --noproxy '*')

# 回到"首次启动"状态：清空管理员表与登录限流键
docker exec deploy-postgres-1 psql -U campus -d campus_gateway -c "DELETE FROM gateway_admin_user;" >/dev/null 2>&1
docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:admin:*' 2>/dev/null \
  | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1

HTTP_ADDR=:18081 \
DATABASE_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" \
REDIS_ADDR=127.0.0.1:6379 \
API_KEY_HMAC_SECRET=campus-local-test-secret \
THINKWATCH_URL=http://127.0.0.1:8788 \
ADMIN_AUTH_MODE=session \
ADMIN_SETUP_LOCAL_ONLY=true \
TRUSTED_PROXY_CIDRS=127.0.0.1/32 \
./bin/campus-gateway.exe > "$WORK/gw.log" 2>&1 &
GWPID=$!
trap 'kill $GWPID 2>/dev/null' EXIT
sleep 5

B=http://127.0.0.1:18081
tok(){ sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p' "$1"; }
uid(){ sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$1"; }

echo "== 1. 首次 setup 创建超级管理员 =="
CODE=$(curl "${J[@]}" -o "$WORK/setup.json" -w '%{http_code}' -c "$WORK/cj-root.txt" \
  -X POST "$B/admin/setup" -H 'Content-Type: application/json' \
  -d '{"username":"root","display_name":"系统管理员","password":"RootAdmin#2026","confirm_password":"RootAdmin#2026"}')
chk "setup 返回 201" "$CODE" "201"
ROOTTOK=$(tok "$WORK/setup.json"); ROOTID=$(uid "$WORK/setup.json")
[ -n "$ROOTTOK" ] && ok "拿到超管 access token" || no "拿到超管 access token" "空" "非空"

echo "== 2. 超管创建 gateway_admin 账号 =="
CODE=$(curl "${J[@]}" -o "$WORK/create.json" -w '%{http_code}' \
  -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"username":"ops01","display_name":"网关运维","password":"OpsAccount#2026","roles":["gateway_admin"]}')
chk "创建账号返回 201" "$CODE" "201"
OPSID=$(uid "$WORK/create.json")
grep -q '"gateway_admin"' "$WORK/create.json" && ok "返回角色正确" || no "返回角色正确" "" "gateway_admin"

echo "== 3. 列表与敏感字段 =="
CODE=$(curl "${J[@]}" -o "$WORK/list.json" -w '%{http_code}' -H "Authorization: Bearer $ROOTTOK" "$B/admin/admin-users")
chk "列表返回 200" "$CODE" "200"
grep -q '"ops01"' "$WORK/list.json" && ok "列表含新账号" || no "列表含新账号" "" "含 ops01"
if grep -q 'password_hash' "$WORK/list.json"; then no "列表不含 password_hash" "含" "不含"; else ok "列表不含 password_hash"; fi
if grep -q 'argon2id' "$WORK/list.json"; then no "列表不含哈希值" "含" "不含"; else ok "列表不含哈希值"; fi

echo "== 4. 用户名大小写重复被拒 =="
CODE=$(curl "${J[@]}" -o "$WORK/dup.json" -w '%{http_code}' \
  -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"username":"OPS01","display_name":"重复账号","password":"OpsAccount#2026","roles":["auditor"]}')
chk "大小写重复返回 409" "$CODE" "409"

echo "== 5. 弱密码与空角色被拒 =="
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"username":"weak01","display_name":"弱密码","password":"123456","roles":["auditor"]}')
chk "弱密码返回 400" "$CODE" "400"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"username":"norole01","display_name":"无角色","password":"NoRole#Password2026","roles":[]}')
chk "空角色返回 400" "$CODE" "400"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"username":"typo01","display_name":"角色拼错","password":"TypoRole#2026","roles":["gateway-admin"]}')
chk "拼错角色名返回 400" "$CODE" "400"

echo "== 6. 新账号登录 =="
CODE=$(curl "${J[@]}" -o "$WORK/opslogin.json" -w '%{http_code}' -c "$WORK/cj-ops.txt" \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"ops01","password":"OpsAccount#2026"}')
chk "ops01 登录 200" "$CODE" "200"
OPSTOK=$(tok "$WORK/opslogin.json")

echo "== 7. 权限边界：非超管不得管理账号 =="
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/admin-users")
chk "gateway_admin 读管理员列表 403" "$CODE" "403"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -X POST "$B/admin/admin-users" \
  -H "Authorization: Bearer $OPSTOK" -H 'Content-Type: application/json' \
  -d '{"username":"evil","display_name":"越权","password":"EvilAccount#2026","roles":["super_admin"]}')
chk "gateway_admin 建号 403" "$CODE" "403"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/providers")
chk "gateway_admin 读供应商 200（本职权限保留）" "$CODE" "200"

echo "== 8. 自锁保护 =="
CODE=$(curl "${J[@]}" -o "$WORK/self.json" -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$ROOTID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"roles":["auditor"]}')
chk "修改自己的角色 403" "$CODE" "403"
CODE=$(curl "${J[@]}" -o "$WORK/self2.json" -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$ROOTID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"status":2}')
chk "停用自己的账号 403" "$CODE" "403"
grep -q 'self_modification_forbidden' "$WORK/self.json" && ok "错误码为 self_modification_forbidden" || no "错误码为 self_modification_forbidden" "" "含"

echo "== 9. 改角色即时生效（无需重新登录）=="
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/tenants")
chk "改角色前读租户 403" "$CODE" "403"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$OPSID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"roles":["tenant_admin"]}')
chk "改为 tenant_admin 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/tenants")
chk "同一 token 立刻可读租户 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/providers")
chk "原有权限同时收回 403" "$CODE" "403"

echo "== 10. 两个超管时可正常降级 =="
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$OPSID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"roles":["super_admin"]}')
chk "提升 ops01 为超管 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$OPSID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"roles":["gateway_admin"]}')
chk "存在第二个超管时降级 200" "$CODE" "200"

echo "== 11. 停用账号并踢下线 =="
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$OPSID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"status":2}')
chk "停用 ops01 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $OPSTOK" "$B/admin/status")
# 停用会撤销会话并提升 auth_version，因此原 token 先撞上会话检查，返回 401
# （会话已撤销）而不是 403（账号停用）。两者都表示凭据已失效。
if [ "$CODE" = "401" ] || [ "$CODE" = "403" ]; then ok "停用后原 token 失效（$CODE）"; else no "停用后原 token 失效" "$CODE" "401/403"; fi
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"ops01","password":"OpsAccount#2026"}')
chk "停用后无法登录 403" "$CODE" "403"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X PATCH "$B/admin/admin-users/$OPSID" \
  -H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json' \
  -d '{"status":1}')
chk "重新启用 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o /dev/null -w '%{http_code}' \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"ops01","password":"OpsAccount#2026"}')
chk "启用后可再次登录 200" "$CODE" "200"

echo "== 12. 审计留痕 =="
curl "${J[@]}" -o "$WORK/audit.json" -H "Authorization: Bearer $ROOTTOK" "$B/admin/admin-audit?limit=200"
for a in admin_user_created admin_user_roles_changed admin_user_disabled admin_user_enabled; do
  if grep -q "$a" "$WORK/audit.json"; then ok "审计含 $a"; else no "审计含 $a" "" "含"; fi
done
if grep -q 'admin_user:ops01' "$WORK/audit.json"; then ok "审计记录了被操作账号"; else no "审计记录了被操作账号" "" "含"; fi

echo ""
echo "======== 结果：$PASS 通过 / $FAIL 失败 ========"
[ "$FAIL" -eq 0 ] || exit 1
