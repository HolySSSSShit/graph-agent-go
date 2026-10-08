package response

import (
	"context"
	"strings"
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func TestMarkdownRendererRendersVerifiedFactsAndRecommendations(t *testing.T) {
	content, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer:          "结论已经整理。",
		Facts:           []core.Fact{{Text: "指标：8 个", Source: "verified"}},
		Recommendations: []string{"建议继续核查影响因素。"},
		Confidence:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"指标：8 个", "建议继续核查影响因素。"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
	if content == "" {
		t.Fatal("rendered markdown must not be empty")
	}
	long, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer:          strings.Repeat("报告内容。", 30),
		Facts:           []core.Fact{{Text: "指标：8 个", Source: "verified"}},
		Recommendations: []string{"检查结账流程。"},
	})
	if err != nil || strings.Contains(long, "已确认") || strings.Contains(long, "建议") || strings.Count(long, "检查结账流程") != 0 {
		t.Fatalf("完整报告不应重复追加事实分区: %s", long)
	}
}

func TestMarkdownRendererRejectsInternalReferences(t *testing.T) {
	for _, response := range []core.FinalResponse{
		{Answer: "请调用 MCP。", Confidence: 1},
		{Answer: "结论。", Facts: []core.Fact{{Text: "source_ref: ref"}}, Confidence: 1},
		{Answer: "结论。", Recommendations: []string{"查看 JSONPath。"}, Confidence: 1},
	} {
		if _, err := (MarkdownRenderer{}).Render(context.Background(), response); err == nil {
			t.Fatalf("internal content must be rejected: %#v", response)
		}
	}
}

func TestMarkdownRendererCleansModelFormattingArtifacts(t *testing.T) {
	content, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer: "### 标题\ntext\n** diagnosis conclusion **：增长\n\n### Action Items",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "\ntext\n") || strings.Contains(content, "diagnosis conclusion") || strings.Contains(content, "Action Items") {
		t.Fatalf("model formatting artifacts remain: %q", content)
	}
	if !strings.Contains(content, "诊断结论") || !strings.Contains(content, "行动建议") {
		t.Fatalf("model labels were not normalized: %q", content)
	}
}

func TestMarkdownRendererDoesNotAppendVagueConfirmationFooter(t *testing.T) {
	content, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer: "结论已经明确。", Confidence: 1, NeedsConfirmation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "需要你确认或补充信息后才能继续") {
		t.Fatalf("renderer must not append a vague confirmation footer: %s", content)
	}
}

func TestMarkdownRendererDecodesEscapedLineBreaks(t *testing.T) {
	content, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer:     "## title\\n\\n| a | b |\\n|---|---|\\n| 1 | 2 |",
		Confidence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "## title\n\n| a | b |") || strings.Contains(content, `\\n`) {
		t.Fatalf("escaped Markdown was not normalized: %q", content)
	}
}

func TestMarkdownRendererSeparatesHeadingFromTable(t *testing.T) {
	content, err := (MarkdownRenderer{}).Render(context.Background(), core.FinalResponse{
		Answer:     "### 核心经营指标对比表| 指标 | 本期 | 对比期 |\n|---|---|---|\n| 销售额 | 10 | 8 |",
		Confidence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "### 核心经营指标对比表\n| 指标 | 本期 | 对比期 |") {
		t.Fatalf("标题和表头应使用独立行: %q", content)
	}
}
