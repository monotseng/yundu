//go:build integration

package workflow

import (
	"context"
	"database/sql"
	"encoding/hex"
	_ "github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMySQLWorkflowLifecycle(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	actor, _ := id()
	department, _ := id()
	group, _ := id()
	suffix := strings.ToUpper(hex.EncodeToString(actor[:6]))
	if _, err = db.Exec("INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,?,'ACTIVE','ACTIVE')", actor, "m5-"+strings.ToLower(suffix), "M5 Test"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO departments(id,code,name) VALUES(?,?,?)", department, "M5D"+suffix, "M5 Test"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO org_groups(id,department_id,code,name) VALUES(?,?,?,?)", group, department, "M5G"+suffix, "M5 Test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{"DELETE FROM workflow_bindings WHERE created_by=?", "DELETE v FROM workflow_validations v WHERE v.created_by=?", "UPDATE workflow_definitions SET current_version_id=NULL WHERE created_by=?", "DELETE v FROM workflow_versions v JOIN workflow_definitions d ON d.id=v.definition_id WHERE d.created_by=?", "DELETE FROM workflow_definitions WHERE created_by=?"} {
			if _, e := db.Exec(q, actor); e != nil {
				t.Errorf("cleanup: %v", e)
			}
		}
		db.Exec("DELETE FROM org_groups WHERE id=?", group)
		db.Exec("DELETE FROM departments WHERE id=?", department)
		db.Exec("DELETE FROM user_accounts WHERE id=?", actor)
	})
	svc := New(db)
	created, err := svc.Create(context.Background(), "M5"+suffix, "M5 lifecycle", "", validGraph(), hex.EncodeToString(actor))
	if err != nil {
		t.Fatal(err)
	}
	validation, err := svc.ValidateVersion(context.Background(), created.DraftVersionID, hex.EncodeToString(actor))
	if err != nil || len(validation) != 0 {
		t.Fatalf("validation=%+v err=%v", validation, err)
	}
	if err = svc.Publish(context.Background(), created.DraftVersionID, hex.EncodeToString(actor), "integration"); err != nil {
		t.Fatal(err)
	}
	cloned, err := svc.CreateRevision(context.Background(), created.ID, hex.EncodeToString(actor))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.UpdateDraft(context.Background(), cloned, validGraph(), map[string]any{"zoom": 1}, 1); err != nil {
		t.Fatal(err)
	}
	if validation, err = svc.ValidateVersion(context.Background(), cloned, hex.EncodeToString(actor)); err != nil || len(validation) != 0 {
		t.Fatalf("cloned validation=%+v err=%v", validation, err)
	}
	if err = svc.Bind(context.Background(), "OFFICE_TO_PROD", "GLOBAL", "", created.DraftVersionID, hex.EncodeToString(actor)); err != nil {
		t.Fatal(err)
	}
	selection, err := svc.Resolve(context.Background(), "OFFICE_TO_PROD", hex.EncodeToString(group), "")
	if err != nil || selection.VersionID != created.DraftVersionID {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	requester, _ := id()
	manager, _ := id()
	security, _ := id()
	departmentApprover, _ := id()
	requestID, _ := id()
	users := [][]byte{requester, manager, security, departmentApprover}
	for index, user := range users {
		if _, err = db.Exec("INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,?,'ACTIVE','ACTIVE')", user, "m5-runtime-"+strings.ToLower(hex.EncodeToString(user[:6])), "Runtime"); err != nil {
			t.Fatal(err)
		}
		_ = index
	}
	if _, err = db.Exec("INSERT INTO group_memberships(group_id,user_id,is_manager) VALUES(?,?,FALSE),(?,?,TRUE)", group, requester, group, manager); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []struct {
		user []byte
		role string
	}{{manager, "GROUP_MANAGER"}, {security, "SECURITY_OFFICER"}, {departmentApprover, "DEPARTMENT_MANAGER"}} {
		assignmentID, _ := id()
		if _, err = db.Exec("INSERT INTO user_role_assignments(id,user_id,role_id,scope_type,scope_id) SELECT ?,?,id,'GLOBAL',NULL FROM roles WHERE code=?", assignmentID, assignment.user, assignment.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO exchange_requests(id,request_no,requester_id,group_id,direction,classification,purpose,status,workflow_version_id,frozen_snapshot) VALUES(?,?,?,?, 'OFFICE_TO_PROD','INTERNAL','runtime','IN_REVIEW',?,JSON_OBJECT())", requestID, "M5-"+suffix, requester, group, mustDecode(created.DraftVersionID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM workflow_actions WHERE task_id IN (SELECT id FROM workflow_tasks WHERE instance_id IN (SELECT id FROM workflow_instances WHERE request_id=?))", requestID)
		db.Exec("DELETE FROM workflow_tasks WHERE instance_id IN (SELECT id FROM workflow_instances WHERE request_id=?)", requestID)
		db.Exec("DELETE FROM workflow_node_instances WHERE instance_id IN (SELECT id FROM workflow_instances WHERE request_id=?)", requestID)
		db.Exec("DELETE FROM workflow_instances WHERE request_id=?", requestID)
		db.Exec("DELETE FROM exchange_requests WHERE id=?", requestID)
		for _, user := range users {
			db.Exec("DELETE FROM user_role_assignments WHERE user_id=?", user)
			db.Exec("DELETE FROM group_memberships WHERE user_id=?", user)
			db.Exec("DELETE FROM user_accounts WHERE id=?", user)
		}
	})
	if err = svc.StartRequest(context.Background(), hex.EncodeToString(requestID)); err != nil {
		t.Fatal(err)
	}
	for _, assignee := range [][]byte{manager, security, departmentApprover} {
		var taskID []byte
		var version uint64
		if err = db.QueryRow("SELECT id,version FROM workflow_tasks WHERE assignee_id=? AND status='PENDING'", assignee).Scan(&taskID, &version); err != nil {
			t.Fatalf("pending task for %x: %v", assignee, err)
		}
		if err = svc.Decide(context.Background(), hex.EncodeToString(taskID), hex.EncodeToString(assignee), "APPROVE", "approved", "m5-"+hex.EncodeToString(taskID), version, nil, false, "", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	var finalStatus string
	if err = db.QueryRow("SELECT status FROM exchange_requests WHERE id=?", requestID).Scan(&finalStatus); err != nil || finalStatus != "APPROVED" {
		t.Fatalf("status=%s err=%v", finalStatus, err)
	}
}

func mustDecode(value string) []byte { decoded, _ := hex.DecodeString(value); return decoded }
