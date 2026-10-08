package evidence

import (
	"github.com/HolySSSSShit/go-agent/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestCompileUsesOnlyExternalContract(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "source_a", Field: "field_a", MetricID: "metric_a", Unit: "unit", Label: "label"}})
	facts := compiler.Compile("step", core.ResultSource{ToolName: "source_a", Reference: "ref"}, map[string]any{"field_a": "8", "field_b": 13})
	if len(facts) != 1 || facts[0].MetricID != "metric_a" || facts[0].SourcePath != "$.field_a" || facts[0].ID != "ref:$.field_a" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestCompileEvidenceIDDoesNotDependOnModelStepID(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "source_a", Field: "field_a", MetricID: "metric_a"}})
	source := core.ResultSource{ToolName: "source_a", Reference: "ref"}
	left := compiler.Compile("step-1", source, map[string]any{"field_a": 8})
	right := compiler.Compile("step-2", source, map[string]any{"field_a": 8})
	if len(left) != 1 || len(right) != 1 || left[0].ID != right[0].ID {
		t.Fatalf("evidence ID changed with model step ID: left=%#v right=%#v", left, right)
	}
	if left[0].ID != "ref:$.field_a" {
		t.Fatalf("unexpected stable evidence ID: %q", left[0].ID)
	}
}

func TestCompileInternalCreatesStableFactsWithoutContract(t *testing.T) {
	compiler := New(nil)
	facts := compiler.CompileInternal("step", core.ResultSource{ToolName: "calc", Reference: "internal:calc"}, map[string]any{"metrics": map[string]any{"change_rate": "12.5"}})
	if len(facts) != 1 || facts[0].ID != "internal:calc:$.metrics.change_rate" || facts[0].Value != 12.5 {
		t.Fatalf("internal facts = %#v", facts)
	}
}

func TestNormalizeIDsRepairsPersistedEvidence(t *testing.T) {
	facts := NormalizeIDs([]core.EvidenceFact{{ID: "old-step:$.field_a", StepID: "old-step", SourceRef: "ref", SourcePath: "$.field_a"}, {ID: "opaque"}})
	if facts[0].ID != "ref:$.field_a" || facts[1].ID != "opaque" {
		t.Fatalf("unexpected normalized facts: %#v", facts)
	}
}

func TestNormalizeIDsDeduplicatesSameSourcePath(t *testing.T) {
	facts := NormalizeIDs([]core.EvidenceFact{
		{ID: "old-1", Value: 1, SourceRef: "handle", SourcePath: "$.rows[0].value", Label: "旧"},
		{ID: "old-2", Value: 2, SourceRef: "handle", SourcePath: "$.rows[0].value", Label: "新"},
		{ID: "other", Value: 3, SourceRef: "handle", SourcePath: "$.rows[1].value"},
	})
	if len(facts) != 2 {
		t.Fatalf("同一句柄和路径应只保留一条事实: %#v", facts)
	}
	if facts[0].Value != 2 || facts[0].Label != "新" || facts[0].ID != "handle:$.rows[0].value" {
		t.Fatalf("重复事实应保留最新内容并使用稳定 ID: %#v", facts)
	}
}

func TestCompileKeepsContractedScalarText(t *testing.T) {
	compiler := New([]Contract{
		{SourceTool: "source_a", Field: "title", MetricID: "product_title", Label: "商品标题"},
		{SourceTool: "source_a", Field: "image", MetricID: "product_image", Label: "商品主图"},
		{SourceTool: "source_a", Field: "url", MetricID: "product_url", Label: "商品链接"},
	})
	facts := compiler.Compile("step", core.ResultSource{ToolName: "source_a", Reference: "ref"}, map[string]any{
		"title": "测试商品", "image": "https://example.invalid/image.jpg", "url": "/products/test",
	})
	if len(facts) != 3 {
		t.Fatalf("facts = %#v", facts)
	}
	values := map[string]any{}
	for _, fact := range facts {
		values[fact.MetricID] = fact.Value
	}
	if values["product_title"] != "测试商品" || values["product_image"] != "https://example.invalid/image.jpg" || values["product_url"] != "/products/test" {
		t.Fatalf("contracted scalar values = %#v", values)
	}
}

func TestCompileKeepsLinksForProductArray(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "get_product", Field: "url", MetricID: "product_url", Label: "商品详情链接", Kind: "link", DisplayField: "title"}})
	facts := compiler.Compile("step", core.ResultSource{ToolName: "get_product", Reference: "ref"}, map[string]any{
		"products": []any{
			map[string]any{"title": "商品 A", "url": "/products/a"},
			map[string]any{"title": "商品 B", "url": "/products/b"},
		},
	})
	if len(facts) != 2 || facts[0].Value != "/products/a" || facts[1].Value != "/products/b" || facts[0].PresentationTitle != "商品 A" || facts[1].PresentationTitle != "商品 B" {
		t.Fatalf("array product links = %#v", facts)
	}
}

func TestCompileRejectsUncontractedSource(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "source_a", Field: "field_a", MetricID: "metric_a"}})
	if facts := compiler.Compile("step", core.ResultSource{ToolName: "source_b"}, map[string]any{"field_a": 8}); len(facts) != 0 {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestCompileInspectWildcardKeepsOriginalSourcePath(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "source_a", Field: "metric", MetricID: "metric", Unit: "人", Label: "指标"}})
	data := []any{
		map[string]any{"date": "2026-07-21", "values": []any{map[string]any{"metric": 1}}},
		map[string]any{"date": "2026-07-22", "values": []any{map[string]any{"metric": 2}}},
	}
	facts := compiler.Compile("step", core.ResultSource{ToolName: "source_a", Reference: "ref", Path: "$.current_data[*]"}, data)
	if len(facts) != 2 || facts[0].SourcePath != "$.current_data[0].values[0].metric" || facts[1].SourcePath != "$.current_data[1].values[0].metric" {
		t.Fatalf("inspect wildcard paths = %#v", facts)
	}
	if facts[0].Dimensions["date"] != "2026-07-21" || facts[1].Dimensions["date"] != "2026-07-22" {
		t.Fatalf("record dimensions were not preserved: %#v", facts)
	}
}

func TestCompileCarriesArbitraryRecordDimensions(t *testing.T) {
	compiler := New([]Contract{{SourceTool: "source_a", Field: "value", MetricID: "metric", Unit: "个", Label: "指标"}})
	data := []any{map[string]any{
		"period": "2026-08-01",
		"values": []any{map[string]any{
			"country": "US",
			"channel": "direct",
			"value":   "8",
		}},
	}}
	facts := compiler.Compile("step", core.ResultSource{ToolName: "source_a", Reference: "ref", Path: "$.rows[*]"}, data)
	if len(facts) != 1 {
		t.Fatalf("facts = %#v", facts)
	}
	if facts[0].Dimensions["period"] != "2026-08-01" || facts[0].Dimensions["country"] != "US" || facts[0].Dimensions["channel"] != "direct" {
		t.Fatalf("arbitrary dimensions were not carried: %#v", facts[0].Dimensions)
	}
}

func TestCompileForPacksUsesOnlySelectedPackContract(t *testing.T) {
	compiler := New([]Contract{
		{Pack: "store", SourceTool: "source_a", Field: "value", MetricID: "store_value", Label: "店铺值"},
		{Pack: "marketing", SourceTool: "source_a", Field: "value", MetricID: "marketing_value", Label: "营销值"},
	})
	facts := compiler.CompileForPacks("step", core.ResultSource{ToolName: "source_a", Reference: "ref"}, map[string]any{"value": 8}, []string{"store"})
	if len(facts) != 1 || facts[0].MetricID != "store_value" {
		t.Fatalf("selected pack contract = %#v", facts)
	}
	if facts := compiler.CompileForPacks("step", core.ResultSource{ToolName: "source_a", Reference: "ref"}, map[string]any{"value": 8}, []string{"unknown"}); len(facts) != 0 {
		t.Fatalf("unselected pack generated facts = %#v", facts)
	}
}

func TestLoadAssignsPackFromEvidencePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "packs", "store", "evidence")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "metrics.json"), []byte(`[{"source_tool":"source_a","field":"value","metric_id":"store_value"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	compiler, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	facts := compiler.CompileForPacks("step", core.ResultSource{ToolName: "source_a", Reference: "ref"}, map[string]any{"value": 8}, []string{"store"})
	if len(facts) != 1 || facts[0].MetricID != "store_value" {
		t.Fatalf("loaded pack contract = %#v", facts)
	}
}
