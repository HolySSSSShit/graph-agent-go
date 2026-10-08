package orderexample

import "github.com/HolySSSSShit/go-agent/internal/orchestrator"

func orderToolBindings() []orchestrator.ToolBinding {
	return []orchestrator.ToolBinding{{Source: "order-api", Tools: []string{"get_order"}}}
}
