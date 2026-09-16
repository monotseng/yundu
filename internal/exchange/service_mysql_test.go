//go:build integration

package exchange

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func TestMySQLConcurrentFileCapacity(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	user, _ := newID()
	department, _ := newID()
	group, _ := newID()
	integration, _ := newID()
	storage, _ := newID()
	username := "m3-" + strings.ToUpper(hexID(user))
	if _, err = db.Exec(`INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,'M3','ACTIVE','ACTIVE')`, user, username); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO departments(id,code,name) VALUES(?,CONCAT('M3D',HEX(?)),'M3')", []any{department, department}},
		{"INSERT INTO org_groups(id,department_id,code,name) VALUES(?,?,CONCAT('M3G',HEX(?)),'M3')", []any{group, department, group}},
		{"INSERT INTO group_memberships(group_id,user_id) VALUES(?,?)", []any{group, user}},
		{"INSERT INTO integration_definitions(id,name,type,zone,status,created_by) VALUES(?,'m3-test','S3_STORAGE','OFFICE','PUBLISHED',?)", []any{integration, user}},
		{`INSERT INTO integration_versions(id,integration_id,revision,config_json,config_sha256,status,created_by) VALUES(?,?,1,JSON_OBJECT(),UNHEX(REPEAT('00',32)),'PUBLISHED',?)`, []any{storage, integration, user}},
	} {
		if _, err = db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, query := range []string{"DELETE us FROM upload_sessions us JOIN user_accounts u ON u.id=us.user_id WHERE u.username=?", "DELETE qc FROM quota_counters qc JOIN user_accounts u ON u.id=qc.user_id WHERE u.username=?", "DELETE rf FROM request_files rf JOIN exchange_requests r ON r.id=rf.request_id JOIN user_accounts u ON u.id=r.requester_id WHERE u.username=?", "DELETE r FROM exchange_requests r JOIN user_accounts u ON u.id=r.requester_id WHERE u.username=?", "DELETE iv FROM integration_versions iv JOIN integration_definitions d ON d.id=iv.integration_id JOIN user_accounts u ON u.id=d.created_by WHERE u.username=?", "DELETE d FROM integration_definitions d JOIN user_accounts u ON u.id=d.created_by WHERE u.username=?", "DELETE gm FROM group_memberships gm JOIN user_accounts u ON u.id=gm.user_id WHERE u.username=?"} {
			if _, cleanupErr := db.Exec(query, username); cleanupErr != nil {
				t.Errorf("cleanup: %v", cleanupErr)
			}
		}
		db.Exec("DELETE FROM org_groups WHERE id=?", group)
		db.Exec("DELETE FROM departments WHERE id=?", department)
		db.Exec("DELETE FROM user_accounts WHERE username=?", username)
	})
	svc := New(db, Limits{OfficeFileBytes: 30 << 20, ProductionFileBytes: 100 << 20, OfficeRequestBytes: 150 << 20, FilesPerRequest: 5})
	req, err := svc.Create(ctx, hexID(user), "OFFICE", "OFFICE_TO_PROD", hexID(group), "", "INTERNAL", "capacity test", "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for retry := 0; retry < 20; retry++ {
				var version uint64
				if err := db.QueryRow("SELECT version FROM exchange_requests WHERE id=?", mustDecode(req.ID)).Scan(&version); err != nil {
					results <- err
					return
				}
				_, err := svc.ReserveUpload(ctx, hexID(user), req.ID, "OFFICE", hexID(storage), "bucket", "file.csv", "text/csv", 1, version)
				if errors.Is(err, ErrConflict) {
					continue
				}
				results <- err
				return
			}
			results <- errors.New("retry exhausted")
		}()
	}
	wg.Wait()
	close(results)
	success, quota := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrQuota) {
			quota++
		} else {
			t.Errorf("unexpected result: %v", err)
		}
	}
	if success != 5 || quota != 1 {
		t.Fatalf("success=%d quota=%d", success, quota)
	}
}
func hexID(v []byte) string      { return fmt.Sprintf("%x", v) }
func mustDecode(v string) []byte { b, _ := hex.DecodeString(v); return b }
