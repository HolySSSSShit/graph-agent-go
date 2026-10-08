package model

import (
	"context"
	"strings"
	"testing"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

func TestJSONOutputExtractorParsesToolPlanDependencies(t *testing.T) {
	extractor := JSONOutputExtractor{}
	output, err := extractor.Extract(context.Background(), "```json\n"+
		"{\"kind\":\"tool_plan\",\"tool_plan\":{\"steps\":["+
		"{\"id\":\"sales\",\"tool_name\":\"get_sales_overview\",\"arguments\":{},\"depends_on\":[]},"+
		"{\"id\":\"products\",\"tool_name\":\"get_product_performance\",\"arguments\":{},\"depends_on\":[]},"+
		"{\"id\":\"summary\",\"tool_name\":\"get_sales_overview\",\"arguments\":{},\"depends_on\":[\"sales\",\"products\"]}]}}\n"+
		"```", core.ResponseProtocol{Name: "agent.tool_plan"})
	if err != nil {
		t.Fatal(err)
	}
	if output.ToolPlan == nil || len(output.ToolPlan.Steps) != 3 || len(output.ToolPlan.Steps[2].DependsOn) != 2 {
		t.Fatalf("unexpected tool plan: %+v", output)
	}
}

func TestJSONOutputExtractorNormalizesKeyedToolPlan(t *testing.T) {
	extractor := JSONOutputExtractor{}
	output, err := extractor.Extract(context.Background(), `{"kind":"tool_plan","mode":"single_step","summary":"查询销售数据","tool_plan":{"1":{"tool_name":"get_sales_summary","parameters":{"query":{"start_at":"2026-08-01","end_at":"2026-08-07"}}}}}`, core.ResponseProtocol{Name: "agent.reasoning"})
	if err != nil {
		t.Fatal(err)
	}
	if output.ToolPlan == nil || len(output.ToolPlan.Steps) != 1 {
		t.Fatalf("unexpected normalized tool plan: %+v", output)
	}
	step := output.ToolPlan.Steps[0]
	if step.ID != "1" || step.ToolName != "get_sales_summary" || step.Arguments["query"] == nil {
		t.Fatalf("unexpected normalized step: %+v", step)
	}
}

func TestJSONOutputExtractorRejectsDependencyCycle(t *testing.T) {
	extractor := JSONOutputExtractor{}
	_, err := extractor.Extract(context.Background(), `{"kind":"tool_plan","tool_plan":{"steps":[{"id":"a","tool_name":"a","depends_on":["b"]},{"id":"b","tool_name":"b","depends_on":["a"]}]}}`, core.ResponseProtocol{})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected dependency cycle error, got %v", err)
	}
}

func TestJSONOutputExtractorParsesFinalResponse(t *testing.T) {
	extractor := JSONOutputExtractor{}
	output, err := extractor.Extract(context.Background(), `{"answer":"销售额上涨","facts":[{"Text":"GMV 为 100","Source":"get_sales_overview"}],"assumptions":[],"recommendations":[],"confidence":0.9,"needs_confirmation":false}`, core.ResponseProtocol{Name: "agent.final_response"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || output.Final.Answer != "销售额上涨" {
		t.Fatalf("unexpected final output: %+v", output)
	}
}

func TestJSONOutputExtractorMergesTopLevelFieldsIntoPartialNestedFinal(t *testing.T) {
	raw := `{"kind":"final","mode":"single_step","final":{"answer":"指标为 8。"},"evidence_ids":["fact"],"facts":[],"assumptions":[],"recommendations":[],"confidence":1,"needs_confirmation":false}`
	output, err := (JSONOutputExtractor{}).Extract(context.Background(), raw, core.ResponseProtocol{Name: "agent.reasoning"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || len(output.Final.EvidenceIDs) != 1 || output.Final.EvidenceIDs[0] != "fact" {
		t.Fatalf("嵌套 final 缺字段时应保留顶层 evidence_ids：%+v", output.Final)
	}
}

func TestJSONOutputExtractorIgnoresTrailingGeneratedText(t *testing.T) {
	protocol := core.ResponseProtocol{
		Name:   "agent.final_response",
		Schema: core.FinalResponse{},
	}
	raw := `{"answer":"## 核心判断\n\n结论","facts":[],"assumptions":[],"recommendations":[],"confidence":0.9,"needs_confirmation":false}.replace(/\\n/g, "\\n")`
	output, err := (JSONOutputExtractor{}).Extract(context.Background(), raw, protocol)
	if err != nil {
		t.Fatalf("trailing generated text should not invalidate JSON: %v", err)
	}
	if output.Final == nil || output.Final.Answer != "## 核心判断\n\n结论" {
		t.Fatalf("unexpected final response: %#v", output.Final)
	}
}

func TestJSONOutputExtractorRepairsDanglingJSONStringField(t *testing.T) {
	raw := "{\"answer\":\"报告已完成。\", \"   \\t\\t\\n    }"
	output, err := (JSONOutputExtractor{}).Extract(context.Background(), raw, core.ResponseProtocol{Name: "agent.final_response"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || output.Final.Answer != "报告已完成。" {
		t.Fatalf("unexpected repaired output: %#v", output.Final)
	}
}

func TestJSONOutputExtractorRepairsEscapedFinalFieldBoundary(t *testing.T) {
	raw := "{\"answer\":\"结论。\\\",\\\"facts\\\":[{\\\"text\\\":\\\"指标为 8\\\"}],\"confidence\":1} ```json {\"}"
	output, err := (JSONOutputExtractor{}).Extract(context.Background(), raw, core.ResponseProtocol{Name: "agent.final_response"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || output.Final.Answer != "结论。" {
		t.Fatalf("unexpected repaired output: %#v", output.Final)
	}
}

func TestJSONOutputExtractorRepairsControlCharsInsideFinalAnswer(t *testing.T) {
	raw := "{\"answer\":\"第一行\n第二行\",\"facts\":[],\"confidence\":1}"
	output, err := (JSONOutputExtractor{}).Extract(context.Background(), raw, core.ResponseProtocol{Name: "agent.final_response"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || output.Final.Answer != "第一行\n第二行" {
		t.Fatalf("unexpected repaired output: %#v", output.Final)
	}
}

func TestFirstJSONObjectRespectsBracesInsideStrings(t *testing.T) {
	raw := `prefix {"answer":"包含 { 大括号 }","facts":[]}`
	if got := firstJSONObject(raw); got != `{"answer":"包含 { 大括号 }","facts":[]}` {
		t.Fatalf("unexpected JSON extraction: %s", got)
	}
}

func TestJSONOutputExtractorPreservesFinalVisualizations(t *testing.T) {
	extractor := JSONOutputExtractor{}
	raw := "{\"answer\":\"trend\",\"confidence\":0.9,\"needs_confirmation\":false,\"visualizations\":[{\"id\":\"trend\",\"type\":\"line\",\"option\":{}}]}"
	output, err := extractor.Extract(context.Background(), raw, core.ResponseProtocol{Name: "agent.final_response"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Final == nil || len(output.Final.Visualizations) != 1 || output.Final.Visualizations[0].ID != "trend" {
		t.Fatalf("visualizations were not preserved: %+v", output.Final)
	}
}
