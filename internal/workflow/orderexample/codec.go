package orderexample

import "github.com/HolySSSSShit/graph-agent-go/internal/orchestrator"

func orderStateCodec() orchestrator.StateCodec[OrderState] {
	return orchestrator.JSONStateCodec[OrderState]{Factory: func() OrderState {
		return OrderState{Version: 1, Stage: "lookup"}
	}}
}
