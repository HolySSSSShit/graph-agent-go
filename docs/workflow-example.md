# Workflow 示例

`internal/workflow/orderexample` 是不调用外部服务的最小示例，
用于学习强类型 payload、图路由、节点注册和提示词模块绑定。

## 代码入口

- `state.go` 定义 OrderState。
- `codec.go` 使用 JSONStateCodec 创建版本为 1 的 payload。
- `graph.go` 显式声明入口、条件路由、End 和 fallback。
- `nodes.go` 注册 lookup、summary 和 clarify 节点。
- `definition.go` 将所有组件绑定为 Definition，并在 New 中校验。
- `skills.go`、`tools.go`、`prompts.go` 展示能力和提示词声明。
- `workflow_test.go` 验证装配、payload 编解码和 route。

lookup 从 Runtime.Input 读取输入并写入 OrderID，route decider 按 OrderID
是否为空选择 summary 或 clarify。两个分支都是显式 End。
summary 只返回“示例订单查询已完成”，不是实际查询结果。

## 新建自己的 Workflow

1. 在 `internal/workflow/<name>` 中定义强类型 State 和 StateCodec。
2. 定义实现 Node 接口的节点，划清字段 ownership。
3. 用 NewGraph、Start、From().To()/Route()、End 构造图。
4. 将 Codec、Graph、NodeRegistry 和能力声明绑定为 Definition。
5. 暴露 New(Dependencies) 装配函数，并调用 Validate。
6. 在进程入口调用 New，然后加入 WorkflowRegistry。

例如，在入口中注册现有示例：

```go
definition, err := orderexample.New(orderexample.Dependencies{
    Prompts: prompt.NewManager("prompts"),
})
if err != nil {
    return err
}
workflows, err := orchestrator.NewWorkflowRegistry(
    orderexample.WorkflowID,
    definition,
)
if err != nil {
    return err
}
runner := &orchestrator.Runner{
    Workflows: workflows,
    Sessions:  sessions,
    MaxSteps:  64,
}
```

这段代码位于自己的装配函数中，sessions 由入口注入。
现有 `cmd/agent/main.go` 还展示 checkpoint、租约、超时和 API 的装配。
HTTP Handler 的 PublicChatWorkflow 必须与公开 Workflow ID 一致。

模型或工具依赖通过 Dependencies 注入。MCP source 使用
`mcpclient.Register`，Harness 工具使用 `harness.Register`。
示例里的 order-api/get_order 和 order_lookup 技能 ID 仅为声明，
并没有对应真实服务或技能实现。

## 状态和恢复

Runtime 是只读通用视图。图片、证据、业务结果等属于 Workflow payload。
checkpoint 存储不理解业务字段，必须通过所属 StateCodec 保存/恢复。
跨节点清理和持久化逻辑放在 StateHooks 中。

新增路由或状态字段时补测试；涉及共享状态和并发时运行 race 检查。
