package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}
type Node struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Responsibility  string   `json:"responsibility,omitempty"`
	Resolver        string   `json:"resolver,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	UserIDs         []string `json:"user_ids,omitempty"`
	Checklist       []string `json:"checklist,omitempty"`
	CommentRequired bool     `json:"comment_required,omitempty"`
	TimeoutHours    int      `json:"timeout_hours,omitempty"`
}
type Edge struct {
	From      string     `json:"from"`
	To        string     `json:"to"`
	Priority  int        `json:"priority,omitempty"`
	Default   bool       `json:"default,omitempty"`
	Condition *Condition `json:"condition,omitempty"`
}
type Condition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}
type ValidationError struct {
	Code    string `json:"code"`
	NodeID  string `json:"node_id,omitempty"`
	Message string `json:"message"`
}

var nodeTypes = map[string]bool{"START": true, "APPROVAL": true, "CONDITION": true, "NOTIFY": true, "END_APPROVED": true}
var fields = map[string]bool{"direction": true, "security_level": true, "data_category": true, "total_size_bytes": true, "file_count": true, "business_system_id": true, "requester_group_id": true}
var operators = map[string]bool{"EQ": true, "NE": true, "GT": true, "GTE": true, "LT": true, "LTE": true, "IN": true}

func Parse(raw []byte) (Graph, error) {
	var g Graph
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&g); err != nil {
		return Graph{}, err
	}
	return g, nil
}
func Validate(g Graph) []ValidationError {
	out := []ValidationError{}
	if len(g.Nodes) > 100 || len(g.Edges) > 200 {
		out = append(out, ValidationError{Code: "GRAPH_LIMIT", Message: "节点或边超过上限"})
	}
	nodes := map[string]Node{}
	start, end := "", ""
	approvals := 0
	for _, n := range g.Nodes {
		if n.ID == "" || nodes[n.ID].ID != "" {
			out = append(out, ValidationError{Code: "DUPLICATE_NODE", NodeID: n.ID, Message: "节点 ID 为空或重复"})
			continue
		}
		nodes[n.ID] = n
		if !nodeTypes[n.Type] {
			out = append(out, ValidationError{Code: "INVALID_NODE_TYPE", NodeID: n.ID, Message: "不支持的节点类型"})
		}
		if n.Type == "START" {
			if start != "" {
				out = append(out, ValidationError{Code: "START_COUNT", NodeID: n.ID, Message: "只能有一个开始节点"})
			}
			start = n.ID
		}
		if n.Type == "END_APPROVED" {
			if end != "" {
				out = append(out, ValidationError{Code: "END_COUNT", NodeID: n.ID, Message: "只能有一个通过结束节点"})
			}
			end = n.ID
		}
		if n.Type == "APPROVAL" {
			approvals++
			if n.Mode != "SINGLE" && n.Mode != "ANY" && n.Mode != "ALL" {
				out = append(out, ValidationError{Code: "INVALID_MODE", NodeID: n.ID, Message: "审批模式必须为 SINGLE、ANY 或 ALL"})
			}
			if n.Resolver == "" {
				out = append(out, ValidationError{Code: "MISSING_RESOLVER", NodeID: n.ID, Message: "审批节点缺少办理人解析器"})
			}
		}
	}
	if start == "" || end == "" {
		out = append(out, ValidationError{Code: "TERMINAL_COUNT", Message: "必须有唯一开始和通过结束节点"})
	}
	if approvals > 20 {
		out = append(out, ValidationError{Code: "APPROVAL_LIMIT", Message: "审批节点超过 20"})
	}
	adj := map[string][]Edge{}
	indegree := map[string]int{}
	for _, e := range g.Edges {
		if nodes[e.From].ID == "" || nodes[e.To].ID == "" {
			out = append(out, ValidationError{Code: "DANGLING_EDGE", Message: "边引用不存在的节点"})
			continue
		}
		adj[e.From] = append(adj[e.From], e)
		indegree[e.To]++
	}
	for id, n := range nodes {
		edges := adj[id]
		if n.Type != "CONDITION" && n.Type != "END_APPROVED" && len(edges) != 1 {
			out = append(out, ValidationError{Code: "INVALID_OUT_DEGREE", NodeID: id, Message: "非条件节点必须有一条出边"})
		}
		if n.Type == "END_APPROVED" && len(edges) != 0 {
			out = append(out, ValidationError{Code: "END_HAS_EDGE", NodeID: id, Message: "结束节点不能有出边"})
		}
		if n.Type == "CONDITION" {
			defaults := 0
			for _, e := range edges {
				if e.Default {
					defaults++
				} else if e.Condition == nil || !fields[e.Condition.Field] || !operators[e.Condition.Operator] {
					out = append(out, ValidationError{Code: "INVALID_CONDITION", NodeID: id, Message: "条件字段或操作符不在白名单"})
				}
			}
			if defaults != 1 {
				out = append(out, ValidationError{Code: "DEFAULT_BRANCH", NodeID: id, Message: "条件节点必须有唯一默认分支"})
			}
		}
	}
	queue := []string{}
	degrees := map[string]int{}
	for id := range nodes {
		degrees[id] = indegree[id]
		if degrees[id] == 0 {
			queue = append(queue, id)
		}
	}
	seen := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		seen++
		for _, e := range adj[id] {
			degrees[e.To]--
			if degrees[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if seen != len(nodes) {
		out = append(out, ValidationError{Code: "CYCLE", Message: "流程图不能包含循环"})
	}
	reachable := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, e := range adj[id] {
			walk(e.To)
		}
	}
	if start != "" {
		walk(start)
	}
	for id := range nodes {
		if !reachable[id] {
			out = append(out, ValidationError{Code: "UNREACHABLE", NodeID: id, Message: "节点从开始节点不可达"})
		}
	}
	if start != "" && end != "" && seen == len(nodes) {
		var paths func(string, []string)
		paths = func(id string, responsibilities []string) {
			n := nodes[id]
			if n.Type == "APPROVAL" && n.Responsibility != "" {
				responsibilities = append(append([]string{}, responsibilities...), n.Responsibility)
			}
			if id == end {
				required := []string{"GROUP_MANAGER", "SECURITY_OFFICER", "DEPARTMENT_MANAGER"}
				pos := -1
				for _, want := range required {
					found := -1
					for i := pos + 1; i < len(responsibilities); i++ {
						if responsibilities[i] == want {
							found = i
							break
						}
					}
					if found < 0 {
						out = append(out, ValidationError{Code: "SECURITY_BASELINE", Message: fmt.Sprintf("成功路径缺少或乱序责任 %s", want)})
						return
					}
					pos = found
				}
				return
			}
			for _, e := range adj[id] {
				paths(e.To, responsibilities)
			}
		}
		paths(start, nil)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code+out[i].NodeID < out[j].Code+out[j].NodeID })
	return out
}
func SelectPath(g Graph, input map[string]any) ([]string, error) {
	nodes := map[string]Node{}
	adj := map[string][]Edge{}
	current := ""
	for _, n := range g.Nodes {
		nodes[n.ID] = n
		if n.Type == "START" {
			current = n.ID
		}
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e)
	}
	path := []string{}
	for steps := 0; steps <= len(g.Nodes); steps++ {
		if current == "" {
			return nil, fmt.Errorf("missing start")
		}
		path = append(path, current)
		if nodes[current].Type == "END_APPROVED" {
			return path, nil
		}
		edges := adj[current]
		if nodes[current].Type == "CONDITION" {
			sort.Slice(edges, func(i, j int) bool { return edges[i].Priority < edges[j].Priority })
			selected := ""
			fallback := ""
			for _, e := range edges {
				if e.Default {
					fallback = e.To
				} else if match(input[e.Condition.Field], e.Condition.Operator, e.Condition.Value) {
					selected = e.To
					break
				}
			}
			if selected == "" {
				selected = fallback
			}
			current = selected
		} else if len(edges) == 1 {
			current = edges[0].To
		} else {
			return nil, fmt.Errorf("invalid path at %s", current)
		}
	}
	return nil, fmt.Errorf("path exceeds graph")
}
func match(actual any, operator string, expected any) bool {
	a := fmt.Sprint(actual)
	e := fmt.Sprint(expected)
	switch operator {
	case "EQ":
		return a == e
	case "NE":
		return a != e
	case "IN":
		if values, ok := expected.([]any); ok {
			for _, v := range values {
				if a == fmt.Sprint(v) {
					return true
				}
			}
		}
		return false
	}
	return false
}
