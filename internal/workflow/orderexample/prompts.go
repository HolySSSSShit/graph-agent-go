package orderexample

import (
	"path/filepath"

	"github.com/HolySSSSShit/go-agent/internal/core"
	"github.com/HolySSSSShit/go-agent/internal/prompt"
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
