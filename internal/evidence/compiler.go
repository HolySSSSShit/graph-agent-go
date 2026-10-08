package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// Contract 与技能一起由外部维护。应用代码只执行通用契约，不拥有业务指标名称。
type Contract struct {
	Pack         string `json:"-"`
	SourceTool   string `json:"source_tool"`
	Field        string `json:"field"`
	MetricID     string `json:"metric_id"`
	Unit         string `json:"unit"`
	Label        string `json:"label"`
	Chartable    *bool  `json:"chartable,omitempty"`
	Kind         string `json:"kind,omitempty"`
	DisplayField string `json:"display_field,omitempty"`
}

type Compiler struct {
	contracts []Contract
	files     map[string][]string
	loaded    map[string][]Contract
	mu        sync.Mutex
}

func Load(directory string) (*Compiler, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "packs", "*", "evidence", "*.json"))
	if err != nil {
		return nil, err
	}
	files := make(map[string][]string)
	for _, path := range paths {
		pack := filepath.Base(filepath.Dir(filepath.Dir(path)))
		files[pack] = append(files[pack], path)
	}
	return &Compiler{files: files, loaded: make(map[string][]Contract)}, nil
}

func New(contracts []Contract) *Compiler {
	return &Compiler{contracts: append([]Contract(nil), contracts...), loaded: make(map[string][]Contract)}
}

func (c *Compiler) Compile(stepID string, source core.ResultSource, data any) []core.EvidenceFact {
	if c == nil {
		return nil
	}
	return c.compile(stepID, source, data, nil)
}

// CompileInternal 将 Harness 内部计算结果中的标量编译为可追溯事实。
// 内部工具没有外部契约，路径和值本身就是其稳定结构；不会访问业务字段或 MCP。
func (c *Compiler) CompileInternal(stepID string, source core.ResultSource, data any) []core.EvidenceFact {
	if c == nil || source.Reference == "" {
		return nil
	}
	result := make([]core.EvidenceFact, 0)
	var walk func(any, string, map[string]any)
	walk = func(value any, path string, dimensions map[string]any) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				childPath := path + "." + key
				if scalar, ok := scalarEvidenceValue(child); ok {
					result = append(result, core.EvidenceFact{ID: StableID(source.Reference, childPath, stepID), MetricID: childPath, Label: key, Value: scalar, Dimensions: clone(dimensions), StepID: stepID, SourceTool: source.ToolName, SourceRef: source.Reference, SourcePath: childPath})
				}
				walk(child, childPath, dimensions)
			}
		case []any:
			for index, child := range current {
				walk(child, fmt.Sprintf("%s[%d]", path, index), dimensions)
			}
		}
	}
	walk(data, "$", nil)
	return result
}

// CompileForPacks 只使用当前已选技能包的证据契约，避免不同业务包的同名字段互相覆盖。
func (c *Compiler) CompileForPacks(stepID string, source core.ResultSource, data any, packs []string) []core.EvidenceFact {
	facts, _ := c.CompileForPacksWithError(stepID, source, data, packs)
	return facts
}

// CompileForPacksWithError 在选中技能包后按需加载对应的证据契约。
func (c *Compiler) CompileForPacksWithError(stepID string, source core.ResultSource, data any, packs []string) ([]core.EvidenceFact, error) {
	if c == nil || len(packs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(packs))
	for _, pack := range packs {
		if pack = strings.TrimSpace(pack); pack != "" {
			allowed[pack] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	contracts, err := c.contractsForPacks(allowed)
	if err != nil {
		return nil, err
	}
	return compileContracts(contracts, stepID, source, data), nil
}

func (c *Compiler) compile(stepID string, source core.ResultSource, data any, _ map[string]struct{}) []core.EvidenceFact {
	if c == nil {
		return nil
	}
	contracts, err := c.allContracts()
	if err != nil {
		return nil
	}
	return compileContracts(contracts, stepID, source, data)
}

func compileContracts(contracts []Contract, stepID string, source core.ResultSource, data any) []core.EvidenceFact {
	byField := map[string]Contract{}
	for _, contract := range contracts {
		if contract.SourceTool == source.ToolName && contract.Field != "" {
			byField[contract.Field] = contract
		}
	}
	if len(byField) == 0 {
		return nil
	}
	result := make([]core.EvidenceFact, 0)
	var walk func(any, string, map[string]any)
	walk = func(value any, path string, dimensions map[string]any) {
		switch current := value.(type) {
		case map[string]any:
			// 将包含记录中的标量上下文保留为通用维度（例如日期、商品标识或渠道）。
			// 编译器不了解业务字段名；排除契约中的指标键，只把记录坐标附加到事实。
			recordDimensions := clone(dimensions)
			for key, raw := range current {
				if _, isMetric := byField[key]; isMetric {
					continue
				}
				if isScalar(raw) {
					recordDimensions[key] = raw
				}
			}
			keys := make([]string, 0, len(current))
			for key := range current {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childPath := path + "." + key
				if contract, ok := byField[key]; ok {
					if evidenceValue, ok := scalarEvidenceValue(current[key]); ok {
						presentationTitle := ""
						if contract.DisplayField != "" {
							presentationTitle, _ = current[contract.DisplayField].(string)
						}
						result = append(result, core.EvidenceFact{ID: StableID(source.Reference, childPath, stepID), MetricID: contract.MetricID, Label: contract.Label, Value: evidenceValue, Unit: contract.Unit, Dimensions: clone(recordDimensions), StepID: stepID, SourceTool: source.ToolName, SourceRef: source.Reference, SourcePath: childPath, Chartable: contract.Chartable, Kind: contract.Kind, PresentationTitle: presentationTitle})
					}
				}
				walk(current[key], childPath, recordDimensions)
			}
		case []any:
			for index, child := range current {
				walk(child, fmt.Sprintf("%s[%d]", path, index), dimensions)
			}
		}
	}
	path := source.Path
	if path == "" {
		path = "$"
	}
	// inspect `$.collection[*]` 会把选中的集合返回为新的根数组。
	// 遍历投影时产生的数组索引属于原集合，因此追加索引前要移除末尾通配符，
	// 这样 SourcePath 才能基于 SourceRef 复核。
	walkPath := path
	if before, ok := strings.CutSuffix(walkPath, "[*]"); ok {
		walkPath = before
		if walkPath == "" {
			walkPath = "$"
		}
	}
	walk(data, walkPath, map[string]any{})
	return result
}

// StableID 返回由结果引用和原始路径决定的证据标识。
// stepID 仅是模型生成的调试信息，不能参与证据身份，否则跨轮恢复后会失效。
func StableID(sourceRef, sourcePath, fallback string) string {
	sourceRef = strings.TrimSpace(sourceRef)
	sourcePath = strings.TrimSpace(sourcePath)
	if sourceRef != "" && sourcePath != "" {
		return sourceRef + ":" + sourcePath
	}
	if fallback == "" {
		return sourcePath
	}
	return fallback + ":" + sourcePath
}

// NormalizeIDs 将持久化或跨节点传递的事实统一到稳定标识，并按来源句柄和 JSONPath 去重。
// 没有完整来源元数据的测试/临时事实保持原 ID，由上层按原协议处理。
func NormalizeIDs(facts []core.EvidenceFact) []core.EvidenceFact {
	if len(facts) == 0 {
		return facts
	}
	result := append([]core.EvidenceFact(nil), facts...)
	for index := range result {
		if result[index].SourceRef != "" && result[index].SourcePath != "" {
			result[index].ID = StableID(result[index].SourceRef, result[index].SourcePath, result[index].StepID)
		}
	}
	// 同一句柄的同一路径代表同一个原始值。保留最后一次出现的事实，
	// 这样重复 inspect 或跨轮恢复时，较新的展示元数据可以覆盖旧值。
	positions := make(map[string]int, len(result))
	deduplicated := make([]core.EvidenceFact, 0, len(result))
	for _, fact := range result {
		if fact.SourceRef == "" || fact.SourcePath == "" {
			deduplicated = append(deduplicated, fact)
			continue
		}
		key := fact.SourceRef + "\x00" + fact.SourcePath
		if index, exists := positions[key]; exists {
			deduplicated[index] = fact
			continue
		}
		positions[key] = len(deduplicated)
		deduplicated = append(deduplicated, fact)
	}
	return deduplicated
}

func (c *Compiler) contractsForPacks(packs map[string]struct{}) ([]Contract, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	contracts := make([]Contract, 0, len(c.contracts))
	for _, contract := range c.contracts {
		if contract.Pack == "" {
			contracts = append(contracts, contract)
			continue
		}
		if _, ok := packs[contract.Pack]; ok {
			contracts = append(contracts, contract)
		}
	}
	packNames := make([]string, 0, len(packs))
	for pack := range packs {
		packNames = append(packNames, pack)
	}
	sort.Strings(packNames)
	for _, pack := range packNames {
		values, err := c.loadPackLocked(pack)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, values...)
	}
	return contracts, nil
}

func (c *Compiler) allContracts() ([]Contract, error) {
	if c == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	contracts := append([]Contract(nil), c.contracts...)
	packNames := make([]string, 0, len(c.files))
	for pack := range c.files {
		packNames = append(packNames, pack)
	}
	sort.Strings(packNames)
	for _, pack := range packNames {
		values, err := c.loadPackLocked(pack)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, values...)
	}
	return contracts, nil
}

func (c *Compiler) loadPackLocked(pack string) ([]Contract, error) {
	values, ok := c.loaded[pack]
	if ok {
		return values, nil
	}
	values, err := readPackContracts(pack, c.files[pack])
	if err != nil {
		return nil, err
	}
	c.loaded[pack] = values
	return values, nil
}

func readPackContracts(pack string, paths []string) ([]Contract, error) {
	contracts := make([]Contract, 0)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var values []Contract
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("decode evidence contract %s: %w", path, err)
		}
		for index := range values {
			values[index].Pack = pack
		}
		contracts = append(contracts, values...)
	}
	return contracts, nil
}

func isScalar(value any) bool {
	switch value.(type) {
	case nil, map[string]any, []any:
		return false
	default:
		return true
	}
}

func Format(fact core.EvidenceFact) string {
	return fmt.Sprintf("%s：%v %s", fact.Label, fact.Value, fact.Unit)
}

func numeric(value any) (any, bool) {
	switch current := value.(type) {
	case int, int64, float64, float32:
		return current, true
	case string:
		value, err := strconv.ParseFloat(current, 64)
		return value, err == nil
	default:
		return nil, false
	}
}

// scalarEvidenceValue 保留契约声明的标量文本（标题、描述、图片地址、链接等），
// 同时把可解析的数字字符串转换为数字，避免输出层把数值当成文本处理。
func scalarEvidenceValue(value any) (any, bool) {
	if number, ok := numeric(value); ok {
		return number, true
	}
	switch current := value.(type) {
	case string, bool:
		return current, true
	default:
		return nil, false
	}
}

func clone(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
