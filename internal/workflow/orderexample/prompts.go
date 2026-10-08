package orderexample

import (
	"path/filepath"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/prompt"
)

func orderPromptManager(global core.PromptManager) core.PromptManager {
	if global == nil {
		global = prompt.Manager{}
	}
	return prompt.WorkflowManager{
		Global: global,
		Module: prompt.JSONModule{File: filepath.Join("workflows", "orderexample.json")},
	}
}
