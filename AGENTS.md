# 开发规范

本项目是 Graph Agent Go 开发模板，不提供真实业务 Workflow 或公共 SDK。

- Graph 是流程控制唯一来源：路由、预算、重入和降级在 Graph/resolver 中实现。
- 每个 Workflow 在自己的包中绑定强类型 State、StateCodec、Graph、NodeRegistry、
  技能、工具白名单和提示词模块。
- 节点只读 Runtime，更新自己拥有的 payload 字段，不返回下一节点名称或调用其他节点。
- Runner 负责执行、checkpoint、租约、取消、审计和 SSE，不读取业务字段。
- cmd/agent 只做基础设施依赖注入和调用 Workflow 公开装配入口。
- MCP source 由 mcpclient 构造注册；全局工具由 harness 注册。
- TOML 配置外部运行参数，业务 edge 在代码中构造并在启动时校验。
- 图使用 NewGraph、Start、From().To()/Route() 和 End 构造。
  route 返回 key，由 resolver 映射目标并验证准入。循环必须声明上限。
- 结构化模型输出严格遵守协议，原始非法输出不能直接展示给用户。
- 图片 URL 不进入 session/checkpoint；派生文本写入所属 Workflow payload。
- checkpoint 外壳由存储层处理，payload 的保存/恢复由 StateCodec 统一处理。
- 新增路由、状态、协议或节点职责需补测试；共享状态或并发变更运行 race 检查。
- 实现与 README、FEATURES、设计文档保持一致。
- 验证运行 go test ./...、go vet ./...，必要时 go test -race ./internal/...。
  Windows 本地代码变更后编译 bin/agent.exe。
- 注释使用中文，文件保持 UTF-8，统一术语 payload。
- 不提交真实凭证、会话数据、模型 trace、构建产物或本地运行配置。
