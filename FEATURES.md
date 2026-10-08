# Go Agent 功能边界

## 核心模块

- 强类型 Workflow Definition、StateCodec、NodeRegistry、Graph 启动校验。
- 统一 route resolver、边准入、步骤预算、重入限制和异常降级。
- Runner 的 checkpoint、恢复、租约、取消、审批、审计和 SSE。
- HTTP 健康检查、会话管理、运行事件及审批控制接口。
- 内存/PostgreSQL 存储、数据库迁移、文件/PostgreSQL trace。
- trace 诊断字段可保存模型提示词、输出和工具参数，接入方负责脱敏和访问控制。
- OpenAI 兼容和 Gemini 模型适配、模型目录、通用 mock。
- 图片生成、fallback、通用存储接口和本地存储适配器。
- 上下文窗口、结果压缩、会话摘要、提示词加载。
- MCP HTTP source、shadow 结果、工具目录、Harness。
- 通用证据编译和 Markdown 渲染。

## 默认示例

`cmd/agent` 装配会话存储、Runner 和 `order_lookup`，默认监听本机
`127.0.0.1:8081`。示例可验证 Graph、checkpoint 和 SSE，返回固定结果，
不查询真实订单，也不调用模型或工具。技能/工具白名单仅为声明示例。

模型、MCP、压缩器、审批服务和 trace sink 是可选模块，需要接入方显式装配。
没有配置公开 Workflow 的 Handler 返回 HTTP 503。默认身份为本地开发身份。

## 开发模板范围

本项目是 MIT 许可的开发模板，使用 `internal/` 包结构，暂不提供公共 SDK
或稳定的跨版本 API 承诺。业务权限、生产身份和数据权限由接入方负责。

仓库包含通用测试夹具、示例 Workflow、公开配置模板、贡献/安全说明和
Windows/Linux CI。PostgreSQL 集成测试在 CI 的独立测试数据库中执行，
本地需要配置 `AGENT_TEST_POSTGRES_DSN`。
