# internal/cmd

server 二进制子命令的实现包：每个子命令一个子目录，对外只导出统一形态的
`Run(args []string, stdout, stderr io.Writer) int`（返回进程退出码，输出只
写传入的 writer），由 [cmd/server/main.go](../../cmd/server/main.go) 的
分发层调用。命令名与 flag 的用法文本（如 `server configinit`）在各包内
自描述。不触达 biz / data，只消费 `pkg/` 与 `conf`。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [configinit/configinit.go](configinit/configinit.go) | `server configinit`：解析 `-dir` / `-force`，内嵌模板按字节写入目标目录，不展开 `${ENV:default}` 占位。 |
| [configinit/template/config.yaml](configinit/template/config.yaml) | 配置模板单一事实源，与 conf.proto 对应。 |
| [secretctl/secretctl.go](secretctl/secretctl.go) | `server secretctl`：`algorithms` 列出支持的完整算法标识，`keygen <kind>` 生成可直接填入登记表的密钥材料（首行 `kind:`，字段行与登记表同名）。 |
| [version/version.go](version/version.go) | `server version`：输出 pkg/version 的单行构建信息摘要（注入契约见 pkg/version）。 |

## 关键约束

- 模板必须留在 `configinit/template/`，**勿移入 `configs/`**：该目录会被
  Kratos file source 整体加载，模板与运行配置必须分离。
- 各包只做入口参数处理与自身输出，不装配服务依赖；serve 路径的装配
  （配置加载、日志、Wire）留在 cmd/server。

## 测试

- configinit：模板经与 serve 相同的加载路径可解析出必需配置段；默认关闭
  的模型可靠性段保持零包装；不覆盖/覆盖/逐字节输出/自动建目录等文件语义；
  多余位置参数被拒。
- 模板同步：`configinit/template/config.yaml` 正文须与
  `configs/config.yaml` 一致，偏离时测试报错，防止两处配置漂移。
- secretctl：算法清单与 `pkg/secret.SupportedKinds` 同源；`keygen` 输出
  结构（`kind` 首行 + 字段行，对称仅 `key:`）与 base64 长度；生成材料经
  `pkg/secret.New` 装配校验并可往返加解密；未知算法非零退出且 stdout 不
  出现密钥行。
- version：输出包含全部构建字段；多余位置参数被拒。
- 子命令分发（未知命令、help、路由、首参为 flag 回落 serve）归
  cmd/server 的 `main_test.go`。
