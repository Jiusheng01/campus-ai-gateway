#!/usr/bin/env bash
# 按模型额度分配 · 端到端验证
#
# 覆盖：系统级模型池 → 数据面 429（且事件能指出是「模型池」还是「租户」）
#       → 租户级按模型收窄 → 不得超过上级 → 白名单校验 → 模型之间互不影响
#       → 删除后回到继承 → 审计留痕
#
# 用法：bash scripts/e2e-model-quota.sh
#
# 两条安全约定，改脚本时不要破坏：
#
# 1) 只用不存在的模型别名（e2e-quota-model / e2e-other-model）。配额检查发生在
#    路由之前，所以别名不需要真的可路由；而用真实别名（如 campus-smart）会创建
#    一个「全校共享池」，在测试期间把用户正在跑的真实业务流量一起限掉。
# 2) 清理只删本脚本自己造出来的键与行，绝不整片清 gw:quota:*：Redis 是和数据面
#    共用的，整片清会抹掉真实租户的用量计数。
set -u
cd "$(dirname "$0")/.." || exit 1

WORK=/tmp/e2e-model-quota
rm -rf "$WORK"; mkdir -p "$WORK"
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "  PASS  $1"; }
no(){ FAIL=$((FAIL+1)); echo "  FAIL  $1 (期望 $3 / 实际 $2)"; }
chk(){ if [ "$2" = "$3" ]; then ok "$1"; else no "$1" "$2" "$3"; fi }
J=(-s --noproxy '*')

# 本脚本专用的模型别名。见文件头约定 1。
MODEL=e2e-quota-model
OTHER=e2e-other-model

if [ ! -f ./bin/campus-gateway.exe ]; then
  echo "缺少 ./bin/campus-gateway.exe，请先构建" >&2
  exit 1
fi

# 回到干净状态：只清本脚本自己的账号、租户、API Key、额度行与配额键。
#
# 删除顺序不能变：gateway_api_key.tenant_id 的外键没有 ON DELETE 子句（即
# restrict），只要租户下还挂着 Key，租户就删不掉；而 gateway_tenant 的
# parent_id 是 RESTRICT，所以整棵树必须在同一条语句里删掉。少了第一步，
# 清理会静默失败（psql 的报错被 >/dev/null 吃掉），下一轮就会在建租户时撞 409。
purge_db(){
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_api_key WHERE tenant_id IN (SELECT id FROM gateway_tenant WHERE code LIKE 'e2e-%');" >/dev/null 2>&1
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_admin_user WHERE username LIKE 'e2e\_%';" >/dev/null 2>&1
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_tenant WHERE code LIKE 'e2e-%';" >/dev/null 2>&1
  # 模型额度行会随租户级联删除，但系统池那行 tenant_id 是 NULL，不受级联影响，
  # 必须显式删掉，否则会在库里留下一个指向测试别名的池子。
  docker exec deploy-postgres-1 psql -U campus -d campus_gateway -q -c \
    "DELETE FROM gateway_model_quota WHERE model_alias LIKE 'e2e-%';" >/dev/null 2>&1
}

purge_quota_keys(){
  # 只删两个新命名空间（模型池 / 租户+模型），它们是本次新增的，不会有存量数据；
  # 加上租户链上属于本脚本那几个租户的键（按 code 反查 id）。
  docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:quota:model:*' 2>/dev/null \
    | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
  docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:quota:tenant:*:model:*' 2>/dev/null \
    | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
  local ids
  ids=$(docker exec deploy-postgres-1 psql -U campus -d campus_gateway -tA -c \
    "SELECT id FROM gateway_tenant WHERE code LIKE 'e2e-%';" 2>/dev/null)
  for id in $ids; do
    docker exec deploy-redis-1 redis-cli --scan --pattern "gw:quota:tenant:${id}:*" 2>/dev/null \
      | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
  done
}

purge_db
purge_quota_keys

# 清理失败必须响出来。否则脚本会带着上一轮的残留继续跑，故障现象会变成
# 「建租户 409」「Key 401」这类看不出根因的错。
leftover=$(docker exec deploy-postgres-1 psql -U campus -d campus_gateway -tA -c \
  "SELECT count(*) FROM gateway_tenant WHERE code LIKE 'e2e-%';" 2>/dev/null | tr -d '[:space:]')
if [ "$leftover" != "0" ]; then
  echo "无法清理上一轮的 e2e 租户（仍剩 $leftover 个），请先手动清理再运行" >&2
  exit 1
fi

E2E_HASH='$argon2id$v=19$m=19456,t=2,p=1$rZnvFvJ646I29YfsiG6IYQ$8h7wnwF21NxnVfLj3VNx0GDeUKQfgYN6lN6VMvmEKvU'
docker exec -i deploy-postgres-1 psql -U campus -d campus_gateway -q -v ON_ERROR_STOP=1 >/dev/null 2>&1 <<SQL
INSERT INTO gateway_admin_user (username, display_name, password_hash, roles)
VALUES ('e2e_root', 'E2E 超管', '$E2E_HASH', '["super_admin"]'::jsonb);
SQL

cleanup(){
  kill $GWPID 2>/dev/null
  purge_db
  purge_quota_keys
  # 登录限流按 IP 计数，且与用户正在使用的实例共用同一个 Redis。不在这里清掉，
  # 跑一次测试就会把用户自己的登录额度（默认 8 次 / 15 分钟）耗光。
  docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:admin:login:*' 2>/dev/null \
    | xargs -r docker exec -i deploy-redis-1 redis-cli DEL >/dev/null 2>&1
}

HTTP_ADDR=:18083 \
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

B=http://127.0.0.1:18083
tok(){ sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p' "$1"; }
field(){ sed -n "s/.*\"$2\":\([^,}]*\).*/\1/p" "$1" | head -1; }
str(){ sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p" "$1" | head -1; }

# 读「解析后的有效值」。分配响应里 effective 与 tenant 都有同名额度字段，
# 不先切出子对象会读到 tenant 里那个 null。
effget(){ sed -n 's/.*"effective":\[\([^]]*\)\].*/\1/p' "$1" \
         | sed -n "s/.*\"$2\":\([^,}]*\).*/\1/p" | head -1; }

# 错误响应是 {"error":{"code":"...","message":"...","request_id":"..."}}，
# 直接用 field 取 error 只会拿到一个左花括号。
ecode(){ sed -n 's/.*"error":{"code":"\([^"]*\)".*/\1/p' "$1" | head -1; }
emsg(){ sed -n 's/.*"message":"\([^"]*\)".*/\1/p' "$1" | head -1; }

# 数据面：发一次真实 /v1 请求。配额检查在路由之前，所以不需要上游可用 ——
# 被配额挡住会拿到 429，放行则会在代理阶段以别的状态码失败。
v1(){ # $1=api_key $2=model
  curl "${J[@]}" -o "$WORK/v1.json" -w '%{http_code}' -X POST "$B/v1/chat/completions" \
    -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"
}
# 429 且确实是配额拒绝（而不是别的限流）
v1_quota_rejected(){ [ "$1" = "429" ] && grep -q 'token_quota_exceeded' "$WORK/v1.json"; }

mq(){ # PUT 一条模型额度
  curl "${J[@]}" -o "$WORK/mq.json" -w '%{http_code}' -X PUT "$B/admin/model-quotas" \
    "${AUTH[@]}" -d "$1"
}
mq_del(){ # DELETE 一条模型额度：$1=tenant_id(空=系统池) $2=alias
  local q="model_alias=$2"
  [ -n "$1" ] && q="tenant_id=$1&$q"
  curl "${J[@]}" -o "$WORK/mqdel.json" -w '%{http_code}' -X DELETE "$B/admin/model-quotas?$q" "${AUTH[@]}"
}

# ---------------------------------------------------------------- 1. 超管登录
echo "== 1. 以预置的超管账号登录 =="
CODE=$(curl "${J[@]}" -o "$WORK/login.json" -w '%{http_code}' -c "$WORK/cj.txt" \
  -X POST "$B/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"e2e_root","password":"E2eRoot#Password2026"}')
chk "超管登录返回 200" "$CODE" "200"
ROOTTOK=$(tok "$WORK/login.json")
[ -n "$ROOTTOK" ] && ok "拿到超管 access token" || no "拿到超管 access token" "空" "非空"
AUTH=(-H "Authorization: Bearer $ROOTTOK" -H 'Content-Type: application/json')

# ---------------------------------------------------------------- 2. 建树 + 建 Key
echo "== 2. 建立租户树 学校 → 院系 → 教师 → 学生，并建一把 Key =="
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

CODE=$(curl "${J[@]}" -o "$WORK/key.json" -w '%{http_code}' -X POST "$B/admin/api-keys" \
  "${AUTH[@]}" -d "{\"name\":\"e2e-quota-key\",\"tenant_id\":\"$STUDENT\",\"status\":1,\"rpm_limit\":600,\"tpm_limit\":0,\"max_concurrency\":50,\"allowed_models\":[],\"allowed_ips\":[]}")
chk "给学生建 API Key → 201" "$CODE" "201"
KEY=$(str "$WORK/key.json" api_key)
[ -n "$KEY" ] && ok "拿到明文 Key" || no "拿到明文 Key" "空" "非空"

# ------------------------------------------------------- 3. 未配置时行为不变
# 这一条是回归基线：一个池子都没配时，模型维度必须完全不介入 —— 既不拒绝，
# 也不在 Redis 里留下任何计数器。
echo "== 3. 未配置任何模型池时，模型维度不介入 =="
CODE=$(curl "${J[@]}" -o "$WORK/list0.json" -w '%{http_code}' "$B/admin/model-quotas" "${AUTH[@]}")
chk "列出模型额度 → 200" "$CODE" "200"
grep -q "$MODEL" "$WORK/list0.json" && no "初始列表不含测试别名" "有" "无" || ok "初始列表不含测试别名"

C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && no "未配置池时不应被配额拒绝" "429" "非配额拒绝" || ok "未配置池时不被配额拒绝"
KEYS=$(docker exec deploy-redis-1 redis-cli --scan --pattern 'gw:quota:model:*' 2>/dev/null | wc -l)
chk "未配置池时不写任何模型计数器" "$KEYS" "0"

# ---------------------------------------------------------------- 4. 系统级模型池
echo "== 4. 系统级共享池：超限即 429，且事件点名「模型池」 =="
chk "配系统池 daily=10 → 200" "$(mq "{\"tenant_id\":null,\"model_alias\":\"$MODEL\",\"daily_token_limit\":10,\"monthly_token_limit\":null}")" "200"
chk "响应回显别名" "$(str "$WORK/mq.json" model_alias)" "$MODEL"
grep -q '"tenant_id":null' "$WORK/mq.json" && ok "系统池的 tenant_id 为 null" || no "系统池的 tenant_id 为 null" "$(cat "$WORK/mq.json")" "null"

C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && ok "超过共享池 → 429 配额拒绝" || no "超过共享池 → 429 配额拒绝" "$C" "429+token_quota_exceeded"
chk "错误码为 model_token_quota_exceeded" "$(ecode "$WORK/v1.json")" "model_token_quota_exceeded"

sleep 1
curl "${J[@]}" -o "$WORK/events.json" -X GET "$B/admin/security-events?limit=50" "${AUTH[@]}"
grep -q 'model_token_quota_exceeded' "$WORK/events.json" \
  && ok "安全事件记录了模型池拒绝" || no "安全事件记录了模型池拒绝" "" "有"
grep -q "level=模型:${MODEL}(全校)" "$WORK/events.json" \
  && ok "事件 detail 指出是「模型池(全校)」" || no "事件 detail 指出是「模型池(全校)」" "$(grep -o 'level=[^"]*' "$WORK/events.json" | head -1)" "level=模型:${MODEL}(全校)"

# 池子放大后必须放行：证明上面那次 429 是配额造成的，而不是请求本身有问题。
chk "把共享池放大到 100000 → 200" "$(mq "{\"tenant_id\":null,\"model_alias\":\"$MODEL\",\"daily_token_limit\":100000,\"monthly_token_limit\":null}")" "200"
C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && no "放大池子后应放行" "仍被配额拒绝" "非配额拒绝" || ok "放大池子后放行"

# 放行的那次请求验证的是「预占 → 对账/退款」这条链有没有把模型键带上。
# 上游不可用 → 响应里没有 usage → totalTokens=0 → 走退款分支。所以这里期望的
# 不是计数增加，而是：键确实被创建过（说明模型层参与了预占），且最终归零
# （说明退款把模型键也一起退了）。少了后半步，池子会被"没真正用掉的估算"慢慢吃光。
K="gw:quota:model:${MODEL}:tokens:day:$(date -u +%Y%m%d)"
EX=$(docker exec deploy-redis-1 redis-cli EXISTS "$K" 2>/dev/null | tr -d '\r')
chk "模型层参与了预占（模型池键已创建）" "$EX" "1"
GOT=$(docker exec deploy-redis-1 redis-cli GET "$K" 2>/dev/null | tr -d '\r')
chk "上游失败后预占已退回（计数归零）" "$GOT" "0"

# ---------------------------------------------------------------- 5. 租户级收窄
echo "== 5. 租户级按模型收窄：不得超过上级，拒绝要能点名到租户 =="
chk "学校配 999999（超过共享池）→ 400" \
  "$(mq "{\"tenant_id\":\"$SCHOOL\",\"model_alias\":\"$MODEL\",\"daily_token_limit\":999999}")" "400"
grep -q 'exceeds what the parent level holds' "$WORK/mq.json" \
  && ok "报错说明超过了上级持有量" || no "报错说明超过了上级持有量" "$(cat "$WORK/mq.json")" "exceeds..."

chk "学校配 10 → 200" "$(mq "{\"tenant_id\":\"$SCHOOL\",\"model_alias\":\"$MODEL\",\"daily_token_limit\":10}")" "200"
C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && ok "租户级模型额度生效 → 429" || no "租户级模型额度生效 → 429" "$C" "429"
sleep 1
curl "${J[@]}" -o "$WORK/events2.json" -X GET "$B/admin/security-events?limit=50" "${AUTH[@]}"
grep -q "level=租户:测试大学/模型:${MODEL}" "$WORK/events2.json" \
  && ok "事件 detail 点名到租户" || no "事件 detail 点名到租户" "$(grep -o 'level=[^"]*' "$WORK/events2.json" | head -1)" "level=租户:测试大学/模型:${MODEL}"

# 同一模型下，共享池与租户行必须并存 —— 这是 tenant_id IS NULL 参与唯一索引的体现。
curl "${J[@]}" -o "$WORK/list1.json" -X GET "$B/admin/model-quotas" "${AUTH[@]}"
chk "同一模型下共存的行数（共享池 + 学校）" "$(grep -o "\"model_alias\":\"$MODEL\"" "$WORK/list1.json" | wc -l | tr -d ' ')" "2"

# ---------------------------------------------------------------- 6. 白名单校验
echo "== 6. 白名单外的模型不允许配额度 =="
chk "建第二个学校" "$(mktenant e2e-school2 第二学校 school '')" "201"
SCHOOL2=$(str "$WORK/e2e-school2.json" id)
CODE=$(curl "${J[@]}" -o "$WORK/allow.json" -w '%{http_code}' -X POST \
  "$B/admin/tenants/$SCHOOL2/quota-allocations" "${AUTH[@]}" \
  -d "{\"allowed_models\":[\"$MODEL\"]}")
chk "把第二学校的白名单收窄为单一模型 → 200" "$CODE" "200"
chk "白名单外的模型 → 400" \
  "$(mq "{\"tenant_id\":\"$SCHOOL2\",\"model_alias\":\"$OTHER\",\"daily_token_limit\":100}")" "400"
grep -q 'is not allowed for this tenant' "$WORK/mq.json" \
  && ok "报错说明模型不在白名单内" || no "报错说明模型不在白名单内" "$(cat "$WORK/mq.json")" "not allowed..."
chk "白名单内的模型 → 200" \
  "$(mq "{\"tenant_id\":\"$SCHOOL2\",\"model_alias\":\"$MODEL\",\"daily_token_limit\":100}")" "200"

# ---------------------------------------------------------------- 7. 参数校验
echo "== 7. 参数校验 =="
chk "空别名 → 400" "$(mq '{"tenant_id":null,"model_alias":"   ","daily_token_limit":1}')" "400"
grep -q 'model alias is required' "$WORK/mq.json" && ok "报错点名别名" || no "报错点名别名" "$(cat "$WORK/mq.json")" "model alias is required"
chk "负数额度 → 400" "$(mq "{\"tenant_id\":null,\"model_alias\":\"$MODEL\",\"daily_token_limit\":-1}")" "400"
grep -q 'daily_token_limit' "$WORK/mq.json" && ok "报错点名字段" || no "报错点名字段" "$(cat "$WORK/mq.json")" "daily_token_limit"
chk "删除时缺别名 → 400" "$(curl "${J[@]}" -o /dev/null -w '%{http_code}' -X DELETE "$B/admin/model-quotas" "${AUTH[@]}")" "400"

# ---------------------------------------------------------------- 8. 模型互不影响
echo "== 8. 不同模型的池子互不影响 =="
chk "给另一个模型配共享池 → 200" \
  "$(mq "{\"tenant_id\":null,\"model_alias\":\"$OTHER\",\"daily_token_limit\":100000}")" "200"
C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && ok "被限的模型仍然 429" || no "被限的模型仍然 429" "$C" "429"
C=$(v1 "$KEY" "$OTHER")
v1_quota_rejected "$C" && no "另一个模型不应被牵连" "429" "非配额拒绝" || ok "另一个模型不受牵连"

# ---------------------------------------------------------------- 9. 回到继承
echo "== 9. 删除租户行后回到继承上级 =="
chk "删除学校的模型额度 → 200" "$(mq_del "$SCHOOL" "$MODEL")" "200"
chk "removed=true" "$(field "$WORK/mqdel.json" removed)" "true"
C=$(v1 "$KEY" "$MODEL")
v1_quota_rejected "$C" && no "删除后应继承共享池而放行" "429" "非配额拒绝" || ok "删除后继承共享池并放行"
chk "重复删除返回 removed=false" "$(mq_del "$SCHOOL" "$MODEL")" "200"
chk "重复删除 removed=false" "$(field "$WORK/mqdel.json" removed)" "false"

# ---------------------------------------------------------------- 10. 审计
echo "== 10. 审计留痕 =="
curl "${J[@]}" -o "$WORK/audit.json" -X GET "$B/admin/admin-audit?limit=200" "${AUTH[@]}"
for action in model_quota_allocated model_quota_deleted model_quota_rejected; do
  grep -q "$action" "$WORK/audit.json" && ok "记录了 $action" || no "记录了 $action" "" "有"
done
grep -q 'model_pool:' "$WORK/audit.json" && ok "审计里能区分共享池" || no "审计里能区分共享池" "" "model_pool:"
grep -q 'model_quota:' "$WORK/audit.json" && ok "审计里能区分租户行" || no "审计里能区分租户行" "" "model_quota:"

# ---------------------------------------------------------------- 11. 收尾
echo
echo "== 结果：$PASS 通过 / $FAIL 失败 =="
[ "$FAIL" -eq 0 ] || exit 1
