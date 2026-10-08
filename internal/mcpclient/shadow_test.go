package mcpclient

import (
	"encoding/json"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectPathUsesRawArrayShape(t *testing.T) {
	value := map[string]any{"current_data": []any{map[string]any{"uv": float64(12)}}}
	selected, ok := inspectPath(value, "$.current_data[0].uv")
	if !ok || selected != float64(12) {
		t.Fatalf("expected array preview alias to resolve, got %#v (ok=%v)", selected, ok)
	}
}

func TestShadowStorageActivelyExpiresEntries(t *testing.T) {
	directory := t.TempDir()
	storage := newShadowStorage(directory)
	handle := storage.put(map[string]any{"value": 1})
	storage.mu.Lock()
	entry := storage.entries[handle]
	entry.CreatedAt = time.Now().Add(-2 * storage.ttl)
	storage.entries[handle] = entry
	storage.mu.Unlock()
	storage.expire()
	if _, _, ok := storage.get(handle); ok {
		t.Fatal("过期 shadow 条目仍可读取")
	}
	if _, err := os.Stat(filepath.Join(directory, handle+".json")); !os.IsNotExist(err) {
		t.Fatalf("过期 shadow 文件仍然存在：%v", err)
	}
	storage.mu.Lock()
	timer := storage.timer
	storage.mu.Unlock()
	if timer != nil {
		t.Fatal("无剩余条目时不应继续调度清理定时器")
	}
}

func TestShadowStorageRestoresEntriesFromDisk(t *testing.T) {
	directory := t.TempDir()
	first := newShadowStorage(directory)
	handle := first.put(map[string]any{"rows": []any{map[string]any{"value": 7}}}, core.ResultSource{ToolName: "source_a"})
	second := newShadowStorage(directory)
	value, source, ok := second.get(handle)
	if !ok || source.ToolName != "source_a" {
		t.Fatalf("expected disk-backed shadow entry, got value=%#v source=%+v ok=%v", value, source, ok)
	}
	if _, err := filepath.Abs(filepath.Join(directory, handle+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestInspectPathFiltersArrayObjects(t *testing.T) {
	value := map[string]any{"current_data": []any{
		map[string]any{"product_id": float64(122), "title": "other"},
		map[string]any{"product_id": float64(123), "title": "target"},
	}}
	selected, ok := inspectPath(value, "$.current_data[?(@.product_id==123)]")
	if !ok {
		t.Fatal("expected JSONPath filter to match")
	}
	row, ok := selected.(map[string]any)
	if !ok || row["title"] != "target" {
		t.Fatalf("expected filtered object, got %#v", selected)
	}
}

func TestInspectPathFiltersStringIDs(t *testing.T) {
	value := map[string]any{"products": []any{
		map[string]any{"product_id": "122"},
		map[string]any{"product_id": "123"},
	}}
	selected, ok := inspectPath(value, `$.products[?(@.product_id=="123")]`)
	if !ok {
		t.Fatal("expected string JSONPath filter to match")
	}
	row, ok := selected.(map[string]any)
	if !ok || row["product_id"] != "123" {
		t.Fatalf("expected filtered string ID object, got %#v", selected)
	}
}

func TestInspectPathRejectsObjectProjectionWithActionableHint(t *testing.T) {
	_, ok, err := inspectPathChecked(map[string]any{"current_data": []any{}}, "$.current_data[*].{product_id,visit_count}")
	if ok || err == nil {
		t.Fatalf("expected object projection to be rejected, got ok=%v err=%v", ok, err)
	}
	for _, expected := range []string{"不支持对象投影", "完整记录集合", "$.current_data[*]"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("projection error missing %q: %v", expected, err)
		}
	}
}

func TestInspectPathDistinguishesSyntaxErrorFromNoMatch(t *testing.T) {
	_, ok, err := inspectPathChecked(map[string]any{"current_data": []any{}}, "$.current_data[")
	if ok || err == nil || !strings.Contains(err.Error(), "ojg/jp") {
		t.Fatalf("expected parser error, got ok=%v err=%v", ok, err)
	}
	_, ok, err = inspectPathChecked(map[string]any{"current_data": []any{}}, "$.missing")
	if ok || err != nil {
		t.Fatalf("expected valid no-match, got ok=%v err=%v", ok, err)
	}
}

func TestSkeletonPreservesJSONArrayShapeAndTruncatesItems(t *testing.T) {
	value := map[string]any{"current_data": []any{
		map[string]any{"uv": float64(12)},
		map[string]any{"uv": float64(13)},
		map[string]any{"uv": float64(14)},
	}}
	previewValue, ok := skeleton(value, 2).(map[string]any)
	if !ok {
		t.Fatalf("expected object preview, got %#v", previewValue)
	}
	rows, ok := previewValue["current_data"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected first two array items in preview, got %#v", previewValue["current_data"])
	}
	if _, synthetic := previewValue["items"]; synthetic {
		t.Fatal("preview must not add synthetic array fields")
	}
}

func TestInspectValueKeepsSmallResultsStructured(t *testing.T) {
	value := map[string]any{"product_id": "123", "title": "target"}
	result := inspectValue(value)
	if _, ok := result.(map[string]any); !ok {
		t.Fatalf("expected structured JSON result, got %T", result)
	}
}

func TestInspectValueKeepsMonthlyTrendStructured(t *testing.T) {
	rows := make([]any, 0, 31)
	for day := 1; day <= 31; day++ {
		rows = append(rows, map[string]any{"date": 20260800 + day, "values": []any{map[string]any{
			"uv": day * 12, "description": strings.Repeat("x", 240),
		}}})
	}
	value := map[string]any{"current_data": rows}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) <= 8*1024 || len(encoded) > inspectMaxBytes {
		t.Fatalf("测试数据必须覆盖旧上限且低于新上限：bytes=%d err=%v", len(encoded), err)
	}
	result := inspectValue(value)
	object, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected structured result, got %T", result)
	}
	if _, truncated := object["truncated"]; truncated {
		t.Fatal("月度趋势大小不应被 inspect 的内部上限截断")
	}
}

func TestInspectValueLimitsLargeResultsWithSkeleton(t *testing.T) {
	value := map[string]any{"description": strings.Repeat("x", inspectMaxBytes+1)}
	result, ok := inspectValue(value).(map[string]any)
	if !ok || result["truncated"] != true || result["preview"] == nil {
		t.Fatalf("expected truncated structured result, got %#v", result)
	}
}

func TestInspectValueLargeResultPreservesNestedObjectShape(t *testing.T) {
	value := map[string]any{"current_data": []any{
		map[string]any{"date": 20260801, "values": []any{
			map[string]any{"sale_times": 3, "ec_data_id": "a", "description": strings.Repeat("x", 2200)},
		}},
	}}
	result, ok := inspectValueWithLimit(value, 100).(map[string]any)
	if !ok || result["truncated"] != true {
		t.Fatalf("expected truncated structured result, got %#v", result)
	}
	preview, ok := result["preview"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured preview object, got %#v", result["preview"])
	}
	rows, ok := preview["current_data"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected current_data array, got %#v", preview["current_data"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("expected current_data item object, got %#v", rows[0])
	}
	values, ok := row["values"].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("expected values array, got %#v", row["values"])
	}
	if _, ok := values[0].(map[string]any); !ok {
		t.Fatalf("expected values item object, got %#v", values[0])
	}
}

func TestPreviewBusinessErrorDoesNotSuggestInspect(t *testing.T) {
	value := preview("hdl_error", core.ToolStatusBusinessError, "商品不存在")
	hint, _ := value["hint"].(string)
	if strings.Contains(hint, "请调用 inspect_handle_data") || !strings.Contains(hint, "业务错误") {
		t.Fatalf("业务错误预览不应继续引导 inspect：%q", hint)
	}
}

func TestPreviewSuccessMarksLatestResultAndCompleteRecordGuidance(t *testing.T) {
	value := preview("hdl_success", core.ToolStatusSuccess, map[string]any{"current_data": []any{map[string]any{"date": 20260801}}})
	hint, _ := value["hint"].(string)
	for _, required := range []string{"本轮最新一次查询结果", "完整原始记录集合", "不要只取孤立字段"} {
		if !strings.Contains(hint, required) {
			t.Fatalf("成功结果提示缺少 %q：%q", required, hint)
		}
	}
}

func TestInspectPathReturnsMultipleValuesUnchanged(t *testing.T) {
	value := map[string]any{"current_data": []any{
		map[string]any{"date": 20260801, "values": []any{map[string]any{"uv": "7"}}},
		map[string]any{"date": 20260802, "values": []any{map[string]any{"uv": "6"}}},
	}}
	selected, ok := inspectPath(value, "$.current_data[*].values[0].uv")
	if !ok {
		t.Fatal("expected JSONPath to match")
	}
	values, ok := selected.([]any)
	if !ok || len(values) != 2 || values[0] != "7" || values[1] != "6" {
		t.Fatalf("expected native multi-value result, got %#v", selected)
	}
}

func TestInspectPathReturnsOriginalMatchingObjects(t *testing.T) {
	value := map[string]any{"current_data": []any{
		map[string]any{"product_id": float64(123), "values": []any{map[string]any{"uv": "7"}}},
		map[string]any{"product_id": float64(123), "values": []any{map[string]any{"uv": "6"}}},
	}}
	selected, ok := inspectPath(value, "$.current_data[?(@.product_id == 123)]")
	if !ok {
		t.Fatal("expected JSONPath filter to match")
	}
	rows, ok := selected.([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected both matching objects, got %#v", selected)
	}
	first, ok := rows[0].(map[string]any)
	if !ok || first["values"] == nil {
		t.Fatalf("expected original nested object, got %#v", rows[0])
	}
}
