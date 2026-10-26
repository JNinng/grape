// dbdeploy 命令：grape 关系存储的全量部署。
//
// 以超级用户 DSN 连接 PostgreSQL，幂等创建目标库后，在单事务内按
// DeployFiles 清单（internal/data/pg_sqlc/deploy.go，与集成测试共用）
// 顺序执行 sqlc/ 下全部部署文件，并动态补齐 CONNECT 授权。幂等边界与
// 手工序列说明见仓库根 deploy_db.md。
//
// 用法：
//
//	go run ./cmd/dbdeploy \
//	  --admin-dsn postgres://user:pass@127.0.0.1:5432/postgres \
//	  --database grape
//
// 必填参数也可经环境变量提供（推荐，避免口令进 shell 历史）：
//
//	GRAPE_DEPLOY_ADMIN_DSN / GRAPE_DEPLOY_GRAPE_U_PASSWORD / GRAPE_DEPLOY_TEST_U_PASSWORD
package main

import (
	"context"
	"log"
	"os"
)

func main() {
	opts, err := ParseOptions(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatalf("[dbdeploy] %v", err)
	}
	if err := Deploy(context.Background(), opts, func(format string, args ...any) {
		log.Printf("[dbdeploy] "+format, args...)
	}); err != nil {
		log.Fatalf("[dbdeploy] deployment failed: %v", err)
	}
}
