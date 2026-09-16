package workflow

import "testing"

func validGraph() Graph {
	return Graph{Nodes: []Node{{ID: "start", Type: "START"}, {ID: "group", Type: "APPROVAL", Resolver: "GROUP_MANAGER", Responsibility: "GROUP_MANAGER", Mode: "SINGLE"}, {ID: "security", Type: "APPROVAL", Resolver: "SECURITY_OFFICER", Responsibility: "SECURITY_OFFICER", Mode: "ANY"}, {ID: "department", Type: "APPROVAL", Resolver: "DEPARTMENT_MANAGER", Responsibility: "DEPARTMENT_MANAGER", Mode: "ALL"}, {ID: "end", Type: "END_APPROVED"}}, Edges: []Edge{{From: "start", To: "group"}, {From: "group", To: "security"}, {From: "security", To: "department"}, {From: "department", To: "end"}}}
}
func TestValidateBaseline(t *testing.T) {
	if errors := Validate(validGraph()); len(errors) != 0 {
		t.Fatalf("%+v", errors)
	}
}
func TestRejectCycleAndBaselineBypass(t *testing.T) {
	g := validGraph()
	g.Edges[1].To = "group"
	errors := Validate(g)
	if len(errors) == 0 {
		t.Fatal("invalid graph passed")
	}
}
func TestConditionSelectsSingleBranch(t *testing.T) {
	g := validGraph()
	g.Nodes = append(g.Nodes, Node{ID: "condition", Type: "CONDITION"}, Node{ID: "extra", Type: "APPROVAL", Resolver: "ROLE_IN_SCOPE", Responsibility: "EXTRA", Mode: "SINGLE"})
	g.Edges = []Edge{{From: "start", To: "group"}, {From: "group", To: "security"}, {From: "security", To: "condition"}, {From: "condition", To: "extra", Priority: 1, Condition: &Condition{Field: "security_level", Operator: "EQ", Value: "HIGH"}}, {From: "condition", To: "department", Default: true}, {From: "extra", To: "department"}, {From: "department", To: "end"}}
	if errs := Validate(g); len(errs) != 0 {
		t.Fatalf("%+v", errs)
	}
	path, err := SelectPath(g, map[string]any{"security_level": "HIGH"})
	if err != nil || path[4] != "extra" {
		t.Fatalf("%v %v", path, err)
	}
}
