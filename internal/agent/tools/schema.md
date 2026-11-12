# 工具入参 / 出参声明 schema 速查

工具声明面的 schema 由框架 `tool/function` 按 Go 类型自动生成（见
[Function Tools](https://trpc-group.github.io/trpc-agent-go/zh/tool/#function-tools)），
本包只在类型上用 `jsonschema` tag 补充说明与约束，不手写 schema。本文件是
tag 键面、逗号陷阱与生成 / 禁用面的速查，「新增工具」按此执行。

## 职责分工

- **工具 description**：只写工具用途与安全声明。数据面纪律（检索结果是
  UNTRUSTED 围栏包裹的文档摘录、库名是租户写入的元数据）属于安全声明；
  参数取值说明、字段清单不写进描述。
- **字段 schema**：入参 / 出参每个字段的取值方式、范围、默认值与必填性经
  `jsonschema` tag 落在 `InputSchema` / `OutputSchema`，随工具声明一起下发给
  模型。

参数文档堆在 description 里会与 schema 形成两份可能漂移的说明；字段级声明
跟随类型定义，改字段即改文档。

## tag 键面（框架 v1.11.2）

| 键 | 形式 | 作用 |
| --- | --- | --- |
| `description=` | `jsonschema:"description=..."` | 字段说明；旧的 `description:"..."` tag 仍被识别但优先级更低 |
| `enum=` | 可重复 | 值枚举集合，按字段类型转换（string / int / float / bool） |
| `pattern=` | 仅 string 字段 | 值正则约束 |
| `required` | 独立标记，无值 | 显式必填 |

框架不支持 `default=` / `minimum=` / `maximum=` / `format=` 等键。默认值与
数值边界没有 tag 表达位：默认值写进 `description` 文本（如
`defaults to 5`），实际缺省与钳制由工具层 / biz 用例实现；更严格的边界
只能写进描述，或改用手写 schema。

## 必填性判定

- 非指针且无 `omitempty` 的字段自动 required。
- 指针字段或带 `omitempty` 的字段默认可选；加 `required` 标记可强制必填。
- 「可选」的语义（缺省值、省略后的行为）写在字段说明里，例如
  `omit to search every library visible to the caller`。

## 逗号陷阱

框架按英文逗号切分 tag 项、逐项按 `key=value` 解析：

- `description=` / `pattern=` 的值内不得含英文逗号，否则后半段会被当作新键
  解析（未知键被忽略，说明被截断）。
- 需要逗号、多段枚举说明或 `default` / `minimum` / `format` / `anyOf` 等
  复杂约束时，改用手写 schema：`function.WithInputSchema` /
  `function.WithOutputSchema`。提供后自动生成被**完全跳过**，须覆盖全部
  字段（含 `type` 与 `required`）。

## OutputSchema 生成面

- 出参 Go 类型走同一生成器，字段 tag 投影进 `OutputSchema`，与
  `Declaration()` 一并下发；出参字段清单不写进工具 description。
- 非递归结构内联展开，字段说明生效；数组以 `items` 表达元素 schema。
- 递归类型以 `$ref` + `$defs` 表达，**指向递归类型的字段自身 tag 说明会
  丢失**（框架在 `$ref` 分支不应用字段 tag，内层字段说明仍保留）。递归结构
  需要该层说明时改用手写 schema。
- `function.WithOutputSchema` 传入自定义 schema 时同样跳过自动生成。

## 本包约定

- 字段说明用英文、短语式、不带句末句号；安全声明与数据面纪律才写进工具
  description。
- `int64` 标识跨 JSON 文本边界一律以字符串出参（超过 2^53 后 JS 消费方按
  double 解析失真），字段说明注明 `decimal string` 与回传方式。
- 出参 `notice` 恒带不可信数据声明；`hits` / `libraries` 为空表示无结果而
  非错误，语义写进字段说明。
- **tag 只是模型面文档，不替代服务端校验**：query 长度、top_k 上限、库 ID
  可见性等边界以工具层与 biz 用例的显式校验为最终防线。框架调用路径只做
  JSON 反序列化，不按 schema 校验入参。
- 声明面改动须同步本节与本文件的示例说明；分级表与工具登记同批提交（见
  [../README.md](../README.md)「工具清单与分级表」）。
