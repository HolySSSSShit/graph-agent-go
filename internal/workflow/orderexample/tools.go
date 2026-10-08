package orderexample

import "github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"

func orderToolBindings() []orchestrator.ToolBinding {
	return []orchestrator.ToolBinding{{Source: "order-api", Tools: []string{"get_order"}}}
}
