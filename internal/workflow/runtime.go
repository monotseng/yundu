package workflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProgressNode struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}
type RequestProgress struct {
	RequestStatus  string         `json:"request_status"`
	WorkflowStatus string         `json:"workflow_status,omitempty"`
	CurrentNodeID  string         `json:"current_node_id,omitempty"`
	Nodes          []ProgressNode `json:"nodes"`
}

func (s *Service) RequestProgress(ctx context.Context, userHex, requestHex string) (RequestProgress, error) {
	user, err := decode(userHex)
	if err != nil {
		return RequestProgress{}, err
	}
	request, err := decode(requestHex)
	if err != nil {
		return RequestProgress{}, err
	}
	out := RequestProgress{Nodes: []ProgressNode{}}
	if err = s.db.QueryRowContext(ctx, `SELECT status FROM exchange_requests WHERE id=? AND requester_id=?`, request, user).Scan(&out.RequestStatus); err != nil {
		return out, err
	}
	var instanceID, graphRaw, pathRaw []byte
	err = s.db.QueryRowContext(ctx, `SELECT id,status,current_node_id,frozen_graph,selected_path FROM workflow_instances WHERE request_id=? ORDER BY created_at DESC LIMIT 1`, request).Scan(&instanceID, &out.WorkflowStatus, &out.CurrentNodeID, &graphRaw, &pathRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	graph, err := Parse(graphRaw)
	if err != nil {
		return out, err
	}
	var path []string
	if err = json.Unmarshal(pathRaw, &path); err != nil {
		return out, err
	}
	names := map[string]Node{}
	for _, node := range graph.Nodes {
		names[node.ID] = node
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,node_type,status,activated_at,due_at,completed_at FROM workflow_node_instances WHERE instance_id=?`, instanceID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	states := map[string]ProgressNode{}
	for rows.Next() {
		var node ProgressNode
		var activated, due, completed sql.NullTime
		if err = rows.Scan(&node.ID, &node.Type, &node.Status, &activated, &due, &completed); err != nil {
			return out, err
		}
		node.Name = names[node.ID].Name
		if activated.Valid {
			node.ActivatedAt = &activated.Time
		}
		if due.Valid {
			node.DueAt = &due.Time
		}
		if completed.Valid {
			node.CompletedAt = &completed.Time
		}
		states[node.ID] = node
	}
	for _, id := range path {
		if node, ok := states[id]; ok {
			out.Nodes = append(out.Nodes, node)
		}
	}
	return out, rows.Err()
}

func (s *Service) StartRequest(ctx context.Context, requestHex string) error {
	requestID, err := decode(requestHex)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var versionID, groupID, requesterID []byte
	var direction, classification, status string
	var total uint64
	var count int
	var business sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT workflow_version_id,group_id,requester_id,direction,classification,status,total_bytes,file_count,COALESCE(HEX(business_system_id),'') FROM exchange_requests WHERE id=? FOR UPDATE`, requestID).Scan(&versionID, &groupID, &requesterID, &direction, &classification, &status, &total, &count, &business); err != nil {
		return err
	}
	if status != "IN_REVIEW" {
		return nil
	}
	var raw, digest []byte
	if err = tx.QueryRowContext(ctx, "SELECT graph_json,graph_sha256 FROM workflow_versions WHERE id=?", versionID).Scan(&raw, &digest); err != nil {
		return err
	}
	graph, err := Parse(raw)
	if err != nil {
		return err
	}
	input := map[string]any{"direction": direction, "security_level": classification, "total_size_bytes": total, "file_count": count, "business_system_id": strings.ToLower(business.String), "requester_group_id": hex.EncodeToString(groupID)}
	path, err := SelectPath(graph, input)
	if err != nil {
		return err
	}
	instanceID, _ := id()
	pathJSON, _ := json.Marshal(path)
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_instances(id,request_id,workflow_version_id,frozen_graph,selected_path,status,current_node_id) VALUES(?,?,?,?,?,'RUNNING',?)", instanceID, requestID, versionID, raw, pathJSON, path[0]); err != nil {
		return err
	}
	nodes := map[string]Node{}
	for _, n := range graph.Nodes {
		nodes[n.ID] = n
	}
	for _, nodeID := range path {
		n := nodes[nodeID]
		nodeInstanceID, _ := id()
		mode := any(nil)
		if n.Type == "APPROVAL" {
			mode = n.Mode
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_node_instances(id,instance_id,node_id,node_type,status,assignee_mode,resolver_snapshot,candidate_snapshot) VALUES(?,?,?,?, 'WAITING',?,JSON_OBJECT('resolver',?),JSON_ARRAY())", nodeInstanceID, instanceID, n.ID, n.Type, mode, n.Resolver); err != nil {
			return err
		}
	}
	if err = s.advanceTx(ctx, tx, instanceID, requestID, requesterID, groupID, graph, path, 0); err != nil {
		_, _ = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='WORKFLOW_BLOCKED' WHERE id=?", requestID)
		return err
	}
	return tx.Commit()
}
func (s *Service) CancelRequest(ctx context.Context, requestHex string) error {
	requestID, err := decode(requestHex)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var instanceID []byte
	err = tx.QueryRowContext(ctx, "SELECT id FROM workflow_instances WHERE request_id=? AND status='RUNNING' FOR UPDATE", requestID).Scan(&instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_tasks SET status='CANCELLED' WHERE instance_id=? AND status IN ('WAITING','PENDING','PAUSED')", instanceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='SKIPPED',completed_at=UTC_TIMESTAMP(6) WHERE instance_id=? AND status IN ('WAITING','ACTIVE')", instanceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_instances SET status='CANCELLED',completed_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=?", instanceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) advanceTx(ctx context.Context, tx *sql.Tx, instanceID, requestID, requesterID, groupID []byte, g Graph, path []string, index int) error {
	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	for index < len(path) {
		n := nodes[path[index]]
		if n.Type == "START" || n.Type == "CONDITION" || n.Type == "NOTIFY" {
			_, err := tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='COMPLETED',activated_at=UTC_TIMESTAMP(6),completed_at=UTC_TIMESTAMP(6) WHERE instance_id=? AND node_id=?", instanceID, n.ID)
			if err != nil {
				return err
			}
			index++
			continue
		}
		if n.Type == "END_APPROVED" {
			_, err := tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='COMPLETED',activated_at=UTC_TIMESTAMP(6),completed_at=UTC_TIMESTAMP(6) WHERE instance_id=? AND node_id=?", instanceID, n.ID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE workflow_instances SET status='APPROVED',current_node_id=?,completed_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=?", n.ID, instanceID)
			if err != nil {
				return err
			}
			var fileCount int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_files WHERE request_id=? AND status='UPLOADED'", requestID).Scan(&fileCount); err != nil {
				return err
			}
			// Historical workflow fixtures can contain no files; submitted production
			// requests cannot reach this branch because submission enforces file_count.
			if fileCount == 0 {
				_, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='APPROVED',version=version+1 WHERE id=?", requestID)
				return err
			}
			var direction string
			var sourceStorage, targetStorage []byte
			var targetAntivirus bool
			if err = tx.QueryRowContext(ctx, "SELECT r.direction,c.source_storage_version_id,c.target_storage_version_id,c.antivirus_enabled FROM exchange_requests r JOIN exchange_channels c ON c.direction=r.direction AND c.status='PUBLISHED' WHERE r.id=?", requestID).Scan(&direction, &sourceStorage, &targetStorage, &targetAntivirus); err != nil {
				return errors.New("published exchange channel is required")
			}
			rows, queryErr := tx.QueryContext(ctx, "SELECT id,source_storage_revision_id,source_sha256,size_bytes FROM request_files WHERE request_id=? AND status='UPLOADED' ORDER BY id", requestID)
			if queryErr != nil {
				return queryErr
			}
			type transferFile struct {
				id, source, hash []byte
				size             uint64
			}
			files := []transferFile{}
			for rows.Next() {
				var f transferFile
				if queryErr = rows.Scan(&f.id, &f.source, &f.hash, &f.size); queryErr != nil {
					rows.Close()
					return queryErr
				}
				files = append(files, f)
			}
			rows.Close()
			for _, f := range files {
				if !bytes.Equal(f.source, sourceStorage) {
					return errors.New("request source storage does not match published exchange channel")
				}
				jobID, _ := id()
				candidate := "transfers/" + hex.EncodeToString(requestID) + "/" + hex.EncodeToString(f.id) + "/" + hex.EncodeToString(jobID)
				if _, err = tx.ExecContext(ctx, "INSERT INTO transfer_jobs(id,request_id,file_id,source_storage_version_id,target_storage_version_id,expected_sha256,expected_bytes,candidate_key,status,available_at) VALUES(?,?,?,?,?,?,?,?, 'PENDING',UTC_TIMESTAMP(6))", jobID, requestID, f.id, f.source, targetStorage, f.hash, f.size, candidate); err != nil {
					return err
				}
			}
			outboxID, _ := id()
			payload, _ := json.Marshal(map[string]any{"request_id": hex.EncodeToString(requestID), "direction": direction, "file_count": len(files)})
			if _, err = tx.ExecContext(ctx, "INSERT INTO outbox_events(id,event_type,aggregate_type,aggregate_id,aggregate_version,dedupe_key,payload) VALUES(?,'TRANSFER_REQUESTED','REQUEST',?,1,?,?)", outboxID, requestID, "transfer-requested:"+hex.EncodeToString(requestID), payload); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='TRANSFER_PENDING',frozen_snapshot=JSON_SET(COALESCE(frozen_snapshot,JSON_OBJECT()),'$.target_antivirus_required',?),version=version+1 WHERE id=?", targetAntivirus, requestID)
			return err
		}
		candidates, err := s.resolveCandidatesTx(ctx, tx, n, groupID, requesterID, instanceID)
		if err != nil {
			return err
		}
		if len(candidates) == 0 || len(candidates) > 20 {
			return errors.New("approver candidate count invalid")
		}
		if n.Mode == "SINGLE" {
			candidates = candidates[:1]
		}
		candidateJSON, _ := json.Marshal(toHex(candidates))
		required := 1
		if n.Mode == "ALL" {
			required = len(candidates)
		}
		due := s.now().UTC().Add(time.Duration(max(n.TimeoutHours, 24)) * time.Hour)
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='ACTIVE',candidate_snapshot=?,required_count=?,activated_at=UTC_TIMESTAMP(6),due_at=? WHERE instance_id=? AND node_id=?", candidateJSON, required, due, instanceID, n.ID); err != nil {
			return err
		}
		for _, candidate := range candidates {
			taskID, _ := id()
			if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_tasks(id,instance_id,node_id,assignee_id,status,due_at) VALUES(?,?,?,?,'PENDING',?)", taskID, instanceID, n.ID, candidate, due); err != nil {
				return err
			}
			notificationID, _ := id()
			if _, err = tx.ExecContext(ctx, `INSERT INTO inbox_notifications(id,user_id,event_type,title,body,request_id) VALUES(?,?,'APPROVAL_PENDING','新的审批待办',?,?)`, notificationID, candidate, "流程节点 "+n.ID+" 等待处理，请登录当前门户查看申请。", requestID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE workflow_instances SET current_node_id=?,version=version+1 WHERE id=?", n.ID, instanceID)
		return err
	}
	return errors.New("workflow path ended unexpectedly")
}
func (s *Service) resolveCandidatesTx(ctx context.Context, tx *sql.Tx, n Node, groupID, requesterID, instanceID []byte) ([][]byte, error) {
	// Prefer an approver who has not handled an earlier node, but do not block a
	// workflow when one person legitimately carries several approval roles (a
	// common setup in small teams and initial deployments).
	out, err := s.roleCandidatesTx(ctx, tx, n.Resolver, groupID, requesterID, instanceID, true)
	if err != nil {
		return nil, err
	}
	if n.Resolver == "GROUP_MANAGER" && len(out) == 0 {
		out, err = s.groupManagerCandidatesTx(ctx, tx, groupID, requesterID, instanceID, true)
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out, err = s.roleCandidatesTx(ctx, tx, n.Resolver, groupID, requesterID, instanceID, false)
	}
	if err != nil {
		return nil, err
	}
	if n.Resolver == "GROUP_MANAGER" && len(out) == 0 {
		out, err = s.groupManagerCandidatesTx(ctx, tx, groupID, requesterID, instanceID, false)
	}
	return out, err
}

func (s *Service) roleCandidatesTx(ctx context.Context, tx *sql.Tx, resolver string, groupID, requesterID, instanceID []byte, excludePrevious bool) ([][]byte, error) {
	query := `SELECT DISTINCT a.user_id FROM user_role_assignments a JOIN roles r ON r.id=a.role_id JOIN user_accounts u ON u.id=a.user_id JOIN org_groups g ON g.id=? WHERE r.code=? AND u.status='ACTIVE' AND (a.scope_type='GLOBAL' OR (a.scope_type='GROUP' AND a.scope_id=g.id) OR (a.scope_type='DEPARTMENT' AND a.scope_id=g.department_id)) AND a.user_id<>?`
	args := []any{groupID, resolver, requesterID}
	if excludePrevious {
		query += ` AND a.user_id NOT IN (SELECT actor_id FROM workflow_actions wa JOIN workflow_tasks wt ON wt.id=wa.task_id WHERE wt.instance_id=?)`
		args = append(args, instanceID)
	}
	query += ` ORDER BY HEX(a.user_id) LIMIT 21`
	return scanCandidateIDs(ctx, tx, query, args...)
}

func (s *Service) groupManagerCandidatesTx(ctx context.Context, tx *sql.Tx, groupID, requesterID, instanceID []byte, excludePrevious bool) ([][]byte, error) {
	query := `SELECT m.user_id FROM group_memberships m JOIN user_accounts u ON u.id=m.user_id WHERE m.group_id=? AND m.is_manager=TRUE AND u.status='ACTIVE' AND m.user_id<>?`
	args := []any{groupID, requesterID}
	if excludePrevious {
		query += ` AND m.user_id NOT IN (SELECT actor_id FROM workflow_actions wa JOIN workflow_tasks wt ON wt.id=wa.task_id WHERE wt.instance_id=?)`
		args = append(args, instanceID)
	}
	query += ` ORDER BY HEX(m.user_id) LIMIT 21`
	return scanCandidateIDs(ctx, tx, query, args...)
}

func scanCandidateIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([][]byte, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]byte{}
	for rows.Next() {
		var value []byte
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Service) ListTasks(ctx context.Context, userHex string) ([]Task, error) {
	user, err := decode(userHex)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(t.id),HEX(r.id),r.request_no,t.node_id,t.node_id,COALESCE(n.assignee_mode,'SINGLE'),t.status,t.due_at,t.version FROM workflow_tasks t JOIN workflow_instances i ON i.id=t.instance_id JOIN workflow_node_instances n ON n.instance_id=t.instance_id AND n.node_id=t.node_id JOIN exchange_requests r ON r.id=i.request_id WHERE t.assignee_id=? ORDER BY t.created_at DESC LIMIT 200`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err = rows.Scan(&t.ID, &t.RequestID, &t.RequestNo, &t.NodeID, &t.NodeName, &t.Mode, &t.Status, &t.DueAt, &t.Version); err != nil {
			return nil, err
		}
		t.ID = strings.ToLower(t.ID)
		t.RequestID = strings.ToLower(t.RequestID)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Service) Decide(ctx context.Context, taskHex, actorHex, decision, comment, key string, expected uint64, checklist []string, admin bool, reason string, mfaAt time.Time) error {
	taskID, err := decode(taskHex)
	if err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	decision = strings.ToUpper(decision)
	if decision != "APPROVE" && decision != "REJECT" {
		return errors.New("invalid decision")
	}
	if decision == "REJECT" && (len([]rune(comment)) < 5 || len([]rune(comment)) > 1000) {
		return errors.New("reject comment must contain 5-1000 characters")
	}
	if admin && (len([]rune(strings.TrimSpace(reason))) < 5 || time.Since(mfaAt) > 5*time.Minute) {
		return errors.New("admin action requires reason and recent MFA")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var instanceID, assigneeID, requestID []byte
	var nodeID, status, mode string
	var version uint64
	var frozenGraph []byte
	if err = tx.QueryRowContext(ctx, `SELECT t.instance_id,t.assignee_id,t.node_id,t.status,t.version,i.request_id,n.assignee_mode,i.frozen_graph FROM workflow_tasks t JOIN workflow_instances i ON i.id=t.instance_id JOIN workflow_node_instances n ON n.instance_id=t.instance_id AND n.node_id=t.node_id WHERE t.id=? FOR UPDATE`, taskID).Scan(&instanceID, &assigneeID, &nodeID, &status, &version, &requestID, &mode, &frozenGraph); err != nil {
		return err
	}
	if status != "PENDING" || version != expected {
		return errors.New("task version conflict")
	}
	if !admin && !bytesEqual(actor, assigneeID) {
		return errors.New("task is not assigned to actor")
	}
	graph, parseErr := Parse(frozenGraph)
	if parseErr != nil {
		return parseErr
	}
	var node Node
	for _, candidate := range graph.Nodes {
		if candidate.ID == nodeID {
			node = candidate
			break
		}
	}
	if node.CommentRequired && strings.TrimSpace(comment) == "" {
		return errors.New("approval comment is required")
	}
	checked := map[string]bool{}
	for _, item := range checklist {
		checked[item] = true
	}
	for _, requiredItem := range node.Checklist {
		if !checked[requiredItem] {
			return errors.New("approval checklist is incomplete")
		}
	}
	acting := "SELF"
	if admin {
		acting = "ADMIN_ON_BEHALF"
	}
	actionID, _ := id()
	before, _ := json.Marshal(map[string]any{"status": status})
	after, _ := json.Marshal(map[string]any{"status": decision})
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_actions(id,task_id,actor_id,original_assignee_id,decision,comment,acting_mode,delegation_reason,permission_scope_snapshot,mfa_verified_at,idempotency_key,before_state,after_state) VALUES(?,?,?,?,?,?,?,?,JSON_OBJECT(),?,?,?,?)", actionID, taskID, actor, assigneeID, decision, comment, acting, nullable(reason), mfaAt, key, before, after); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_tasks SET status=?,decided_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=?", map[bool]string{true: "APPROVED", false: "REJECTED"}[decision == "APPROVE"], taskID); err != nil {
		return err
	}
	if decision == "REJECT" {
		_, _ = tx.ExecContext(ctx, "UPDATE workflow_tasks SET status='CANCELLED' WHERE instance_id=? AND node_id=? AND status='PENDING'", instanceID, nodeID)
		_, _ = tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='REJECTED',completed_at=UTC_TIMESTAMP(6) WHERE instance_id=? AND node_id=?", instanceID, nodeID)
		_, _ = tx.ExecContext(ctx, "UPDATE workflow_instances SET status='REJECTED',completed_at=UTC_TIMESTAMP(6) WHERE id=?", instanceID)
		_, _ = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='REJECTED',version=version+1 WHERE id=?", requestID)
		return tx.Commit()
	}
	var approved, required int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),MAX(n.required_count) FROM workflow_tasks t JOIN workflow_node_instances n ON n.instance_id=t.instance_id AND n.node_id=t.node_id WHERE t.instance_id=? AND t.node_id=? AND t.status='APPROVED'", instanceID, nodeID).Scan(&approved, &required); err != nil {
		return err
	}
	complete := mode != "ALL" || approved >= required
	if !complete {
		_, err = tx.ExecContext(ctx, "UPDATE workflow_node_instances SET approved_count=? WHERE instance_id=? AND node_id=?", approved, instanceID, nodeID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	_, _ = tx.ExecContext(ctx, "UPDATE workflow_tasks SET status='CANCELLED' WHERE instance_id=? AND node_id=? AND status='PENDING'", instanceID, nodeID)
	_, _ = tx.ExecContext(ctx, "UPDATE workflow_node_instances SET status='APPROVED',approved_count=?,completed_at=UTC_TIMESTAMP(6) WHERE instance_id=? AND node_id=?", approved, instanceID, nodeID)
	var raw, pathRaw []byte
	if err = tx.QueryRowContext(ctx, "SELECT frozen_graph,selected_path FROM workflow_instances WHERE id=?", instanceID).Scan(&raw, &pathRaw); err != nil {
		return err
	}
	g, err := Parse(raw)
	if err != nil {
		return err
	}
	var selected []string
	_ = json.Unmarshal(pathRaw, &selected)
	if len(selected) == 0 {
		return errors.New("selected path missing")
	}
	index := -1
	for i, v := range selected {
		if v == nodeID {
			index = i + 1
			break
		}
	}
	if index < 1 {
		return errors.New("current node absent from path")
	}
	var groupID, requesterID []byte
	if err = tx.QueryRowContext(ctx, "SELECT group_id,requester_id FROM exchange_requests WHERE id=?", requestID).Scan(&groupID, &requesterID); err != nil {
		return err
	}
	if err = s.advanceTx(ctx, tx, instanceID, requestID, requesterID, groupID, g, selected, index); err != nil {
		return err
	}
	return tx.Commit()
}
func toHex(values [][]byte) []string {
	out := []string{}
	for _, v := range values {
		out = append(out, hex.EncodeToString(v))
	}
	return out
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

var _ = fmt.Sprint
