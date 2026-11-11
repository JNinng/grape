# 数据库部署

grape 关系存储（PostgreSQL）的部署：按序执行
`internal/data/pg_sqlc/sqlc/` 下的 38 个 SQL 文件，最后补一次连接授权。
执行顺序的单一事实源是
[deploy.go](internal/data/pg_sqlc/deploy/deploy.go) 的 `DeployFiles` 清单
（dbdeploy 执行生产清单；集成测试经 `TestDeployFiles()` 追加测试种子），
本文表格是其镜像。SQL 对象索引见
[sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)；生产运行口径
（多实例粘性、优雅停机、密钥管理、框架升级）见 [operations.md](operations.md)。

## 一键部署（推荐）

```bash
export GRAPE_DEPLOY_ADMIN_DSN=postgres://<user>:<pass>@127.0.0.1:5432/postgres
export GRAPE_DEPLOY_GRAPE_U_PASSWORD=<grape_u 口令>
export GRAPE_DEPLOY_TEST_U_PASSWORD=<test_u 口令>
make dbdeploy ARGS="--database <库名>"
```

连接 `postgres` 库幂等创建目标库（已存在沿用）→ 连接目标库 → **单事务内**
按清单顺序执行全部文件（任一失败整体回滚）→ 事务内补 `GRANT CONNECT`。
`role.sql` 的角色口令以 `{{GRAPE_U_PASSWORD}}` / `{{TEST_U_PASSWORD}}` 占位，
dbdeploy 执行前在内存中一次性替换，仓库与日志不落明文。

幂等边界：角色（判存）与种子（`ON CONFLICT DO NOTHING`）可重复；结构 DDL 为
裸创建，对已部署库重复执行会报错退出——全量重建先 `DROP DATABASE` 再执行，
本命令不宣称全量幂等。

## 执行顺序

按序执行 27 个步骤、38 个文件，顺序不能变（步骤 16 的种子包按
`seed.Files` 逐文件执行）。`queries/` 是 sqlc 的 Go 生成输入，不执行。

| 步骤 | 文件 | 作用 | 关键约束 |
| --- | --- | --- | --- |
| 1 | schema/10_basic.sql | basic 表范式（归属三元组 + `@scope`） | 1–11 无外键依赖，按 10→92 执行 |
| 2 | schema/20_tenant.sql | sys_tenant 租户表（RLS 允许命中自身 id） | 同上 |
| 3 | schema/30_user.sql | sys_user、用户-角色/用户组映射、用户组 | 同上 |
| 4 | schema/40_rbac.sql | 角色、范围明细、分发部门、菜单、角色-菜单映射 | 同上 |
| 5 | schema/50_dept.sql | 组织树、闭包表与闭包触发器 | 同上 |
| 6 | schema/60_dict.sql | 字典类型与数据 | 同上 |
| 7 | schema/70_audit.sql | 登录/操作日志（append-only） | 同上 |
| 8 | schema/80_session.sql | 会话/令牌（仅存哈希） | 同上 |
| 9 | schema/90_config.sql | 全局/租户配置、功能开关、翻译 | 同上 |
| 10 | schema/91_knowledge.sql | kb_library 知识库与 kb_document 条目 | 同上；租户 RLS 在 rls.sql 建策略 |
| 11 | schema/92_model.sql | sys_model 模型目录（平台/租户双态） | 同上；平台行只能经受控函数写 |
| 12 | role.sql | grape_u / test_u、schema 使用权、DML 授权、GRANT SET | 必须先于 13–15（函数授权依赖角色） |
| 13 | rls.sql | RLS 策略（含 kb_* 与 sys_model 双策略）、`fn_accessible_*` 三件套、updated_at 触发器 | 依赖 1–11 的表 |
| 14 | expose.sql | 只读判定、范围版本表与 bump 触发器 | 依赖 13；先于 15（函数体引用）与 16（种子 bump） |
| 15 | operate.sql | `fn_can_*` 与角色判定族 | 依赖 13/14 的函数；`fn_visible_role_ids` 依赖同文件 `fn_can_see_role` |
| 16 | [seed/](internal/data/pg_sqlc/sqlc/seed/) | 根租户自举 12 个文件，顺序见 [seed.go](internal/data/pg_sqlc/sqlc/seed/seed.go) | RLS 已启用，须以超级用户/表 owner 执行 |
| 17 | schema/93_oss.sql | oss_object 对象表（内嵌 gofakes3 数据介质，设施面无 RLS） | 尾部追加；依赖 12 的 grape_u（DML 授权文件内显式声明） |
| 18 | agent_session.sql | 框架 session/postgres 六张 `agent_*` 表 | 只依赖 12 的 grape_u；尾部追加不改序 |
| 19 | agent_vector.sql | pgvector 扩展与 `agent_document` 多空间向量表 | 后于 12；超级用户执行；无 RLS，应用层隔离 |
| 20 | schema/95_agent_audit.sql | Agent 审计两表、索引与租户 RLS | 尾部追加；依赖 12；授权由 role.sql 默认权限覆盖 |
| 21 | 96_agent_grant.sql | 工具授权表、未撤销唯一约束与租户 RLS | 后于 12；超级用户执行；策略随域文件 |
| 22 | schema/97_agent_retrieval_log.sql | 检索留痕表、索引与租户 RLS | 同 20 追加口径 |
| 23 | 98_menu_write.sql | 四个受控写函数 | 尾部追加；依赖 4；EXECUTE 默认 PUBLIC |
| 24 | 98_config_write.sql | 三个受控写函数 | 尾部；依赖 9；EXECUTE 默认 PUBLIC |
| 25 | 98_model_write.sql | 平台模型建/改/软删三个受控写函数 | 尾部；依赖 11；EXECUTE 默认 PUBLIC |
| 26 | 98_tenant_guard.sql | 租户删除前置计数函数 | 尾部；依赖 2；`CREATE OR REPLACE` 可重复 |
| 27 | schema/99_model_call_log.sql | 模型调用留痕表、索引与租户 RLS | 同 20 追加口径 |

手工执行按上表顺序；`role.sql` 的口令占位符需先替换，最后补
`GRANT CONNECT`（见下节）。

集成测试种子 [test_seed.sql](internal/data/pg_sqlc/sqlc/test_seed.sql)
（`t-test-*` 租户及演示数据）不在生产清单内：仅由 `TestDeployFiles()` 在
生产清单后追加，依赖种子包 `010_sys_tenant.sql` 的树根租户。开发/演示库
如需数据，在生产清单后以超级用户单独执行；该文件复用固定主键，仅适用于
全新建库，历史残留库会被文件头守卫拒绝，重复执行会重复追加日志。

## 幂等性与全量重建

`role.sql`（角色判存 + 授权）与种子包（`ON CONFLICT DO NOTHING`，不覆盖
已有行）可重复执行；其余文件不保证可重复：步骤 1–11 的 `CREATE TABLE` /
`CREATE INDEX`、`rls.sql` 的 `CREATE POLICY`、rls/expose 的
`CREATE TRIGGER` 均为裸创建，重复执行报错。agent 域两个文件的
`CREATE SCHEMA IF NOT EXISTS` 与扩展安装幂等，但文件内建表/建索引仍为裸
创建；函数为 `CREATE OR REPLACE` 不足以让文件整体可重复。全量重建先
`DROP DATABASE`，再从步骤 1 开始。

## 部署后授权

dbdeploy 在部署事务内自动补齐；手工部署时以超级用户执行一次：

```sql
GRANT CONNECT ON DATABASE <库名> TO grape_u, test_u;
```

`grape_u` 供应用运行连接，`test_u` 供演示/验收。CONNECT 依赖库名，无法写进
`role.sql`。此后应用 DSN 以 `grape_u` 连接，勿用超级用户（会绕过 RLS）。

## 升级

- **种子**：`ON CONFLICT DO NOTHING` 对已部署库可重跑但不覆盖已有行；种子
  行修正需手工 `UPDATE` 或重建库。生产种子只含根租户自举数据，历史库中的
  旧演示租户/用户/部门/basic 矩阵不会被新种子删除；模型字典（`model_kind`
  / `model_provider`，`120_sys_dict.sql`）与平台模型默认行（`110_sys_model.sql`）
  在历史库重跑后补入；字典标签翻译在 `sys_translation` 46–56、配置翻译
  57–63，重放不冲突。
- **schema/91_knowledge.sql / 92_model.sql**：历史库追加对象（`kb_library` /
  `kb_document` / `sys_model` 与索引），以超级用户/表 owner 执行；裸创建，
  重复执行报错；RLS 策略随 `rls.sql` 重放（`CREATE POLICY` 亦为裸创建，
  历史库需手工补对应策略或重建）。
- **kb_library.chunk_delimiter（91_knowledge.sql）**：已有 `kb_library` 的历史库
  以表 owner 手工追加列。先以空串默认追加（存量库回填空串，保持纯固定窗口
  切分行为不变），再把默认改为段落空行（之后新建的库启用区分串切分），
  最后补长度约束与列注释（与 91_knowledge.sql 逐字一致）：

  ```sql
  ALTER TABLE public.kb_library ADD COLUMN chunk_delimiter text NOT NULL DEFAULT '';
  ALTER TABLE public.kb_library ALTER COLUMN chunk_delimiter SET DEFAULT E'\n\n';
  ALTER TABLE public.kb_library
      ADD CONSTRAINT kb_library_chunk_delimiter_chk CHECK (char_length(chunk_delimiter) <= 32);
  COMMENT ON COLUMN public.kb_library.chunk_delimiter IS '切分区分串：非空时先按该字符串切出段落块（区分串不入块），超过 chunk_size 的段落块再按固定窗口切分；空串表示不按区分串切分（纯固定窗口）。新建库缺省为两个换行（段落空行），至多 32 字符；历史库升级回填空串保持原行为 [@dynamic: exact @updatable: set]';
  ```

  新版本二进制的知识库查询依赖该列，历史库须先执行上述升级再部署。
  存量库要改用区分串切分时，经管理页编辑库的区分串后对条目执行重建索引。
- **kb_document kind 取值（91_knowledge.sql）**：历史库以表 owner 手工替换
  kind 与正文两条约束、刷新列注释（与 91_knowledge.sql 逐字一致）。存量行
  只有 text/file，天然满足新约束，无数据迁移：

  ```sql
  ALTER TABLE public.kb_document DROP CONSTRAINT kb_document_kind_chk;
  ALTER TABLE public.kb_document ADD CONSTRAINT kb_document_kind_chk CHECK (kind IN ('text', 'markdown', 'file'));
  ALTER TABLE public.kb_document DROP CONSTRAINT kb_document_text_chk;
  ALTER TABLE public.kb_document ADD CONSTRAINT kb_document_text_chk CHECK (kind = 'file' OR (content IS NOT NULL AND btrim(content) <> ''));
  COMMENT ON COLUMN public.kb_document.kind IS '条目类型：text（纯文本，区分串切分）/ markdown（Markdown，标题切分）/ file（文件解析条目，解析产物走标题切分；摄取入口暂未开放） [@dynamic: exact]';
  COMMENT ON COLUMN public.kb_document.content IS '条目正文（text / markdown 必填非空白；file 解析成功前可空；向量化来源）';
  ```

  旧版本二进制不产出 markdown 条目，不受旧约束影响；新版二进制写入的
  markdown 条目会被存量库的旧 `kb_document_kind_chk` 拒绝，历史库须先执行
  上述约束升级再部署新版二进制。
- **kb_library.contextual_chunking（91_knowledge.sql）**：已有 `kb_library`
  的历史库以表 owner 手工追加列与列注释（与 91_knowledge.sql 逐字一致）。
  存量库回填缺省 false（不启用上下文前缀，摄取行为不变）：

  ```sql
  ALTER TABLE public.kb_library ADD COLUMN contextual_chunking boolean NOT NULL DEFAULT false;
  COMMENT ON COLUMN public.kb_library.contextual_chunking IS '上下文检索开关：开启后摄取在切分之后、向量化之前为每块生成上下文前缀（本文出处 + 本块要旨），前缀只拼入向量输入，不改变切片正文与锚点；前缀生成失败按质量增强降级为无前缀继续摄取 [@dynamic: exact @updatable: set]';
  ```

  新版本二进制的知识库写面依赖该列，历史库须先执行上述升级再部署。
  开启开关的库对条目执行重建索引后生效（存量切片的向量输入不带前缀）。
- **kb_library 归属三元组与 `@scope`（91_knowledge.sql）**：历史库以表 owner
  手工追加三列、索引与注释（与 91_knowledge.sql 逐字一致）：

  ```sql
  ALTER TABLE public.kb_library ADD COLUMN owner_user_id int8 NULL;
  ALTER TABLE public.kb_library ADD COLUMN owner_dept_id int8 NULL;
  ALTER TABLE public.kb_library ADD COLUMN owner_user_group_id int8 NULL;
  CREATE INDEX kb_library_owner_user_idx ON public.kb_library USING btree (owner_user_id);
  CREATE INDEX kb_library_owner_dept_idx ON public.kb_library USING btree (owner_dept_id);
  CREATE INDEX kb_library_owner_group_idx ON public.kb_library USING btree (owner_user_group_id);
  COMMENT ON TABLE public.kb_library IS '知识库表：租户内知识条目的归类与管理面，条目正文切分后的向量落 agent.agent_document [@scope: owner_user_id in_any fn_accessible_user_ids(user_id:int) owner_dept_id in_any fn_accessible_dept_ids(user_id:int) owner_user_group_id in_any fn_accessible_group_ids(user_id:int) @default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';
  COMMENT ON COLUMN public.kb_library.owner_user_id IS '归属用户 ID（创建人缺省归属，NULL=无该归属） [@dynamic: exact, membership]';
  COMMENT ON COLUMN public.kb_library.owner_dept_id IS '归属部门 ID（NULL=无该归属） [@dynamic: exact, membership]';
  COMMENT ON COLUMN public.kb_library.owner_user_group_id IS '归属用户组 ID（NULL=无该归属） [@dynamic: exact, membership]';
  ```

  存量行归属为 NULL，按数据范围口径只有全租户角色可见（含其创建人，若其
  没有全租户角色）。如需保持「创建人可见」的平滑口径，追加列后回填一次：

  ```sql
  UPDATE public.kb_library
  SET owner_user_id = created_by
  WHERE owner_user_id IS NULL AND owner_dept_id IS NULL AND owner_user_group_id IS NULL;
  ```

  新版本二进制的知识库读面（单行读、动态 List、检索可见库集合）依赖该
  三元组，历史库须先升级再部署。`@scope` 只影响生成代码，不改 DDL。
- **kb_document 文件元数据列与 kb_file 介质表（91_knowledge.sql / rls.sql）**：
  历史库以表 owner 手工执行（与 91_knowledge.sql 逐字一致）。kb_document
  追加四列与约束（存量行 kind 只有 text/markdown，天然满足新约束，无数据
  迁移）；kb_file 为裸创建，重复执行报错；RLS 策略与 ENABLE/FORCE 同批补齐
  （CREATE POLICY 亦为裸创建）：

  ```sql
  ALTER TABLE public.kb_document ADD COLUMN file_id int8 NULL;
  ALTER TABLE public.kb_document ADD COLUMN file_name varchar(256) NULL;
  ALTER TABLE public.kb_document ADD COLUMN mime_type varchar(128) NULL;
  ALTER TABLE public.kb_document ADD COLUMN file_size int8 NULL;
  ALTER TABLE public.kb_document
      ADD CONSTRAINT kb_document_file_chk CHECK (kind <> 'file' OR (file_name IS NOT NULL AND mime_type IS NOT NULL));
  COMMENT ON COLUMN public.kb_document.file_id IS '原件介质行 ID（kb_file，本租户内由用例校验；仅 kind=file 非空） [@dynamic: exact]';
  COMMENT ON COLUMN public.kb_document.file_name IS '原件文件名（保留上传原名，与嗅探 MIME 不一致时以 MIME 为准；仅 kind=file 非空） [@dynamic: fuzzy, exact]';
  COMMENT ON COLUMN public.kb_document.mime_type IS '原件 MIME（内容嗅探结果，不信任客户端声明；仅 kind=file 非空） [@dynamic: exact]';
  COMMENT ON COLUMN public.kb_document.file_size IS '原件字节数（≤ 20MB，与 oss_object 上限一致；仅 kind=file 非空） [@dynamic: exact, range]';

  CREATE TABLE public.kb_file
  (
      id          int8                                                   NOT NULL,
      object_key  text                                                   NOT NULL,
      etag        varchar(64)                                            NOT NULL,
      sha256      varchar(64)                                            NOT NULL,
      mime_type   varchar(128)                                           NOT NULL,
      file_size   int8                                                   NOT NULL,
      created_by  int8                                                   NOT NULL,
      created_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
      updated_by  int8                                                   NOT NULL,
      updated_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
      deleted_at  timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
      tenant_id   varchar(16)                                            NOT NULL,
      CONSTRAINT kb_file_pkey PRIMARY KEY (id)
  );
  COMMENT ON TABLE public.kb_file IS '文件介质表：kind=file 知识条目的原件介质元数据（对象字节落内嵌 OSS，object_key 指向），租户表（RLS 在 rls.sql）；留原件是为了解析器升级后能重解析';
  COMMENT ON COLUMN public.kb_file.id IS '介质行 ID（kb_document.file_id 引用）';
  COMMENT ON COLUMN public.kb_file.object_key IS '对象键（内嵌 OSS 的 S3 key，形如 kb-file/<发号 ID>，URL 安全 ASCII；表只存元数据，将来切真实 OSS 语义不变）';
  COMMENT ON COLUMN public.kb_file.etag IS '对象内容 MD5 十六进制（S3 ETag 语义，写入 OSS 时的返回值）';
  COMMENT ON COLUMN public.kb_file.sha256 IS '原件内容 sha256 十六进制（介质行的内容身份，与 etag 的 MD5 互为冗余校验）';
  COMMENT ON COLUMN public.kb_file.mime_type IS '原件 MIME（内容嗅探结果，不信任客户端声明）';
  COMMENT ON COLUMN public.kb_file.file_size IS '原件字节数（≤ 20MB，与 oss_object 上限一致，由应用层限流保证）';
  COMMENT ON COLUMN public.kb_file.created_by IS '创建人（上传操作者）';
  COMMENT ON COLUMN public.kb_file.created_at IS '创建时间（上传时间）';
  COMMENT ON COLUMN public.kb_file.updated_by IS '更新人';
  COMMENT ON COLUMN public.kb_file.updated_at IS '更新时间';
  COMMENT ON COLUMN public.kb_file.deleted_at IS '删除时间（宿主条目删除时同批软删，OSS 对象尽力清理） [@internal]';
  COMMENT ON COLUMN public.kb_file.tenant_id IS '租户 ID [@internal]';

  CREATE POLICY kb_file_tenant_isolation
      ON public.kb_file
      USING (tenant_id = current_setting('app.tenant_id', true))
      WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
  ALTER TABLE public.kb_file
      ENABLE ROW LEVEL SECURITY;
  ALTER TABLE public.kb_file
      FORCE ROW LEVEL SECURITY;
  ```

  另需刷新 kb_document 的 kind / content 列注释（与本批 91_knowledge.sql
  逐字一致，此前升级写入的是「摄取入口暂未开放」的旧口径）：

  ```sql
  COMMENT ON COLUMN public.kb_document.kind IS '条目类型：text（纯文本，区分串切分）/ markdown（Markdown，标题切分）/ file（文件解析条目，原件经解析产 markdown 后走标题切分） [@dynamic: exact]';
  COMMENT ON COLUMN public.kb_document.content IS '条目正文（text / markdown 必填非空白；file 解析成功前可空，解析产物入库后为 markdown；向量化来源）';
  ```

  新版本二进制的文件摄取（kind=file 创建 / 重解析 / 删除清理）依赖上述对象，历史库须先升级再部署；未升级时文件上传返回
  500（缺列），text / markdown 摄取不受影响。内嵌 OSS（schema/93_oss.sql）
  是文件摄取的前置依赖，见下文该文件的升级条目。
- **98_model_write.sql**：历史库追加对象，以部署超级用户执行；只含
  `CREATE OR REPLACE FUNCTION`，可重复。平台模型写端点（`sys:model:platform:*`）
  与模型管理页菜单（279–286）依赖 `050_sys_menu.sql` / `060_sys_role_menu.sql`
  重放补入，补齐前平台写返回 403，属预期收紧。
- **agent_vector.sql**：历史库追加对象，以超级用户单独执行（扩展/schema 判存，
  建表建索引裸创建）。调整嵌入空间须 conf 与 DDL 成对变更（`conf.vector.embedders`
  加条目 + 对应维度索引，装配期探针逐维度对账）；单维旧表升级到多空间形状
  是结构变更，走全量重建；换默认空间的迁移靠应用侧重摄取（source_id 幂等
  覆盖并清除旧切片）。
- **95_agent_audit.sql / 97_agent_retrieval_log.sql / 99_model_call_log.sql**：
  历史库追加对象，以部署角色（与 role.sql 同一执行者）执行；裸创建，重复
  执行报错；`grape_u` 的 DML 授权由 role.sql 默认权限覆盖。
- **96_agent_grant.sql**：历史库追加对象，以超级用户执行。授权管理权限码的
  种子（菜单 252/253 与角色 101 映射）随 `050_sys_menu.sql` /
  `060_sys_role_menu.sql` 执行；已部署库在种子执行前判定返回 403，属预期
  收紧。
- **schema/93_oss.sql**：历史库追加对象（`oss_object` 表），以部署角色
  （与 role.sql 同一执行者）执行；裸创建，重复执行报错；文件尾部的
  grape_u / test_u DML 授权为幂等语句可重复。表设施面无 RLS、无策略
  需补。未部署本表的新版二进制以内嵌 OSS 启用（`oss.enabled=true` 且
  provider=local）时启动即失败，属预期收紧；未启用内嵌 OSS 时无影响。
- **98_menu_write.sql / 98_config_write.sql / 98_tenant_guard.sql**：历史库
  追加对象，以部署超级用户执行；只含 `CREATE OR REPLACE FUNCTION`，可重复。
  函数属主 = 执行者，换执行者重跑会连带改属主，部署口径以 dbdeploy 为准。
  未部署 `fn_tenant_active_child_count` 的历史库在删除租户时调用失败，属
  预期收紧。历史库另需补建子租户计数索引：
  `CREATE INDEX IF NOT EXISTS idx_sys_tenant_parent_id ON public.sys_tenant (parent_id) WHERE deleted_at IS NULL;`
  （新库由 `schema/20_tenant.sql` 携带）。
- **菜单种子**：历史库按 `seed.Files` 重跑补入缺失权限点（246–249/254–273/274–286，其中 274–286 为知识库与模型管理），
  不覆盖既有行；route_path/component 修正需手工 `UPDATE` 或重建库；旧演示
  范围明细（`sys_role_id_scope` 1001）不会被删除。种子补齐前新增权限码返回
  403（超级管理员同样失去这些码），补齐后恢复。
- **sys_user_role_mapping 撤销改软删**：结构变更。新表以应用层发号 `id` 为
  主键，加 `deleted_by` / `deleted_at`，原 `(user_id, role_id)` 主键由部分
  唯一索引 `(tenant_id, user_id, role_id) WHERE deleted_at IS NULL` 替代；
  历史库需手工迁移或重建，未迁移库在撤销/重复授予路径会因缺列或主键冲突
  报错。判定函数经清单 `CREATE OR REPLACE` 重放即可生效。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`
（N=2^14, r=8, p=1）。登录校验识别该格式，通过后由应用透明重哈希为
argon2id，无需批量重置。
