package core

import (
	"encoding/json"
	"errors"
	"strings"
)

// MaxSessionEntityStateBytes 限制会话执行条件的大小。
// 该状态会作为模型上下文复用，截断值可能造成不安全行为。
const MaxSessionEntityStateBytes = 16 * 1024

var ErrSessionEntityStateTooLarge = errors.New("session entity state exceeds size limit")

// SessionEntityState 使用现有 session state 字段保存最近一次完整实体快照。
// 它不保存模型正文、工具返回或来源引用。
type SessionEntityState struct {
	Entities any `json:"entities"`
}

func DecodeSessionEntities(state string) (any, bool) {
	if strings.TrimSpace(state) == "" || state == "new" {
		return nil, false
	}
	var value SessionEntityState
	if err := json.Unmarshal([]byte(state), &value); err != nil || value.Entities == nil {
		return nil, false
	}
	return value.Entities, true
}

func EncodeSessionEntities(entities any) (string, error) {
	encoded, err := json.Marshal(SessionEntityState{Entities: entities})
	if err != nil {
		return "", err
	}
	if len(encoded) > MaxSessionEntityStateBytes {
		return "", ErrSessionEntityStateTooLarge
	}
	return string(encoded), nil
}

// MergeSessionEntities 应用局部实体更新，不让会话层依赖业务字段名。
// 对象值递归合并；输入中的标量和数组值替换旧值。
func MergeSessionEntities(previous, incoming any) any {
	previousMap, previousOK := previous.(map[string]any)
	incomingMap, incomingOK := incoming.(map[string]any)
	if !previousOK || !incomingOK {
		return incoming
	}
	merged := make(map[string]any, len(previousMap)+len(incomingMap))
	for key, value := range previousMap {
		merged[key] = value
	}
	for key, value := range incomingMap {
		if old, exists := merged[key]; exists {
			merged[key] = MergeSessionEntities(old, value)
		} else {
			merged[key] = value
		}
	}
	return merged
}
