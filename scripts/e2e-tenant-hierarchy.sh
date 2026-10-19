#!/usr/bin/env bash
# 多级额度分配 · 端到端验证
#
# 覆盖：建树（学校→院系→教师→学生）→ 逐级分配 → 继承解析 → 越权分配被拒
#       → 权限交集（子集不能超过父集）→ 作用域管理员只能管自己子树
#       → 作用域管理员不能提权 → 审计留痕
#
# 用法：bash scripts/e2e-tenant-hierarchy.sh
#
# 说明：数据面的强制（限流器沿祖先链逐级扣减）由 internal/ratelimit 的单元测试
# 覆盖；本脚本验证的是管理面——分配进去的额度是否被正确解析、越权是否被拒绝。
set -u
cd "$(dirname "$0")/.." || exit 1

WORK=/tmp/e2e-tenant-hierarchy
rm -rf "$WORK"; mkdir -p "$WORK"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "  PASS  $1"; }
no(){ FAIL=$((FAIL+1)); echo "  FAIL  $1 (期望 $3 / 实际 $2)"; }
chk(){ if [ "$2" = "$3" ]; then ok "$1"; else no "$1" "$2" "$3"; fi }
J=(-s --noproxy '*')

if [ ! -f ./bin/campus-gateway.exe ]; then
  echo "缺少 ./bin/campus-gateway.exe，请先构建" >&2
  exit 1
fi

# 回到干净状态：只清掉本脚本自己建出来的账号与租户，绝不动库里已有的管理员。
# 因此这里不走 /admin/setup（库里已有超管时它会返回 403），而是直接插入一个
# 已知密码的 e2e_root 账号。哈希是对固定测试口令算出的 argon2id，与用户名无关。
docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
  "DELETE FROM gateway_admin_user WHERE username LIKE 'e2e\_%';" >/dev/null 2>&1
docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
  "DELETE FROM gateway_tenant WHERE code LIKE 'e2e-%';" >/dev/null 2>&1
docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:admin:*' 2>/dev/null \
  | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1

E2E_HASH='$argon2id$v=19$m=19456,t=2,p=1$rZnvFvJ646I29YfsiG6IYQ$8h7wnwF21NxnVfLj3VNx0GDeUKQfgYN6lN6VMvmEKvU'
docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q -v ON_ERROR_STOP=1 >/dev/null 2>&1 <<SQL
INSERT INTO gateway_admin_user (username, display_name, password_hash, roles)
VALUES ('e2e_root', 'E2E 超管', '$E2E_HASH', '["super_admin"]'::jsonb);
SQL

cleanup() {
  kill $GWPID 2>/dev/null
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_admin_user WHERE username LIKE 'e2e\_%';" >/dev/null 2>&1
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_tenant WHERE code LIKE 'e2e-%';" >/dev/null 2>&1
  # 本脚本的登录请求全部来自 127.0.0.1，而登录限流是按 IP 计数的，且与用户
  # 正在使用的 8080 实例共用同一个 Redis。不在这里清掉，跑一次测试就会把用户
  # 自己的登录额度（默认 8 次 / 15 分钟）耗光，导致「too many login attempts」。
  docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:admin:login:*' 2>/dev/null \
    | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
}

HTTP_ADDR=:18082 \
DATABASE_URL="postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable" \
REDIS_ADDR=127.0.0.1:6379 \
API_KEY_HMAC_SECRET=campus-local-test-secret \
THINKWATCH_URL=http://127.0.0.1:8788 \
ADMIN_AUTH_MODE=session \
ADMIN_SETUP_LOCAL_ONLY=true \
TRUSTED_PROXY_CIDRS=127.0.0.1/32 \
./bin/campus-gateway.exe > "$WORK/gw.log" 2>&1 &
GWPID=$!
trap cleanup EXIT
sleep 5

B=http://127.0.0.1:18082
tok(){ sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p' "$1"; }
field(){ sed -n "s/.*\"$2\":\([^,}]*\).*/\1/p" "$1" | head -1; }
str(){ sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p" "$1" | head -1; }

# 分配响应是 {"effective":{...},"tenant":{...}} —— Go 的 map 序列化按键字母序，
# 所以 effective 排在 tenant 前面，两个对象里都有同名的额度字段。要读"解析后的
# 有效值"必须先切出 effective 子对象，否则会读到 tenant 里那个 null。
effget(){ sed -n 's/.*"effective":{\([^}]*\)}.*/\1/p' "$1" \
         | sed -n "s/.*\"$2\":\([^,}]*\).*/\1/p" | head -1; }

# ---------------------------------------------------------------- 1. 超管登录
echo "== 1. 以预置的超管账号登录 =="
CODE=$(curl "${J[@]}" -o "$WORK/setup.json" -w '%{http_code}' -c "$WORK/cj-root.txt" \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"e2e_root","password":"E2eRoot#Password2026"}')
chk "超管登录返回 200" "$CODE" "200"
ROOTTOK=$(tok "$WORK/setup.json")
[ -n "$ROOTTOK" ] && ok "拿到超管 access token" || no "拿到超管 access token" "空" "非空"
AUTH=(-H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json')

# ------------------------------------------------- 1b. 表单校验指明缺失字段
# 原先 code 与 name 任一为空都只回一句 "code and name are required"：用户填了
# 名称、漏了编码时，界面上只弹一条看不出问题在哪的提示。这里锁定「报错必须点名
# 具体字段」，否则前端再怎么高亮也没有依据。
echo "== 1b. 租户表单校验指明缺失字段 =="
vtenant(){ curl "${J[@]}" -o "$WORK/validate.json" -w '%{http_code}' \
  -X POST "$B/admin/tenants" "${AUTH[@]}" -d "$1"; }
vmsg(){ str "$WORK/validate.json" message; }

chk "缺编码 → 400" "$(vtenant '{"name":"只有名称"}')" "400"
chk "报错点名 code" "$(vmsg)" "tenant code is required"
chk "缺名称 → 400" "$(vtenant '{"code":"e2e-only-code"}')" "400"
chk "报错点名 name" "$(vmsg)" "tenant name is required"
chk "两者都缺 → 400" "$(vtenant '{"code":"","name":""}')" "400"
chk "两者都缺时同时点名" "$(vmsg)" "tenant code and name are required"
chk "只有空白字符也算缺失" "$(vtenant '{"code":"   ","name":"测试"}')" "400"
chk "空白字符同样点名 code" "$(vmsg)" "tenant code is required"

# ---------------------------------------------------------------- 2. 建树
echo "== 2. 建立租户树 学校 → 院系 → 教师 → 学生 =="
mktenant(){ # $1=code $2=name $3=kind $4=parent_id(可空)
  local body
  if [ -n "$4" ]; then
    body="{\"code\":\"$1\",\"name\":\"$2\",\"kind\":\"$3\",\"parent_id\":\"$4\"}"
  else
    body="{\"code\":\"$1\",\"name\":\"$2\",\"kind\":\"$3\"}"
  fi
  curl "${J[@]}" -o "$WORK/$1.json" -w '%{http_code}' -X POST "$B/admin/tenants" \
    "${AUTH[@]}" -d "$body"
}
chk "建学校" "$(mktenant e2e-school 测试大学 school '')" "201"
SCHOOL=$(str "$WORK/e2e-school.json" id)
chk "建院系" "$(mktenant e2e-dept 计算机学院 dept "$SCHOOL")" "201"
DEPT=$(str "$WORK/e2e-dept.json" id)
chk "建教师" "$(mktenant e2e-teacher 张老师 teacher "$DEPT")" "201"
TEACHER=$(str "$WORK/e2e-teacher.json" id)
chk "建学生" "$(mktenant e2e-student 张三 student "$TEACHER")" "201"
STUDENT=$(str "$WORK/e2e-student.json" id)
[ -n "$SCHOOL" ] && [ -n "$DEPT" ] && [ -n "$TEACHER" ] && [ -n "$STUDENT" ] \
  && ok "四层 id 全部拿到" || no "四层 id 全部拿到" "$SCHOOL/$DEPT/$TEACHER/$STUDENT" "非空"

# path 与 depth 由服务端派生，不信任客户端输入
chk "学校 depth=0" "$(field "$WORK/e2e-school.json" depth)" "0"
chk "院系 depth=1" "$(field "$WORK/e2e-dept.json" depth)" "1"
chk "学生 depth=3" "$(field "$WORK/e2e-student.json" depth)" "3"
grep -q "\"/$SCHOOL/$DEPT/$TEACHER/$STUDENT/\"" "$WORK/e2e-student.json" \
  && ok "学生 path 为物化路径" || no "学生 path 为物化路径" "$(str "$WORK/e2e-student.json" path)" "/$SCHOOL/$DEPT/$TEACHER/$STUDENT/"

# 不存在的父节点必须被拒绝，否则会产生额度链断裂的孤儿
CODE=$(curl "${J[@]}" -o "$WORK/orphan.json" -w '%{http_code}' -X POST "$B/admin/tenants" \
  "${AUTH[@]}" -d '{"code":"e2e-orphan","name":"孤儿","parent_id":"00000000-0000-0000-0000-000000000000"}')
chk "父节点不存在返回 400" "$CODE" "400"

# ---------------------------------------------------------------- 3. 逐级分配
echo "== 3. 逐级分配额度 =="
alloc(){ # $1=tenant_id $2=json
  curl "${J[@]}" -o "$WORK/alloc-$1.json" -w '%{http_code}' -X POST \
    "$B/admin/tenants/$1/quota-allocations" "${AUTH[@]}" -d "$2"
}
chk "给学校分配 100 万日额度" \
  "$(alloc "$SCHOOL" '{"daily_token_limit":1000000,"monthly_token_limit":20000000,"monthly_request_limit":100000,"allowed_models":["gpt-4o","claude-3-5-sonnet","qwen-max"]}')" "200"
chk "给院系分配 20 万日额度" \
  "$(alloc "$DEPT" '{"daily_token_limit":200000,"monthly_token_limit":4000000,"monthly_request_limit":20000,"allowed_models":["gpt-4o","qwen-max"]}')" "200"
chk "给教师分配 5 万日额度" \
  "$(alloc "$TEACHER" '{"daily_token_limit":50000,"monthly_token_limit":1000000,"monthly_request_limit":5000,"allowed_models":["qwen-max"]}')" "200"

# ---------------------------------------------------------------- 4. 继承解析
echo "== 4. 继承解析（学生不设额度，应继承教师）=="
chk "给学生的空分配" "$(alloc "$STUDENT" '{}')" "200"
EFF="$WORK/alloc-$STUDENT.json"
chk "学生日额度继承为 50000" "$(effget "$EFF" daily_token_limit)" "50000"
chk "学生月额度继承为 1000000" "$(effget "$EFF" monthly_token_limit)" "1000000"
chk "学生月请求继承为 5000" "$(effget "$EFF" monthly_request_limit)" "5000"
chk "学生模型访问受限" "$(effget "$EFF" model_access_restricted)" "true"
grep -q '"qwen-max"' "$EFF" && ok "学生可用模型含 qwen-max" || no "学生可用模型含 qwen-max" "$(cat "$EFF")" "qwen-max"
grep -q '"gpt-4o"' "$EFF" && no "学生不应拿到 gpt-4o（教师已收窄）" "有" "无" \
  || ok "学生拿不到 gpt-4o（教师已收窄）"

# 显式 0 = 不限额，它必须"阻断继承"而不是被当成"没设置"。这是三态语义里最容易写错的一条。
chk "给学生显式设 0（不限额）" "$(alloc "$STUDENT" '{"daily_token_limit":0}')" "200"
chk "显式 0 覆盖了继承来的 50000" "$(effget "$WORK/alloc-$STUDENT.json" daily_token_limit)" "0"
chk "同一次分配里未提到的维度仍继承" "$(effget "$WORK/alloc-$STUDENT.json" monthly_token_limit)" "1000000"
chk "恢复学生继承" "$(alloc "$STUDENT" '{}')" "200"

# ---------------------------------------------------------------- 5. 越权分配
echo "== 5. 不能分配超过上级持有的额度 =="
CODE=$(curl "${J[@]}" -o "$WORK/over.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$TEACHER/quota-allocations" "${AUTH[@]}" \
  -d '{"daily_token_limit":999999}')
chk "教师额度超过院系持有 → 400" "$CODE" "400"
grep -q 'exceeds what the parent level holds' "$WORK/over.json" \
  && ok "错误信息说明是超过上级持有量" || no "错误信息说明是超过上级持有量" "$(cat "$WORK/over.json")" "exceeds..."

# 院系的白名单是 {gpt-4o, qwen-max}（学校原本还有 claude，已被院系收窄）。
# 教师只能在这两个里面选，不能把院系收窄掉的那个再要回来。
CODE=$(curl "${J[@]}" -o "$WORK/widen.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$TEACHER/quota-allocations" "${AUTH[@]}" \
  -d '{"allowed_models":["claude-3-5-sonnet"]}')
chk "教师索要院系已收窄掉的模型 → 400" "$CODE" "400"
grep -q 'is not available at the parent level' "$WORK/widen.json" \
  && ok "错误信息说明模型不在上级白名单内" || no "错误信息说明模型不在上级白名单内" "$(cat "$WORK/widen.json")" "is not available..."

# 在院系白名单之内换模型是允许的：约束是"相对上级"，不是"相对自己的当前值"。
CODE=$(curl "${J[@]}" -o "$WORK/rewidthin.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$TEACHER/quota-allocations" "${AUTH[@]}" \
  -d '{"daily_token_limit":50000,"monthly_token_limit":1000000,"monthly_request_limit":5000,"allowed_models":["gpt-4o"]}')
chk "在上级白名单内换模型 → 200" "$CODE" "200"
CODE=$(curl "${J[@]}" -o "$WORK/restore.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$TEACHER/quota-allocations" "${AUTH[@]}" \
  -d '{"daily_token_limit":50000,"monthly_token_limit":1000000,"monthly_request_limit":5000,"allowed_models":["qwen-max"]}')
chk "教师白名单恢复为 qwen-max" "$CODE" "200"

CODE=$(curl "${J[@]}" -o "$WORK/neg.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$TEACHER/quota-allocations" "${AUTH[@]}" \
  -d '{"daily_token_limit":-1}')
chk "负额度 → 400" "$CODE" "400"

# ---------------------------------------------------------------- 6. 作用域管理员
echo "== 6. 院系管理员只能管自己子树 =="
CODE=$(curl "${J[@]}" -o "$WORK/scoped.json" -w '%{http_code}' -X POST "$B/admin/admin-users" \
  "${AUTH[@]}" \
  -d "{\"username\":\"e2e_dept_admin\",\"display_name\":\"院系管理员\",\"password\":\"DeptAdmin#2026\",\"roles\":[\"tenant_admin\"],\"scope_tenant_id\":\"$DEPT\"}")
chk "创建院系作用域账号返回 201" "$CODE" "201"
grep -q "\"scope_tenant_id\":\"$DEPT\"" "$WORK/scoped.json" \
  && ok "账号带上了作用域" || no "账号带上了作用域" "$(cat "$WORK/scoped.json")" "$DEPT"
DEPTID=$(str "$WORK/scoped.json" id)

CODE=$(curl "${J[@]}" -o "$WORK/deptlogin.json" -w '%{http_code}' -c "$WORK/cj-dept.txt" \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"e2e_dept_admin","password":"DeptAdmin#2026"}')
chk "院系管理员登录返回 200" "$CODE" "200"
DTOK=$(tok "$WORK/deptlogin.json")
[ -n "$DTOK" ] && ok "拿到院系管理员 token" || no "拿到院系管理员 token" "空" "非空"
DAUTH=(-H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json')

# 子树内：允许
CODE=$(curl "${J[@]}" -o "$WORK/dept-ok.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$STUDENT/quota-allocations" "${DAUTH[@]}" \
  -d '{"daily_token_limit":10000}')
chk "院系管理员给学生分配 → 200" "$CODE" "200"

# 子树外（自己的作用域租户本身）：拒绝。能写自己的节点就等于能给自己加额度。
CODE=$(curl "${J[@]}" -o "$WORK/dept-self.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$DEPT/quota-allocations" "${DAUTH[@]}" \
  -d '{"daily_token_limit":999999}')
chk "院系管理员改自己节点 → 403" "$CODE" "403"

# 子树外（上级）：拒绝
CODE=$(curl "${J[@]}" -o "$WORK/dept-up.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$SCHOOL/quota-allocations" "${DAUTH[@]}" \
  -d '{"daily_token_limit":1}')
chk "院系管理员改上级节点 → 403" "$CODE" "403"

# 列表被收窄到子树：自己的节点 + 后代，不含学校。
# 断言用 code 而不是 id —— 后代的 parent_id 与 path 里本来就带着上级的 id，
# 用 id 去 grep 会把"合法的父引用"误判成"越界返回了上级"。
curl "${J[@]}" -o "$WORK/dept-list.json" -X GET "$B/admin/tenants" "${DAUTH[@]}"
grep -q '"code":"e2e-dept"' "$WORK/dept-list.json" && ok "列表含自己的节点" || no "列表含自己的节点" "" "e2e-dept"
grep -q '"code":"e2e-student"' "$WORK/dept-list.json" && ok "列表含后代节点" || no "列表含后代节点" "" "e2e-student"
grep -q '"code":"e2e-school"' "$WORK/dept-list.json" && no "列表不应含上级节点" "有" "无" \
  || ok "列表不含上级节点"

# 不能新建根租户
CODE=$(curl "${J[@]}" -o "$WORK/dept-root.json" -w '%{http_code}' -X POST "$B/admin/tenants" \
  "${DAUTH[@]}" -d '{"code":"e2e-illegal-root","name":"非法根"}')
chk "院系管理员建根租户 → 403" "$CODE" "403"

# 不能给自己加额度（自改作用域被拒）
CODE=$(curl "${J[@]}" -o "$WORK/dept-selfscope.json" -w '%{http_code}' -X POST \
  "$B/admin/admin-users/$DEPTID/scope" "${DAUTH[@]}" -d '{"scope_tenant_id":null}')
chk "院系管理员清空自己的作用域 → 4xx" "$(echo "$CODE" | cut -c1)" "4"

# ---------------------------------------------------------------- 7. 审计
echo "== 7. 超管改作用域 + 审计留痕 =="
# 正向的作用域变更（放在院系管理员的断言之后：作用域是每次请求实时读库的，
# 改掉它会让上面那个已签发的 token 立刻换一副权限）。
CODE=$(curl "${J[@]}" -o "$WORK/scope-move.json" -w '%{http_code}' -X POST \
  "$B/admin/admin-users/$DEPTID/scope" "${AUTH[@]}" -d "{\"scope_tenant_id\":\"$TEACHER\"}")
chk "超管改作用域 → 200" "$CODE" "200"
grep -q "\"scope_tenant_id\":\"$TEACHER\"" "$WORK/scope-move.json" \
  && ok "作用域已改到教师" || no "作用域已改到教师" "$(cat "$WORK/scope-move.json")" "$TEACHER"

curl "${J[@]}" -o "$WORK/audit.json" -X GET "$B/admin/admin-audit?limit=200" "${AUTH[@]}"
grep -q 'tenant_quota_allocated' "$WORK/audit.json" \
  && ok "记录了 tenant_quota_allocated" || no "记录了 tenant_quota_allocated" "" "有"
grep -q 'tenant_quota_allocated_denied' "$WORK/audit.json" \
  && ok "记录了越权分配被拒" || no "记录了越权分配被拒" "" "有"
grep -q 'admin_user_scope_changed' "$WORK/audit.json" \
  && ok "记录了作用域变更" || no "记录了作用域变更" "" "有"
# 院系管理员碰 /admin/admin-users 是被 RBAC 挡下的（那个端点只对超管开放），
# 记录的是中间件的 admin_forbidden，而不是处理函数里的 *_failed。
grep -q 'admin_forbidden' "$WORK/audit.json" \
  && ok "记录了 RBAC 越权访问" || no "记录了 RBAC 越权访问" "" "有"

# ---------------------------------------------------------------- 8. 收尾
echo
echo "== 结果：$PASS 通过 / $FAIL 失败 =="
[ "$FAIL" -eq 0 ] || exit 1
