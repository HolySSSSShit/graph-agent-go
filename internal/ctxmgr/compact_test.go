package ctxmgr

import (
	"context"
	"testing"

	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"github.com/HolySSSSShit/graph-agent-go/internal/prompt"
)

type compactModel struct {
	request core.ModelRequest
}

func (m *compactModel) Generate(_ context.Context, request core.ModelRequest) (core.ModelEvent, error) {
	m.request = request
	return core.ModelEvent{Type: "final", Text: `{"compact":"用户要求继续分析本周销售"}`}, nil
}

func (*compactModel) Stream(context.Context, core.ModelRequest) (<-chan core.ModelEvent, error) {
	return nil, nil
}

func TestModelCompactorBuildsJSONCompact(t *testing.T) {
	model := &compactModel{}
	compactor := ModelCompactor{Model: model, ModelName: "qwen:qwen-flash", Prompts: prompt.Manager{}, MaxTokens: 2048}
	compact, err := compactor.Compact(context.Background(), core.SessionCompact{Content: "此前确认按北京时间统计"}, []core.Message{{Role: "user", Content: "继续看销售"}})
	if err != nil {
		t.Fatal(err)
	}
	if compact.Content != "用户要求继续分析本周销售" {
		t.Fatalf("compact = %q", compact.Content)
	}
	if model.request.Model != "qwen:qwen-flash" || model.request.MaxTokens != 2048 || len(model.request.Messages) != 3 {
		t.Fatalf("unexpected compact request: %+v", model.request)
	}
	if model.request.Messages[1].Role != "user" {
		t.Fatalf("旧 compact 不得提升为 system 消息：%+v", model.request.Messages[1])
	}
}
