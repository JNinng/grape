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
- **sys_model kind 取值放开 rerank（92_model.sql / 120_sys_dict.sql）**：历史库
  以表 owner 手工替换 kind 约束、刷新列注释（与 92_model.sql 逐字一致），
  并补种重排模型字典行与类型备注（120_sys_dict.sql 同批口径，`ON CONFLICT
  DO NOTHING` 不覆盖已有行，remark 需手工 UPDATE）。存量行只有 text /
  embedding，天然满足新约束，无数据迁移：

  ```sql
  ALTER TABLE public.sys_model DROP CONSTRAINT sys_model_kind_chk;
  ALTER TABLE public.sys_model ADD CONSTRAINT sys_model_kind_chk CHECK (kind IN ('text', 'embedding', 'rerank'));
  COMMENT ON COLUMN public.sys_model.kind IS '模型类别：text / embedding / rerank（rerank 供检索重排，不携带维度） [@dynamic: exact, membership]';
  INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                    created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
  VALUES (3103, 1, 301, '重排模型', 'rerank', 'string', 3, NULL, 1, '2026-11-08 10:00:00.000', 1,
          '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
  UPDATE public.sys_dict_type SET remark = '文本 / 嵌入 / 重排模型类别枚举' WHERE id = 301 AND remark = '文本 / 嵌入模型类别枚举';
  ```

  旧版本二进制不产出 rerank 模型行，不受旧约束影响；新版二进制写入的
  rerank 行会被存量库的旧 `sys_model_kind_chk` 拒绝，历史库须先执行上述
  约束升级再部署新版二进制。不重排的部署无需配置（检索行为不变）。
- **98_model_write.sql**：历史库追加对象，以部署超级用户执行；只含
  `CREATE OR REPLACE FUNCTION`，可重复。平台模型写端点（`sys:model:platform:*`）
  与模型管理页菜单（279–286）依赖 `050_sys_menu.sql` / `060_sys_role_menu.sql`
  重放补入，补齐前平台写返回 403，属预期收紧。
- **agent_vector.sql**：历史库追加对象，以超级用户单独执行（扩展/schema 判存，
  建表建索引裸创建）。调整嵌入空间须 conf 与 DDL 成对变更（`conf.vector.embedders`
  加条目 + 对应维度索引，装配期探针逐维度对账）；单维旧表升级到多空间形状
  是结构变更，走全量重建；换默认空间的迁移靠应用侧重摄取（source_id 差量
  对账：旧空间切片标记 `superseded_at` 退役，保留期内可反查、由运维按
  保留期清理）。

  本批 `agent_vector.sql` 只改 `id` / `metadata` 两列的 `COMMENT ON COLUMN`
  文本（切片 ID 形态与退役标记语义），**无 DDL、不需 `make sqlc`**。该文件
  不是幂等重放脚本，历史库的列注释不会随部署自动刷新；需要同步注释时以
  表 owner 单独执行：

  ```sql
  COMMENT ON COLUMN agent.agent_document.id IS '文档 ID（调用方生成，租户内唯一，承载框架字符串 ID）：知识切片为 <sourceID>#<index>#<hash8> 形态（hash 取嵌入空间键 + 切片内容，内容或空间变化即出新 ID，历史 ID 永不指向新内容）';
  COMMENT ON COLUMN agent.agent_document.metadata IS '文档元数据（jsonb）：含仓储注入的 tenant_id/user_id/embedder/embed_dim 与调用方自定义键值，过滤经容器运算符整值下发；superseded_at（RFC3339）为重摄差量对账写入的退役标记，键存在且非 JSON null 即非现行切片（检索/管理枚举/Count 以 is_null 排除，Get 复现反查不过滤），整行 Update 替换 metadata 即清除（复活）';
  ```

- **agent_document.lexical 词法索引列与 GIN 索引（agent_vector.sql）**：已有
  `agent_document` 的历史库以表 owner 手工追加列、刷新表/列注释并建部分
  GIN 索引（与 agent_vector.sql 逐字一致）。存量行的 lexical 保持 NULL、
  不做一次性迁移，对条目执行重建索引（重摄取）时由应用侧分词器回填：

  ```sql
  ALTER TABLE agent.agent_document ADD COLUMN lexical tsvector NULL;
  COMMENT ON TABLE agent.agent_document IS '租户向量文档（RAG 检索，多嵌入空间）：租户隔离由应用层适配器强制，RLS 仅作可选加强；embedder/embed_dim 与 embedding 由 CHECK 绑定，空间注册表见 conf.vector.embedders，每维度索引见本文件表达式部分 HNSW，词法索引列 lexical 由应用侧分词器产出、GIN 部分索引承接词法召回';
  COMMENT ON COLUMN agent.agent_document.lexical IS '词法索引（tsvector）：应用侧由分词器对切片正文产出（词元按出现序带位置），Add/Update 同批填充；可空——分词为空（无有效词元）或历史库存量行（重摄回填）落 NULL，词法检索经部分 GIN 索引只扫非空行';
  CREATE INDEX agent_document_lexical_gin
      ON agent.agent_document USING gin (lexical)
      WHERE lexical IS NOT NULL;
  ```

  新版本二进制的装配探针与词法召回（写入建索引 + 检索分词）依赖该列，
  历史库须先执行上述升级再部署（缺列启动即失败）；词法索引内容在应用侧
  产出（Tokenizer 分词构造 tsvector），数据库侧不引入分词扩展。存量切片
  在重摄取前不参与词法召回（NULL 行被部分索引排除），混合检索自动降级为
  仅向量路，属预期行为。
- **95_agent_audit.sql / 97_agent_retrieval_log.sql / 99_model_call_log.sql**：
  历史库追加对象，以部署角色（与 role.sql 同一执行者）执行；裸创建，重复
  执行报错；`grape_u` 的 DML 授权由 role.sql 默认权限覆盖。
- **agent_retrieval_log.stages 阶段数组（97_agent_retrieval_log.sql）**：已有
  `agent_retrieval_log` 的历史库以表 owner 手工追加 `stages` 列、删除
  `rerank_before` / `rerank_after` 两列并刷新列注释（与本批 97 文件逐字一致）。
  rerank 两列自引入以来恒为 NULL（append-only 表删列安全，无数据迁移；
  删列避免与 stages 的双写歧义）：

  ```sql
  ALTER TABLE public.agent_retrieval_log ADD COLUMN stages jsonb NULL;
  ALTER TABLE public.agent_retrieval_log DROP COLUMN rerank_before;
  ALTER TABLE public.agent_retrieval_log DROP COLUMN rerank_after;
  COMMENT ON COLUMN public.agent_retrieval_log.filter_summary IS '过滤条件摘要（jsonb）：装饰器单路行记 ids/metadata/min_score 形态；编排层写入的检索行记 {"kb_ids":[…]} 可见库集合（库范围即调用方可见的过滤面）；metadata 值经 retrieval 类别脱敏；脱敏截断破坏 JSON 结构时以 {"summary_truncated": …} 摘要形态落库（应用层兜底）；无条件为 NULL';
  COMMENT ON COLUMN public.agent_retrieval_log.hits IS '最终命中数组（jsonb）：元素为 {"document_id","score","anchor"}，按分数降序（装配重排层时为 rerank 分、否则为 RRF 融合分——与 stages 各阶段的分数口径一致），上限 50 条（应用层截断，超限不再另行标记——检索上限 20 之下为防御直连契约的超大 Limit 而设）；仅存服务端产出的标识与数值，不含文档正文。检索行为记录命中；摄取行为复用本列记录作用文档（单元素，anchor 取切片元数据）';
  COMMENT ON COLUMN public.agent_retrieval_log.stages IS '检索阶段数组（jsonb，仅编排层写入的检索行）：数组顺序即执行序，后一阶段的输入序即前一阶段的输出序，不重复记录。元素为 {"stage","query_text","limit","model","candidates","units","error","duration_ms"}——stage 取 vector / lexical（召回路，query_text 记该路的变体文本）、fusion（RRF 融合）、rerank（model 记重排模型键）、pack（回包聚合）；candidates 为该阶段输出候选（元素形态同 hits，上限 50 条，分数按阶段自解释——向量相似度 [0,1] / 词法分 / RRF 分 / rerank 模型分，同阶段可比、跨阶段不可比，不做归一）；units 仅 pack 承载，元素为 {"mode","source_id","document_ids","heading_path","anchor","tokens","admitted","reason"}——mode 取 chunk/window/section/document，tokens 为回包 token 量级估算，admitted 标记是否入包；未入包时 reason 说明原因：merged-同源收编（同源 document 单元已入包，其余单元内容已被包含）、redundant-保底直回单元在来源已有入包单元时未入包（不参与深度补深、仅在广度段兜底，来源已有入包单元时只会重复内容）、truncated-预算截断（优先级 merged > redundant > truncated，与装配期跳过顺序一致），admitted=true 时省略 reason（对照 fusion 序与 units 可还原预算截断）；limit 为该阶段的上限，口径随阶段：召回与融合为候选条数上限（2×top_k），rerank 为输出上限（最终 top_k，candidates 记重排输入候选的重排后序、条数可为 2×top_k），pack 为回包 token 预算；error 记阶段失败摘要（候选空，降级路径由调用方继续）；duration_ms 为该阶段耗时。装饰器单路行本列为 NULL，不伪造空阶段';
  COMMENT ON COLUMN public.agent_retrieval_log.query_text IS '检索查询文本：经 pkg/redact 的 retrieval 类别脱敏（标准注册表 512 rune 截断）+ 应用层兜底截断 4096 rune；编排层写入的检索行记主查询文本，查询变体在各召回阶段的 stages.query_text 记录（与主查询同脱敏口径）；装饰器单路行取 ctx 携带的查询事实（Embedder 调用文本），无事实时不伪造（NULL）；pipeline 来源为 NULL——摄取正文不重复留痕，经 hits 的 document_id 反查 agent_document';
  COMMENT ON COLUMN public.agent_retrieval_log.requested_top_k IS '请求的 top_k：NULL 为未显式指定。编排层写入的检索行取归一化后的 top_k（缺省 5、硬上限 20 在用例内钳制），即最终命中上限；装饰器单路行取存储契约实际收到的 Limit——top_k 硬上限钳制（20）发生在本值上游，tool 来源在框架适配器（internal/agent/tools/vectorstore.go）、console 来源在知识库检索用例（internal/biz/knowledge_search.go）；编排行的逐路召回宽度（2×top_k）在 stages 各召回阶段的 limit 记录；tool 来源的模型原始入参可对账 agent_tool_call_log 的参数摘要。非检索操作为 NULL';
  COMMENT ON COLUMN public.agent_retrieval_log.effective_top_k IS '实际生效的命中上限：请求值非正（含 NULL）时取存储层默认页大小 10；编排层写入的检索行与请求值同值（最终命中上限）；非检索操作为 NULL';
  ```

  升级影响与执行顺序：本变更是**破坏性列变更**，DDL 与新二进制必须在同一
  维护窗内一起生效，不存在「先升级 DDL 后择期换二进制」的兼容区间。
  HEAD 二进制的生成代码按名引用 `rerank_before` / `rerank_after`（INSERT
  列清单、`RETURNING` 与 List SELECT 三处），因此：

  - DDL 先于旧二进制下线：旧二进制的留痕写入（装饰器行，含检索与摄取全部
    来源）按名引用已删列，报 `42703 undefined_column`，装饰器只记警告、
    行永久丢失；留痕查询端点（/v1/agent/retrieval-logs）500。检索本身仍返回
    （留痕是旁路观察面），但窗口内无新证据行，复现/管理页不可用。
  - 新二进制先于 DDL：新二进制的 INSERT（`stages`）与 List SELECT 同样
    报 `42703`，装饰器行与混合检索的编排行**都**写不进（共用同一条
    INSERT，不止编排行），检索照常返回但证据行全部丢失，查询端点 500。

  因此以「停服 → 执行本批 DDL → 部署新二进制 → 启动」为执行顺序；滚动
  更新在同一维护窗内完成，窗口内的留痕证据行不可恢复（既有历史行不受影
  响，rerank 两列自引入以来恒为 NULL，删列无数据损失）。上方五条列注释
  （filter_summary / hits / query_text / requested_top_k / effective_top_k）
  为同批口径刷新，与本批 97 文件逐字一致。
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
- **菜单种子**：历史库按 `seed.Files` 重跑补入缺失权限点（246–249/254–273/274–288，其中 274–278 为知识库、279–286 为模型管理、287–288 为模型调用），
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
