# Go Agent 技术架构

## 模块边界

`internal/core` 定义通用协议和依赖接口。`internal/orchestrator` 是流程控制
的唯一 owner，包含 Graph、强类型 Definition、StateCodec、节点注册、路由与 Runner。

`internal/api` 提供 HTTP、会话管理、SSE、审批和取消。
`internal/session` 提供内存/PostgreSQL 存储及迁移。
`internal/trace` 提供审计 sink。checkpoint 存储通用 RunState 外壳，
Workflow payload 的编解码由所属 StateCodec 完成。

trace 投影保留模型诊断提示词、输出和工具参数。字段的 JSON 序列化不代表
脱敏，接入方按自己的安全策略在记录前处理敏感内容并控制持久化访问。

模型、上下文、提示词、MCP、工具注册、Harness、策略、证据编译和展示模块
通过 `core` 接口接入。核心包不导入具体 Workflow。

## 模板入口

`cmd/agent/main.go` 加载配置、装配会话存储、Runner 和平台管理 API，
调用 `orderexample.New` 注册教学示例。示例按输入选择 Graph 路由并返回
固定结果，不执行真实订单、工具或模型调用。

默认配置仅监听 `127.0.0.1:8081`。模型、MCP、压缩器、审批和审计 sink
由接入方显式注入。生产身份需要实现 `core.IdentityProvider`。

`internal/testkit` 为 API 和 Runner 提供独立于示例的通用测试图。

## Workflow 契约

每个 Workflow 独立绑定强类型 State、StateCodec、Graph、NodeRegistry、
技能、工具白名单和提示词。通过 NewGraph、Start、From().To()/Route()、
End 构造拓扑，入口和结束节点显式声明，循环由图声明准入上限。

节点只读 Runtime 并更新自己拥有的 State 字段，不返回下一节点，也不调用
其他节点。route decider 返回 route key，resolver 映射目标并验证准入。
跨节点清理、合并和持久化使用 Workflow lifecycle hooks。

Runner 负责执行、checkpoint、租约、取消、审计和 SSE，不读取业务字段。
业务权限由接入方 API 负责；Go 平台约束运行策略和流程权限。

## 持久化

数据库迁移按版本顺序执行。证据、消息、审批、checkpoint、租约和 trace
使用通用存储契约。恢复 checkpoint 时必须存在对应 Workflow 注册及兼容的
StateCodec。更换 Workflow 或 payload 结构时由接入方设计迁移。

## 发布形态

当前以开发模板发布，`internal/` 限制其他模块直接导入实现。接入方在
自己的仓库中扩展 Workflow；若未来提供 SDK，需要另行定义公共包和版本契约。
