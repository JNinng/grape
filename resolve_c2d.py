# -*- coding: utf-8 -*-
# C2 合并冲突第四轮：四份文档（一次性脚本）

def hunks(path):
    s = open(path, encoding='utf-8').read()
    out = []
    i = s.find('<<<<<<< HEAD')
    while i != -1:
        j = s.find('=======', i)
        k = s.find('>>>>>>> feat/grants-c2', j)
        out.append((i, j, k, s[i+len('<<<<<<< HEAD\n'):j], s[j+len('=======\n'):k]))
        i = s.find('<<<<<<< HEAD', k)
    return s, out

# ---------- sqlc/README.md ----------
p = 'internal/data/pg_sqlc/sqlc/README.md'
s, hs = hunks(p)
assert len(hs) == 5, len(hs)
# 逐 hunk 从后往前替换，避免位移
merged = {}
# hunk5（index 4）与 hunk4（index 3）：先看 5 的内容再定 —— 保留 HEAD 的 audit 节 + THEIRS 的 grant 节
(h4i, h4j, h4k, h4head, h4theirs) = hs[3]
(h5i, h5j, h5k, h5head, h5theirs) = hs[4]
# 从后往前
s = s[:h5i] + h5head + h5theirs + s[h5k+len('>>>>>>> feat/grants-c2'):]
s = s[:h4i] + h4head + h4theirs + s[h4k+len('>>>>>>> feat/grants-c2'):]
# 重算前三个 hunk 位置
s2, hs2 = hunks(p)
assert len(hs2) == 3, len(hs2)
(i1, j1, k1, head1, theirs1) = hs2[0]
(i2, j2, k2, head2, theirs2) = hs2[1]
(i3, j3, k3, head3, theirs3) = hs2[2]
# hunk3 部署顺序
m3 = '''  `agent_session.sql` 与 `agent_vector.sql` → `schema/95_agent_audit.sql`
  → `96_agent_grant.sql`。单一事实源是
'''
s = s[:i3] + m3 + s[k3+len('>>>>>>> feat/grants-c2'):]
# hunk2 散文
m2 = '''  `sqlc.yaml` 的 schema 列表 = schema/ 十个文件按序 + rls/operate/expose
  + 96_agent_grant.sql，queries 列表 = queries/ 七个文件（operate/basic/
  perm、业务表固定 CRUD sys_user/sys_session 与 Agent 审计 agent_audit、
  工具授权 agent_grant）。加 rls/operate/expose 是为了让 queries 里的查
  询能调用这些文件里的函数（GetScope 调三件套、判断类调 fn_can_* /
  fn_check_visible）；sqlc 只看签名，函数体是否存在于库里由执行顺序保证。
  业务表的固定 CRUD 一资源一文件（queries/<资源>.sql），
'''
s = s[:i2] + m2 + s[k2+len('>>>>>>> feat/grants-c2'):]
# hunk1 表格：HEAD（含 audit #10）为基础，在 expose(#14) 后插 grant(#15)，后续行号顺延
grant_row = '| 15 | 96_agent_grant.sql | 定义 Agent 工具授权表：agent_tool_grant、未撤销唯一约束（部分唯一表达式索引）、租户 RLS 策略与运行角色授权（C2；策略随域文件走，口径同 expose.sql 的 sys_perm_version） | 是 | 1 张表、1 个部分唯一索引、1 个普通索引、1 条策略、1 条授权 |'
lines = head1.splitlines()
out = []
for ln in lines:
    if ln.startswith('| 15 | queries/'):
        out.append(grant_row)
        out.append('| 16 | queries/ | 定义写侧判定、basic 操作、权限查询、业务表固定 CRUD 与 Agent 审计/授权读写的命名查询 | 是（queries） | 7 个文件、46 条命名查询 |')
    elif ln.startswith('| 16 | seed.sql'):
        out.append('| 17 | seed.sql | 填充种子数据（含多租户权限样例与 C2 授权管理权限码菜单） | 否 | 租户/部门/角色/用户/用户组/字典/菜单/配置/开关/翻译/日志 + basic 归属矩阵 |')
    elif ln.startswith('| 17 | agent_session'):
        out.append('| 18 | agent_session.sql | 定义 Agent 会话存储：schema agent 与 trpc-agent-go 框架 session/postgres 的六张 `agent_*` 表、索引及 grape_u 授权 | 否 | 1 个 schema、6 张表、12 个索引、3 条授权 |')
    elif ln.startswith('| 18 | agent_vector'):
        out.append('| 19 | agent_vector.sql | 定义向量检索设施：pgvector 扩展、agent schema、agent_document 向量表、HNSW 索引与运行角色授权（超级用户面；租户隔离由应用层适配器强制，RLS 仅作可选加强；授权收窄到 agent_document） | 否 | 1 个扩展、1 个 schema、1 张表、2 个索引、2 条授权 |')
    else:
        out.append(ln)
m1 = '\n'.join(out) + '\n'
s = s[:i1] + m1 + s[k1+len('>>>>>>> feat/grants-c2'):]
assert '<<<<<<<' not in s
open(p, 'w', encoding='utf-8', newline='').write(s)
print(p, 'OK')

# ---------- deploy_db.md ----------
p = 'deploy_db.md'
s, hs = hunks(p)
assert len(hs) == 3, len(hs)
# 从后往前
(i3, j3, k3, head3, theirs3) = hs[2]
m3 = head3.rstrip('\n') + '\n' + theirs3.rstrip('\n') + '\n'
s = s[:i3] + m3 + s[k3+len('>>>>>>> feat/grants-c2'):]
(i2, j2, k2, head2, theirs2) = hs[1]
m2 = head2.replace('schema/95_agent_audit.sql; do', 'schema/95_agent_audit.sql 96_agent_grant.sql; do')
s = s[:i2] + m2 + s[k2+len('>>>>>>> feat/grants-c2'):]
(i1, j1, k1, head1, theirs1) = hs[0]
grant_row = theirs1.rstrip('\n')
grant_row = grant_row.replace('| 17 |', '| 18 |')
m1 = head1.rstrip('\n') + '\n' + grant_row + '\n'
s = s[:i1] + m1 + s[k1+len('>>>>>>> feat/grants-c2'):]
assert '<<<<<<<' not in s
open(p, 'w', encoding='utf-8', newline='').write(s)
print(p, 'OK')

# ---------- internal/README.md ----------
p = 'internal/README.md'
s, hs = hunks(p)
assert len(hs) == 1
(i1, j1, k1, head1, theirs1) = hs[0]
m1 = '''- `agent`：`NewStack`（Agent 框架骨架，依赖会话存储、向量存储、用户用例、授权用例与 embedder——业务工具经用户用例访问数据，检索工具经向量适配器与 embedder 访问向量（D2），许可源经授权用例落 agent_tool_grant 表（C2）；agent 会话列表端点随骨架装配，service 经 `Stack.SessionsHandler` 取其 HTTP 形态注册路由；授权管理端点随骨架挂进 ServeMux；cleanup 释放 runner）、`NewSessionService`（Agent 会话存储，框架 session/postgres：schema agent + agent_ 表前缀，部署面预建表，运行期不做 DDL；TTL 与压缩经 conf Agent 段注入。会话表无 RLS、不存租户业务数据，属文档化「设施面」例外，userKey = tenant_id:user_id 应用层隔离）与 `NewVectorStore`（biz 向量契约的框架适配器，经 Stack 进入骨架）；'''
s = s[:i1] + m1 + s[k1+len('>>>>>>> feat/grants-c2'):]
assert '<<<<<<<' not in s
open(p, 'w', encoding='utf-8', newline='').write(s)
print(p, 'OK')

# ---------- internal/agent/README.md ----------
p = 'internal/agent/README.md'
s, hs = hunks(p)
assert len(hs) == 3, len(hs)
(i3, j3, k3, head3, theirs3) = hs[2]
m3 = head3.rstrip('\n') + '\n' + theirs3.rstrip('\n') + '\n'
s = s[:i3] + m3 + s[k3+len('>>>>>>> feat/grants-c2'):]
s, hs = hunks(p)
(i2, j2, k2, head2, theirs2) = hs[1]
c2_gateway = [l for l in theirs2.splitlines() if '[gateway.go]' in l][0]
head_provider = [l for l in head2.splitlines() if '[provider.go]' in l][0]
head_provider = head_provider.replace('依赖会话存储/向量存储/用户用例/embedder', '依赖会话存储/向量存储/用户用例/授权用例/embedder')
audit_hook = [l for l in head2.splitlines() if '[audit_hook.go]' in l][0]
m2 = c2_gateway + '\n' + audit_hook + '\n' + head_provider + '\n'
s = s[:i2] + m2 + s[k2+len('>>>>>>> feat/grants-c2'):]
s, hs = hunks(p)
(i1, j1, k1, head1, theirs1) = hs[0]
c2_gateway_short = [l for l in theirs1.splitlines() if '[gateway.go]' in l][0]
d2_vector = [l for l in head1.splitlines() if '[vectorstore.go]' in l][0]
m1 = c2_gateway_short + '\n' + d2_vector + '\n'
s = s[:i1] + m1 + s[k1+len('>>>>>>> feat/grants-c2'):]
assert '<<<<<<<' not in s
open(p, 'w', encoding='utf-8', newline='').write(s)
print(p, 'OK')
print('DOCS DONE')
