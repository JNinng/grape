package main

import (
	"flag"
	"fmt"
	"regexp"
	"strings"
)

// Options 是 dbdeploy 的部署参数：来自命令行 flag（优先）或环境变量。
// 管理连接用独立的部署专用 DSN，不复用应用日常 conf。
type Options struct {
	// AdminDSN 超级用户 DSN；DSN 中的库名部分被忽略——建库阶段固定
	// 连接 postgres 库，随后才连接目标库。
	AdminDSN string
	// Database 目标库名；不存在时创建，已存在时直接沿用（结构 DDL
	// 重复执行会报错，全量重建先手动 DROP DATABASE）。
	Database string
	// GrapeUPassword 运行角色口令，替换 role.sql 的
	// {{GRAPE_U_PASSWORD}} 占位符；只在内存中使用，不落日志。
	GrapeUPassword string
	// TestUPassword 演示用户口令，替换 role.sql 的
	// {{TEST_U_PASSWORD}} 占位符。
	TestUPassword string
}

const (
	envAdminDSN       = "GRAPE_DEPLOY_ADMIN_DSN"
	envGrapeUPassword = "GRAPE_DEPLOY_GRAPE_U_PASSWORD"
	envTestUPassword  = "GRAPE_DEPLOY_TEST_U_PASSWORD"

	defaultDatabase = "grape"
)

// databaseNamePattern 约束目标库名为常规标识符：CREATE DATABASE 与
// GRANT CONNECT 都要拼接库名，收紧格式后按标识符引用拼接即可。
var databaseNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// ParseOptions 解析部署参数：flag 未显式给值时回落环境变量。
// getenv 抽象便于单测注入。必填项缺失或库名非法时返回明确文案。
func ParseOptions(args []string, getenv func(string) string) (*Options, error) {
	fs := flag.NewFlagSet("dbdeploy", flag.ContinueOnError)
	adminDSN := fs.String("admin-dsn", "", "superuser admin DSN (default "+envAdminDSN+"; database part is ignored)")
	database := fs.String("database", "", "target database, created when missing (default "+defaultDatabase+")")
	grapeUPwd := fs.String("grape-u-password", "", "grape_u password (default "+envGrapeUPassword+"; prefer the env var)")
	testUPwd := fs.String("test-u-password", "", "test_u password (default "+envTestUPassword+"; prefer the env var)")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("parse flags: %w", err)
	}

	opts := &Options{
		AdminDSN:       firstNonEmpty(*adminDSN, getenv(envAdminDSN)),
		Database:       firstNonEmpty(*database, getenv("GRAPE_DEPLOY_DATABASE"), defaultDatabase),
		GrapeUPassword: firstNonEmpty(*grapeUPwd, getenv(envGrapeUPassword)),
		TestUPassword:  firstNonEmpty(*testUPwd, getenv(envTestUPassword)),
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

func (o *Options) validate() error {
	if strings.TrimSpace(o.AdminDSN) == "" {
		return fmt.Errorf("missing admin DSN: provide it via --admin-dsn or %s (superuser, e.g. postgres://user:pass@host:5432/postgres)", envAdminDSN)
	}
	if !databaseNamePattern.MatchString(o.Database) {
		return fmt.Errorf("invalid database name %q: only letters, digits, underscore and $ are allowed, and it must start with a letter or underscore", o.Database)
	}
	if o.GrapeUPassword == "" {
		return fmt.Errorf("missing grape_u password: provide it via --grape-u-password or %s", envGrapeUPassword)
	}
	if o.TestUPassword == "" {
		return fmt.Errorf("missing test_u password: provide it via --test-u-password or %s", envTestUPassword)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
