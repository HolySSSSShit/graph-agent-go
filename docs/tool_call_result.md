📌 [技术设计] Harness 侧解决 MCP 上下文爆表：Shadow Write 句柄模式

核心问题：
底层 MCP 工具返回内容不可控（如单次返回 15KB+ 嵌套 JSON），频繁查询/下钻时易撑爆 LLM 上下文。

限制与原则：
只能在 Harness 侧进行改造，MCP 侧 Schema 不可控且随时可能变动。

解决思路：
采用 Shadow Write（阴影存储）+ 句柄 Preview 机制，将 MCP 原始数据与 LLM Context 进行物理隔离。

----------------------------------------
🏗️ 核心架构流程：
1. 拦截 (Intercept)：Harness 拦截所有 MCP 工具的原始返回值。
2. 阴影存储 (Shadow Storage)：原始数据全量写入 Harness 本地内存/磁盘，生成唯一 data_id（如 hdl_9a8f12）。
3. 骨架生成 (Skeleton Preview)：剥离庞大具体的 Values，仅提取 Key 结构/数组长度/前几行预览，控制在 300 Tokens 安全范围内塞入 Context。
4. 按需下钻 (Inspect)：Harness 侧注入内置工具 inspect_handle_data，LLM 根据 Preview 中的 Key 树地图，按需精准查询特定路径数据。

----------------------------------------
⚙️ 核心模块 Golang 实现规范：

1. 阴影存储模块 (ShadowStorage)
- 并发安全地暂存原始数据，带 TTL 自动过期释放内存。
- 数据结构：Key: hdl_ + 8位 UUID；Value: { RawData, CreatedAt }
- 内存保护：后台定时清理（如每 5min 执行），超时（如 30min）自动删除失效 Handle。

2. 安全渲染模块 (SafeRenderer)
- 数组处理：提取前 N 条基础字段生成 Markdown Table，尾部追加 *... N items hidden.*
- 嵌套 Object 处理：提炼顶层骨架（Key Tree），屏蔽深层大对象：
    * Array -> "[Array of N items]"
    * Deep Dict -> "[Object with keys: k1, k2...]"
    * Base Types -> 截断保留前 50 字符。

3. 注入下钻工具 (InspectHandleData)
- 工具名称：inspect_handle_data
- 输入参数：handle_id (string), json_path (string)
- 防暴保底：即使下钻查询，返回结果仍施加 2000 字符硬截断。

----------------------------------------
💡 LLM 上下文交互示例：

[Harness 注入 Context 的 Tool Output 示例]
**[Harness Notice]** Raw output intercepted & saved to handle: `hdl_9a8f12`

**Type:** Structural Skeleton Preview
{
"status": "success",
"product_id": "P10086",
"sku_details": "[Array of 3 items]",
"supplier_info": "[Object with keys: supplier_id, name, address]",
"audit_logs": "[Array of 210 items]"
}
> Tip for Agent: Call inspect_handle_data(handle_id="hdl_9a8f12", json_path="$.your.path") to get details.

[LLM 后续下钻调用]
- LLM 发起调用: inspect_handle_data(handle_id="hdl_9a8f12", json_path="$.supplier_info.name")
- Harness 拦截并返回: "供应链科技公司"

对于按日期、事件或对象列表组织的数据，不要分别查询每个叶子字段，否则返回的数组会丢失记录之间的对应关系。应直接用一个 JSONPath 命中完整记录集合或需要的字段集合：

```json
{
  "handle_id": "hdl_9a8f12",
  "json_path": "$.current_data[*].values[0]"
}
```

Harness 只执行 `json_path`，直接返回命中的 JSON 值，不做字段投影、字段别名、结构重建或聚合。inspect 的返回是一次新的查询结果，不保证与原结果保持相同结构；命中多个值时返回数组并保持查询顺序。后续查询路径仍必须依据该 handle 对应的原始业务工具结果编写，不能把上一次 inspect 返回值当成新的根数据。需要 `sum`、`count`、`group`、`avg` 等聚合或业务计算时，应调用提供该能力的业务 MCP 工具。

`json_path` 使用标准 JSONPath，由 Harness 侧的 `github.com/ohler55/ojg/jp` 解析：

- 根节点：`$`
- 对象字段：`$.supplier_info.name`
- 数组下标：`$.current_data[0]`
- 全部数组元素：`$.current_data[*].product_id`
- 数字过滤：`$.current_data[?(@.product_id == 123)]`
- 字符串过滤：`$.products[?(@.product_id == '123')].title`
- 特殊字段名：`$.data['field.name']`

过滤条件中的字符串必须加引号，数字和布尔值不要加引号；路径命中多个值时会返回数组。每次查询都必须针对原始 handle 的原始 JSON 结构，不能把上一次 inspect 返回值当成新的根数据，也不能根据返回值形状改写后续路径。

----------------------------------------
✅ 落地效果与优势：
- 绝对安全：MCP 侧即使返回 10MB 垃圾数据，也绝不会把上下文撑爆。
- 架构解耦：不依赖 MCP Schema，底层 API 新增任意复杂字段完全不受影响。
- 精准下钻：保留了 Key 架构作为“地图”，LLM Reasoning 依旧能够精准获取需要的数据。
