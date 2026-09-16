package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"yundu/internal/idempotency"
	"yundu/internal/workflow"
)

type workflowCreateInput struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Graph       workflow.Graph `json:"graph"`
}
type workflowPublishInput struct {
	ChangeNote string `json:"change_note"`
}
type workflowBindInput struct {
	Direction         string `json:"direction"`
	ScopeType         string `json:"scope_type"`
	ScopeID           string `json:"scope_id"`
	WorkflowVersionID string `json:"workflow_version_id"`
}
type simulateInput struct {
	Input map[string]any `json:"input"`
}
type decisionInput struct {
	Decision        string   `json:"decision"`
	Comment         string   `json:"comment"`
	ExpectedVersion uint64   `json:"expected_version"`
	Checklist       []string `json:"checklist"`
}
type adminDecisionInput struct {
	Decision        string `json:"decision"`
	Comment         string `json:"comment"`
	Reason          string `json:"reason"`
	ExpectedVersion uint64 `json:"expected_version"`
}
type workflowUpdateInput struct {
	Graph           workflow.Graph `json:"graph"`
	Layout          any            `json:"layout"`
	ExpectedVersion uint64         `json:"expected_version"`
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "workflow.manage"); !ok {
		return
	}
	items, err := s.workflow.List(r.Context())
	if err != nil {
		problem(w, 500, "WORKFLOW_ERROR", "无法读取流程")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "workflow.manage")
	if !ok {
		return
	}
	var in workflowCreateInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, err := s.workflow.Create(r.Context(), in.Code, in.Name, in.Description, in.Graph, actor)
	if err != nil {
		problem(w, 422, "INVALID_WORKFLOW", "流程定义无效或编码重复")
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) createWorkflowRevision(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "workflow.manage")
	if !ok {
		return
	}
	id, err := s.workflow.CreateRevision(r.Context(), r.PathValue("workflow_id"), actor)
	if err != nil {
		problem(w, 409, "WORKFLOW_REVISION_CONFLICT", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"version_id": id, "version": 1})
}
func (s *Server) getWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "workflow.manage"); !ok {
		return
	}
	item, err := s.workflow.GetVersion(r.Context(), r.PathValue("version_id"))
	if err != nil {
		problem(w, 404, "WORKFLOW_VERSION_NOT_FOUND", "流程版本不存在")
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) listWorkflowBindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "workflow.manage"); !ok {
		return
	}
	items, err := s.workflow.ListBindings(r.Context())
	if err != nil {
		problem(w, 500, "WORKFLOW_BINDINGS_ERROR", "无法读取流程绑定")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) updateWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "workflow.manage"); !ok {
		return
	}
	var in workflowUpdateInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.workflow.UpdateDraft(r.Context(), r.PathValue("version_id"), in.Graph, in.Layout, in.ExpectedVersion); err != nil {
		problem(w, 409, "WORKFLOW_DRAFT_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) validateWorkflow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "workflow.manage")
	if !ok {
		return
	}
	validation, err := s.workflow.ValidateVersion(r.Context(), r.PathValue("version_id"), actor)
	if err != nil {
		problem(w, 422, "WORKFLOW_VALIDATION_FAILED", "无法校验流程版本")
		return
	}
	writeJSON(w, 200, map[string]any{"passed": len(validation) == 0, "errors": validation})
}
func (s *Server) simulateWorkflow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "workflow.manage"); !ok {
		return
	}
	var in simulateInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	path, err := s.workflow.Simulate(r.Context(), r.PathValue("version_id"), in.Input)
	if err != nil {
		problem(w, 422, "WORKFLOW_SIMULATION_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"selected_path": path, "side_effects": false})
}
func (s *Server) publishWorkflow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "workflow.publish")
	if !ok {
		return
	}
	var in workflowPublishInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.workflow.Publish(r.Context(), r.PathValue("version_id"), actor, in.ChangeNote); err != nil {
		problem(w, 409, "WORKFLOW_PUBLISH_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) bindWorkflow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "workflow.publish")
	if !ok {
		return
	}
	var in workflowBindInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.workflow.Bind(r.Context(), in.Direction, in.ScopeType, in.ScopeID, in.WorkflowVersionID, actor); err != nil {
		problem(w, 409, "WORKFLOW_BINDING_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listApprovalTasks(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	items, err := s.workflow.ListTasks(r.Context(), user.ID)
	if err != nil {
		problem(w, 500, "TASK_LIST_FAILED", "无法读取审批任务")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) requestProgress(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.read.own")
	if !ok {
		return
	}
	value, err := s.workflow.RequestProgress(r.Context(), user, r.PathValue("request_id"))
	if err != nil {
		problem(w, 404, "REQUEST_NOT_FOUND", "申请不存在或无权查看")
		return
	}
	writeJSON(w, 200, value)
}
func (s *Server) decideTask(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "审批必须提供 Idempotency-Key")
		return
	}
	var in decisionInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	payload, _ := json.Marshal(in)
	route := "POST:/api/v1/approval-tasks/" + r.PathValue("task_id") + "/decision"
	claim, err := s.idempotency.Claim(r.Context(), user.ID, route, key, payload)
	if err != nil {
		if errors.Is(err, idempotency.ErrConflict) {
			problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键冲突")
		} else {
			problem(w, 400, "INVALID_IDEMPOTENCY_KEY", "幂等键无效")
		}
		return
	}
	if claim.Replay {
		w.WriteHeader(claim.StatusCode)
		return
	}
	if err = s.workflow.Decide(r.Context(), r.PathValue("task_id"), user.ID, in.Decision, in.Comment, key, in.ExpectedVersion, in.Checklist, false, "", user.MFAVerifiedAt); err != nil {
		s.idempotency.Release(r.Context(), user.ID, route, key)
		problem(w, 409, "TASK_DECISION_CONFLICT", err.Error())
		return
	}
	_ = s.idempotency.Complete(r.Context(), user.ID, route, key, 204, []byte(`{}`))
	w.WriteHeader(204)
}
func (s *Server) adminDecideTask(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	allowed, err := s.authz.Allowed(r.Context(), user.ID, "approval.admin_on_behalf", "", "")
	if err != nil || !allowed {
		problem(w, 403, "PERMISSION_DENIED", "没有管理员代办权限")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	var in adminDecisionInput
	if key == "" || s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err = s.workflow.Decide(r.Context(), r.PathValue("task_id"), user.ID, in.Decision, in.Comment, key, in.ExpectedVersion, nil, true, in.Reason, user.MFAVerifiedAt); err != nil {
		problem(w, 409, "ADMIN_DECISION_REJECTED", err.Error())
		return
	}
	w.WriteHeader(204)
}
