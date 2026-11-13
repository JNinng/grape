// dbdeploy 是对 grape 关系存储执行全量部署的命令：以超级用户 DSN 幂等建库，
// 单事务内按 internal/data/pg_sqlc/deploy 的生产清单执行 sqlc/ 下全部 SQL，
// 并补齐 CONNECT 授权（集成测试经 TestDeployFiles 追加测试种子）。
//
// 敏感口令建议经 GRAPE_DEPLOY_* 环境变量提供，避免进入 shell 历史；参数
// 解析见 ParseOptions，部署顺序与幂等边界见仓库根 deploy_db.md。
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
