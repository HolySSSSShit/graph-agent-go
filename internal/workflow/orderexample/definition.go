package orderexample

import (
	"github.com/HolySSSSShit/go-agent/internal/core"
	"github.com/HolySSSSShit/go-agent/internal/orchestrator"
)

// Dependencies 是示例 Workflow 的外部依赖。真实业务可在这里加入模型、工具注册表等接口。
type Dependencies struct {
	Prompts core.PromptManager
}

const WorkflowID = "order_lookup"

// New 将 State、Codec、Graph、节点组、技能、工具和提示词绑定为一个 Definition。
func New(dependencies Dependencies) (orchestrator.Definition[OrderState], error) {
	graph := newOrderGraph()
	nodes, err := newOrderNodes()
	if err != nil {
		return orchestrator.Definition[OrderState]{}, err
	}
	definition := orchestrator.Definition[OrderState]{
		ID:            WorkflowID,
		WorkflowGraph: graph,
		NodeGroup:     nodes,
		Codec:         orderStateCodec(),
		SkillIDs:      orderSkillIDs(),
		ToolBindings:  orderToolBindings(),
		PromptManager: orderPromptManager(dependencies.Prompts),
	}
	if err := definition.Validate(); err != nil {
		return orchestrator.Definition[OrderState]{}, err
	}
	return definition, nil
}
