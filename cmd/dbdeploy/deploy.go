package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
)

// Deploy 执行数据库全量部署。流程（对应仓库根 deploy_db.md）：
//
//  1. 校验部署清单完整性（拒绝 CREATE DATABASE / CONCURRENTLY 破坏
//     单事务前提）；
//  2. 以管理 DSN 连接 postgres 库，目标库不存在则创建（CREATE DATABASE
//     不能在事务块内，独立于后续事务执行）；已存在则沿用并声明幂等边界；
//  3. 连接目标库，单事务内按 DeployFiles 清单顺序执行全部文件
//     （simple protocol；role.sql 的口令占位符在内存中替换，残留即拒绝）；
//  4. 事务内补 GRANT CONNECT（库名动态拼接，role.sql 无法预知库名）；
//  5. 任一步失败整体回滚并上抛错误。
//
// 幂等边界：角色（DO 块判存）与种子（ON CONFLICT DO NOTHING）可重复
// 执行；结构 DDL 为裸创建，重复执行报错，全量重建先手动 DROP DATABASE。
// logf 为日志回调（面向运维），口令绝不进入日志与错误文本。
func Deploy(ctx context.Context, opts *Options, logf func(format string, args ...any)) error {
	dir, err := pg_sqlc.DeployDir()
	if err != nil {
		return err
	}
	if err := pg_sqlc.ValidateDeployFiles(dir); err != nil {
		return err
	}
	logf("deploy manifest validated (%d files)", len(pg_sqlc.DeployFiles))

	if err := ensureDatabase(ctx, opts, logf); err != nil {
		return err
	}
	return deployInTransaction(ctx, opts, dir, logf)
}

// ensureDatabase 连接 postgres 库，目标库不存在时创建。CREATE DATABASE
// 不能在事务块内执行，故在进入目标库事务之前独立完成。
func ensureDatabase(ctx context.Context, opts *Options, logf func(format string, args ...any)) error {
	cfg, err := pgx.ParseConfig(opts.AdminDSN)
	if err != nil {
		return fmt.Errorf("parse admin dsn: %w", err)
	}
	// 建库阶段固定连 postgres 库：DSN 中的库名部分被忽略
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect to postgres database as superuser: %w", err)
	}
	defer admin.Close(ctx)

	var exists bool
	if err := admin.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", opts.Database).Scan(&exists); err != nil {
		return fmt.Errorf("check target database existence: %w", err)
	}
	if exists {
		logf("target database %s already exists, reusing it (structural DDL fails on rerun; drop the database first for a full rebuild)", opts.Database)
		return nil
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{opts.Database}.Sanitize()); err != nil {
		return fmt.Errorf("create target database %s: %w", opts.Database, err)
	}
	logf("target database %s created", opts.Database)
	return nil
}

// deployInTransaction 连接目标库，单事务内按清单顺序执行全部部署文件，
// 失败整体回滚。
func deployInTransaction(ctx context.Context, opts *Options, dir string, logf func(format string, args ...any)) error {
	cfg, err := pgx.ParseConfig(opts.AdminDSN)
	if err != nil {
		return fmt.Errorf("parse admin dsn: %w", err)
	}
	cfg.Database = opts.Database
	// 部署文件含多语句与函数体，必须走 simple protocol
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect to target database %s: %w", opts.Database, err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin deploy transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // Commit 成功后为无害空操作

	total := len(pg_sqlc.DeployFiles)
	for i, name := range pg_sqlc.DeployFiles {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read deploy file %s: %w", name, err)
		}
		sql := pg_sqlc.ResolveDeployPasswords(string(content), opts.GrapeUPassword, opts.TestUPassword)
		if pg_sqlc.HasResidualPlaceholder(sql) {
			return fmt.Errorf("deploy file %s still contains a password placeholder after substitution, refusing to execute (transaction rolled back)", name)
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("execute deploy file %s, deploy transaction rolled back: %w", name, err)
		}
		logf("[%d/%d] %s applied", i+1, total, name)
	}

	// CONNECT 授权依赖库名，role.sql 无法预知：在目标库事务内以
	// 当前库名动态补齐（应用 DSN 与验收连接均依赖此授权）
	grant := fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO grape_u, test_u",
		pgx.Identifier{opts.Database}.Sanitize())
	if _, err := tx.Exec(ctx, grant); err != nil {
		return fmt.Errorf("grant connect to grape_u/test_u, deploy transaction rolled back: %w", err)
	}
	logf("connect privilege granted (grape_u, test_u -> %s)", opts.Database)

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit deploy transaction: %w", err)
	}
	logf("deployment finished: target database %s, %d deploy files", opts.Database, total)
	return nil
}
