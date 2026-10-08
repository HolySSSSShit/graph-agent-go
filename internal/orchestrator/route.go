package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/HolySSSSShit/go-agent/internal/core"
)

// ConfiguredRouteResolver 根据图定义和强类型 Workflow State 解析下一跳。
// 节点不提供下一节点；条件路由由 Decide 函数选择，静态边由图定义提供。
type ConfiguredRouteResolver[T any] struct {
	Graph Graph[T]
}

func (r ConfiguredRouteResolver[T]) Resolve(_ context.Context, input RouteInput[T]) (core.RouteDecision, error) {
	graph := r.Graph
	requested := core.NodeType("")
	implicitApprovalRequest := graph.approval != "" && input.Current != graph.approval && input.Runtime != nil && input.Runtime.Control.PendingAction != nil
	if implicitApprovalRequest {
		requested = graph.approval
	} else if graph.approval != "" && input.Current == graph.approval && input.Runtime != nil && input.Runtime.Control.PendingAction != nil {
		requested = input.Runtime.Control.PendingAction.ResumeNode
	}
	limitTriggered := false
	if requested == "" && input.Runtime != nil {
		if limit, ok := graph.VisitLimit(input.Current); ok && input.Runtime.Metrics.NodeVisits[input.Current] >= limit.MaxVisits {
			requested = limit.Target
			input.Runtime.Metrics.BudgetExhausted = true
			limitTriggered = true
		}
	}
	if !implicitApprovalRequest && !limitTriggered {
		for _, route := range graph.routes {
			if requested != "" {
				break
			}
			if route.From != input.Current {
				continue
			}
			if route.Decide == nil {
				return core.RouteDecision{SourceNode: input.Current, AdmissionReason: "条件路由未配置决策函数", At: time.Now()}, fmt.Errorf("conditional route from %s has no decider", input.Current)
			}
			key, err := route.Decide(input)
			if err != nil {
				return core.RouteDecision{SourceNode: input.Current, AdmissionReason: "条件路由决策失败", At: time.Now()}, fmt.Errorf("route decision from %s: %w", input.Current, err)
			}
			var ok bool
			requested, ok = route.Targets[key]
			if !ok {
				return core.RouteDecision{SourceNode: input.Current, RequestedNext: core.NodeType(key), AdmissionReason: "条件路由未声明目标", At: time.Now()}, fmt.Errorf("[Edge is not defined] %s -> %s", input.Current, core.NodeType(key))
			}
			break
		}
	}
	if requested == "" {
		// 待确认动作可以由任意节点产生，统一先进入审批控制节点；审批节点
		// 自身则沿待确认动作记录的恢复目标继续，避免把目标写死成业务节点。
		if graph.approval != "" && input.Current != graph.approval && input.Runtime != nil && input.Runtime.Control.PendingAction != nil {
			requested = graph.approval
		} else {
			candidates := make([]core.NodeType, 0, 1)
			for _, edge := range graph.edges {
				if edge.From == input.Current {
					candidates = append(candidates, edge.To)
				}
			}
			if len(candidates) == 1 {
				requested = candidates[0]
			}
		}
	}
	decision := core.RouteDecision{SourceNode: input.Current, RequestedNext: requested, At: time.Now()}
	var edge *core.NodeEdge
	implicitApproval := false
	if requested == "" {
		decision.AdmissionReason = "节点没有返回路由目标"
		return decision, fmt.Errorf("node %s has no next route", input.Current)
	}
	if !graph.contains(requested) {
		decision.AdmissionReason = "目标节点未注册或未启用"
		return decision, fmt.Errorf("node route not found: %s", requested)
	}
	if len(graph.edges) > 0 {
		for index := range graph.edges {
			candidate := &graph.edges[index]
			if candidate.From == input.Current && candidate.To == requested {
				edge = candidate
				break
			}
		}
		if edge == nil {
			if !approvalTransitionAllowed(graph, input, requested) {
				decision.AdmissionReason = "目标节点不在当前节点的有向边中"
				return decision, fmt.Errorf("[Edge is not defined] %s -> %s", input.Current, requested)
			}
			implicitApproval = true
			decision.AdmissionReason = "待确认动作允许审批控制转移"
		} else if input.Runtime != nil {
			if !edge.Reentrant && input.Runtime.Metrics.NodeVisits[requested] > 0 {
				decision.AdmissionReason = "目标节点未声明允许重入"
				return decision, fmt.Errorf("route target is not reentrant: %s", requested)
			}
			for _, required := range edge.Requires {
				if input.Runtime.Metrics.NodeVisits[required] == 0 {
					decision.AdmissionReason = "前置节点尚未执行"
					return decision, fmt.Errorf("route prerequisite not met: %s", required)
				}
			}
		}
		if edge != nil {
			switch edge.Condition {
			case "", "always":
			case "not_budget_exhausted":
				if input.Runtime != nil && input.Runtime.Metrics.BudgetExhausted {
					decision.AdmissionReason = "运行预算已耗尽"
					return decision, fmt.Errorf("route budget exhausted")
				}
			case "max_visits":
				if edge.MaxVisits > 0 && input.Runtime != nil && input.Runtime.Metrics.NodeVisits[requested] >= edge.MaxVisits {
					decision.AdmissionReason = "目标节点访问次数达到上限"
					return decision, fmt.Errorf("route visit limit reached")
				}
			default:
				decision.AdmissionReason = "边条件未注册"
				return decision, fmt.Errorf("unknown edge condition: %s", edge.Condition)
			}
		}
	}
	decision.SelectedNext = requested
	decision.TransitionID = string(input.Current) + "->" + string(requested)
	decision.AdmissionPassed = true
	if !implicitApproval {
		if limitTriggered {
			decision.AdmissionReason = "节点访问次数达到 Workflow 图上限，转入图定义目标"
		} else {
			decision.AdmissionReason = "目标节点已注册并启用"
		}
	}
	if edge != nil && edge.ApprovalRequired {
		decision.RequiresApproval = true
		decision.Reason = "该状态转移需要用户确认"
	}
	return decision, nil
}

// approvalTransitionAllowed 只放行审批控制节点的隐式转移。审批节点是通用
// 控制节点，不要求业务图为每一种待确认动作预先声明边；但必须存在待确认
// 动作，且审批完成后的目标必须与动作声明的恢复节点一致。
func approvalTransitionAllowed[T any](graph Graph[T], input RouteInput[T], requested core.NodeType) bool {
	if graph.approval == "" || input.Runtime == nil || input.Runtime.Control.PendingAction == nil {
		return false
	}
	if requested == graph.approval && input.Current != graph.approval {
		return true
	}
	return input.Current == graph.approval && requested == input.Runtime.Control.PendingAction.ResumeNode
}
