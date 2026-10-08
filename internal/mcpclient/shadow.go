package mcpclient

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/HolySSSSShit/graph-agent-go/internal/core"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ohler55/ojg/jp"
)

const (
	shadowTTL = 30 * time.Minute
	// inspect 返回的是模型主动查询的结果。16 KB 可容纳常见月度日趋势，
	// 同时仍限制长文本或大列表整体进入上下文。
	inspectMaxBytes = 16 * 1024
)

type shadowEntry struct {
	Data      any
	Source    core.ResultSource
	CreatedAt time.Time
}

type shadowStorage struct {
	mu      sync.Mutex
	entries map[string]shadowEntry
	ttl     time.Duration
	dir     string
	timer   *time.Timer
}

func newShadowStorage(directory ...string) *shadowStorage {
	dir := ""
	if len(directory) > 0 {
		dir = strings.TrimSpace(directory[0])
		if dir != "" {
			_ = os.MkdirAll(dir, 0o750)
		}
	}
	storage := &shadowStorage{entries: make(map[string]shadowEntry), ttl: shadowTTL, dir: dir}
	if dir != "" {
		storage.timer = time.AfterFunc(storage.ttl, storage.expire)
	}
	return storage
}

func (s *shadowStorage) put(data any, sources ...core.ResultSource) string {
	if s == nil {
		return ""
	}
	value := cloneJSON(data)
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		copy(bytes[:], []byte(fmt.Sprintf("%08x", time.Now().UnixNano())))
	}
	handle := "hdl_" + hex.EncodeToString(bytes[:])
	s.mu.Lock()
	s.cleanupLocked(time.Now())
	var source core.ResultSource
	if len(sources) > 0 {
		source = sources[0]
	}
	if source.Reference == "" {
		source.Reference = handle
	}
	entry := shadowEntry{Data: value, Source: source, CreatedAt: time.Now()}
	s.entries[handle] = entry
	if s.dir != "" {
		_ = s.writeFileLocked(handle, entry)
	}
	s.scheduleCleanupLocked(time.Now())
	s.mu.Unlock()
	return handle
}

func (s *shadowStorage) scheduleCleanupLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	delay := s.ttl
	for _, entry := range s.entries {
		remaining := entry.CreatedAt.Add(s.ttl).Sub(now)
		if remaining < delay {
			delay = remaining
		}
	}
	if delay < 0 {
		delay = 0
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(delay, s.expire)
		return
	}
	s.timer.Reset(delay)
}

func (s *shadowStorage) expire() {
	s.mu.Lock()
	s.timer = nil
	s.cleanupLocked(time.Now())
	if len(s.entries) > 0 {
		s.scheduleCleanupLocked(time.Now())
	}
	s.mu.Unlock()
}

func (s *shadowStorage) get(handle string) (any, core.ResultSource, bool) {
	if s == nil || !strings.HasPrefix(handle, "hdl_") {
		return nil, core.ResultSource{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cleanupLocked(now)
	entry, ok := s.entries[handle]
	if !ok {
		if s.dir == "" {
			return nil, core.ResultSource{}, false
		}
		loaded, loadErr := s.readFileLocked(handle)
		if loadErr != nil {
			return nil, core.ResultSource{}, false
		}
		if now.Sub(loaded.CreatedAt) >= s.ttl {
			_ = os.Remove(s.filePath(handle))
			return nil, core.ResultSource{}, false
		}
		entry = loaded
		s.entries[handle] = entry
		s.scheduleCleanupLocked(now)
	}
	return cloneJSON(entry.Data), entry.Source, true
}

func (s *shadowStorage) cleanupLocked(now time.Time) {
	for handle, entry := range s.entries {
		if now.Sub(entry.CreatedAt) >= s.ttl {
			delete(s.entries, handle)
			if s.dir != "" {
				_ = os.Remove(s.filePath(handle))
			}
		}
	}
	if s.dir != "" {
		entries, err := os.ReadDir(s.dir)
		if err == nil {
			for _, item := range entries {
				if item.IsDir() || !strings.HasPrefix(item.Name(), "hdl_") || !strings.HasSuffix(item.Name(), ".json") {
					continue
				}
				info, err := item.Info()
				if err == nil && now.Sub(info.ModTime()) >= s.ttl {
					_ = os.Remove(filepath.Join(s.dir, item.Name()))
				}
			}
		}
	}
}

func (s *shadowStorage) filePath(handle string) string { return filepath.Join(s.dir, handle+".json") }

func (s *shadowStorage) writeFileLocked(handle string, entry shadowEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+handle+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {

		}
	}(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.filePath(handle))
}

func (s *shadowStorage) readFileLocked(handle string) (shadowEntry, error) {
	data, err := os.ReadFile(s.filePath(handle))
	if err != nil {
		return shadowEntry{}, err
	}
	var entry shadowEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return shadowEntry{}, err
	}
	if entry.CreatedAt.IsZero() || entry.Data == nil {
		return shadowEntry{}, fmt.Errorf("invalid shadow entry")
	}
	return entry, nil
}

func cloneJSON(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var clone any
	if json.Unmarshal(data, &clone) != nil {
		return value
	}
	return clone
}

func skeleton(value any, depth int) any {
	switch current := value.(type) {
	case map[string]any:
		if depth < 0 {
			keys := make([]string, 0, len(current))
			for key := range current {
				keys = append(keys, key)
			}
			return fmt.Sprintf("[Object with keys: %s]", strings.Join(keys, ", "))
		}
		result := make(map[string]any, len(current))
		for key, child := range current {
			result[key] = skeleton(child, depth-1)
		}
		return result
	case []any:
		result := make([]any, 0, 3)
		for index, child := range current {
			if index >= 2 {
				break
			}
			result = append(result, skeleton(child, depth-1))
		}
		return result
	case string:
		return truncateText(current, 50)
	default:
		return current
	}
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func preview(handle string, status string, data any) map[string]any {
	hint := "这是本轮最新一次查询结果，应优先使用；当前内容是 preview 概览，不是完整数据。若当前问题需要明细，必须先用 inspect_handle_data 查询该句柄的完整原始记录集合（例如 $.current_data[*]），保持对象与指标的对应关系，不要只取孤立字段。"
	switch status {
	case core.ToolStatusBusinessError:
		hint = "这是业务错误结果；不要调用 inspect_handle_data，依据 error 和当前结果直接说明查询未成功。"
	case core.ToolStatusPermissionDenied:
		hint = "这是权限错误结果；不要调用 inspect_handle_data，依据 error 说明当前无法访问。"
	case core.ToolStatusFailed:
		hint = "这是工具失败结果；不要重复调用 inspect_handle_data，依据 error 说明失败原因。"
	}
	result := map[string]any{
		"handle_id": handle,
		"status":    status,
		"preview":   skeleton(data, 2),
		"hint":      hint,
	}
	// 对小型且常见的响应，直接保留浅层标量字段；
	// 完整值仍可通过句柄读取。
	if object, ok := data.(map[string]any); ok {
		for key, value := range object {
			if _, reserved := result[key]; !reserved {
				if _, nested := value.(map[string]any); !nested {
					result[key] = skeleton(value, 1)
				}
			}
		}
	}
	return result
}

func inspectPath(value any, path string) (any, bool) {
	selected, ok, _ := inspectPathChecked(value, path)
	return selected, ok
}

func inspectPathChecked(value any, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, fmt.Errorf("JSONPath 不能为空；请从原始结果根节点 $ 开始查询")
	}
	if strings.Contains(path, "{") || strings.Contains(path, "}") {
		return nil, false, fmt.Errorf("JSONPath 不支持对象投影 {…}；inspect 只能返回原始 JSON 值。若需保留归因对象与指标的对应关系，请查询完整记录集合，例如 $.current_data[*]")
	}
	expression, err := jp.ParseString(path)
	if err != nil {
		return nil, false, fmt.Errorf("JSONPath 语法不受 ojg/jp 支持：%q。只能使用路径、下标、通配符和过滤表达式；不要使用字段投影、别名、联合字段或脚本表达式", path)
	}
	results := expression.Get(value)
	if len(results) == 0 {
		return nil, false, nil
	}
	if len(results) == 1 {
		return results[0], true, nil
	}
	return results, true, nil
}

func inspectValue(value any) any {
	return inspectValueWithLimit(value, inspectMaxBytes)
}

func inspectValueWithLimit(value any, limit int) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	if len(data) <= limit {
		// 保持工具观察结果为结构化值。ResultReduce 会在交给模型前统一编码，
		// 避免额外一层带引号的 JSON 文本。
		return value
	}
	// 保持预览为 JSON 结构。把嵌套对象改成说明字符串后，模型会把合法的
	// 记录数组误判为异常数据，进而重复执行没有意义的 inspect。
	return map[string]any{
		"truncated":   true,
		"preview":     structuredInspectPreview(value),
		"total_bytes": len(data),
	}
}

// structuredInspectPreview 在限制展示数据量的同时保留查询值的类型和字段关系。
// 外层 truncated 字段就是截断标记，不向源记录本身添加虚构字段。
func structuredInspectPreview(value any) any {
	return structuredInspectPreviewValue(value)
}

func structuredInspectPreviewValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 24 {
			keys = keys[:24]
		}
		result := make(map[string]any, len(keys))
		for _, key := range keys {
			result[key] = structuredInspectPreviewValue(current[key])
		}
		return result
	case []any:
		count := len(current)
		if count > 3 {
			count = 3
		}
		result := make([]any, 0, count)
		for index := 0; index < count; index++ {
			result = append(result, structuredInspectPreviewValue(current[index]))
		}
		return result
	case string:
		return truncateText(current, 120)
	default:
		return value
	}
}
