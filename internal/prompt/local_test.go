package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
)

func TestGlobalTemplatesCoverRuntimePrompts(t *testing.T) {
	for _, id := range []string{core.PromptSystem, core.PromptExecutionRecovery, core.PromptSessionCompact} {
		template, err := (Manager{}).Get(context.Background(), id, "")
		if err != nil {
			t.Fatalf("load global prompt %q: %v", id, err)
		}
		if template.Render() == "" {
			t.Fatalf("global prompt %q is empty", id)
		}
		if id == core.PromptSessionCompact && template.Protocol.Name == "" {
			t.Fatalf("global prompt %q has no protocol", id)
		}
	}
}

func TestSystemPromptProhibitsUserVisibleReasoningAndCode(t *testing.T) {
	template, err := (Manager{}).Get(context.Background(), core.PromptSystem, "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := template.Render()
	for _, required := range []string{"不得向用户输出任何推理过程、代码", "工具调用细节"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("系统提示词缺少安全输出约束 %q：%s", required, rendered)
		}
	}
}

func TestSystemPromptRequiresEvidenceForFacts(t *testing.T) {
	template, err := (Manager{}).Get(context.Background(), core.PromptSystem, "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := template.Render()
	for _, required := range []string{"数据不足时说明限制", "不得依据字段名、历史结论或数据形状补齐事实"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("系统提示词缺少事实约束 %q：%s", required, rendered)
		}
	}
}

func TestSystemPromptDelegatesDomainInstructionsToSkills(t *testing.T) {
	template, err := (Manager{}).Get(context.Background(), core.PromptSystem, "")
	if err != nil {
		t.Fatal(err)
	}
	rendered := template.Render()
	if !strings.Contains(rendered, "已加载技能时优先遵循") {
		t.Fatalf("系统提示词没有声明技能优先规则：%s", rendered)
	}
	if strings.Contains(rendered, "get_sales_summary") {
		t.Fatalf("销售工具策略不应硬编码在系统提示词中：%s", rendered)
	}
}
