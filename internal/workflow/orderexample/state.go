// Package orderexample 提供按业务组织 Workflow 的最小示例。
package orderexample

// OrderState 是订单查询 Workflow 自己拥有的业务状态。
type OrderState struct {
	Version     int    `json:"version"`
	Stage       string `json:"stage"`
	OrderID     string `json:"order_id,omitempty"`
	OrderStatus string `json:"order_status,omitempty"`
}
