//go:build integration

package downloads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	_ "github.com/go-sql-driver/mysql"
	"os"
	"sync"
	"testing"
	"time"
	"yundu/internal/integrations"
	"yundu/internal/secrets"
)

func TestMySQLConcurrentGrantLimitAndEncryptedReplay(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id := func() []byte { return random(16) }
	user, dep, group, definition, storage, request, file, session := id(), id(), id(), id(), id(), id(), id(), id()
	username := "m7-" + hex.EncodeToString(user)
	sessionToken := "m7-session-" + hex.EncodeToString(user)
	sessionHash := sha256.Sum256([]byte(sessionToken))
	digest := sha256.Sum256([]byte("m7"))
	now := time.Now().UTC()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, e := db.Exec(q, a...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,'M7','ACTIVE','ACTIVE')`, user, username)
	exec(`INSERT INTO sessions(id,user_id,token_hash,portal_zone,session_version,mfa_verified_at,last_seen_at,expires_at) VALUES(?,?,?,'OFFICE',1,?,?,?)`, session, user, sessionHash[:], now, now, now.Add(time.Hour))
	exec(`INSERT INTO departments(id,code,name) VALUES(?,CONCAT('M7D',HEX(?)),'M7')`, dep, dep)
	exec(`INSERT INTO org_groups(id,department_id,code,name) VALUES(?,?,CONCAT('M7G',HEX(?)),'M7')`, group, dep, group)
	exec(`INSERT INTO integration_definitions(id,name,type,zone,status,created_by) VALUES(?,'m7-storage','S3_STORAGE','OFFICE','PUBLISHED',?)`, definition, user)
	exec(`INSERT INTO integration_versions(id,integration_id,revision,config_json,config_sha256,status,created_by) VALUES(?,?,1,JSON_OBJECT(),?,'PUBLISHED',?)`, storage, definition, digest[:], user)
	exec(`UPDATE integration_definitions SET current_version_id=? WHERE id=?`, storage, definition)
	exec(`INSERT INTO exchange_requests(id,request_no,requester_id,group_id,direction,classification,purpose,status,file_count,total_bytes,expires_at) VALUES(?,CONCAT('M7-',LEFT(HEX(?),20)),?,?,'PROD_TO_OFFICE','INTERNAL','m7 grant test','READY',1,2,?)`, request, request, user, group, now.Add(time.Hour))
	exec(`INSERT INTO request_files(id,request_id,original_name,object_key,source_storage_revision_id,source_bucket,source_version_id,source_sha256,size_bytes,target_storage_revision_id,target_bucket,target_object_key,target_version_id,target_sha256,status) VALUES(?,?,'数据.csv','source',?,'source','sv',?,2,?,'target','target','tv',?,'READY')`, file, request, storage, digest[:], storage, digest[:])
	exec(`INSERT INTO file_check_results(id,request_id,file_id,stage,check_type,required,status,rule_version,engine,sha256,checked_bytes,coverage_complete,report_summary,started_at,finished_at) VALUES(?,?,?,'TARGET','HASH',TRUE,'PASSED','m7','test',?,2,TRUE,JSON_OBJECT(),?,?)`, id(), request, file, digest[:], now, now)
	t.Cleanup(func() {
		db.Exec(`UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND holder_id IN (SELECT id FROM download_grants WHERE user_id=?)`, user)
		db.Exec(`DELETE FROM download_grants WHERE user_id=?`, user)
		db.Exec(`DELETE FROM file_check_results WHERE request_id=?`, request)
		db.Exec(`DELETE FROM request_files WHERE request_id=?`, request)
		db.Exec(`DELETE FROM exchange_requests WHERE id=?`, request)
		db.Exec(`UPDATE integration_definitions SET current_version_id=NULL WHERE id=?`, definition)
		db.Exec(`DELETE FROM integration_versions WHERE id=?`, storage)
		db.Exec(`DELETE FROM integration_definitions WHERE id=?`, definition)
		db.Exec(`DELETE FROM org_groups WHERE id=?`, group)
		db.Exec(`DELETE FROM departments WHERE id=?`, dep)
		db.Exec(`DELETE FROM sessions WHERE id=?`, session)
		db.Exec(`DELETE FROM user_accounts WHERE id=?`, user)
	})
	box, _ := secrets.NewBox(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	store := secrets.NewStore(db, box)
	svc := New(db, integrations.New(db, store), store)
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := svc.Issue(ctx, hex.EncodeToString(user), sessionToken, "OFFICE", hex.EncodeToString(file), "m7-key-0000000"+string(rune('a'+i)), 1)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	success, busy := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrBusy) {
			busy++
		} else {
			t.Fatalf("unexpected: %v", e)
		}
	}
	if success != 2 || busy != 1 {
		t.Fatalf("success=%d busy=%d", success, busy)
	}
	exec(`UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND holder_id IN (SELECT id FROM download_grants WHERE user_id=?)`, user)
	exec(`UPDATE download_grants SET status='EXPIRED' WHERE user_id=?`, user)
	first, err := svc.Issue(ctx, hex.EncodeToString(user), sessionToken, "OFFICE", hex.EncodeToString(file), "m7-key-replay01", 1)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Issue(ctx, hex.EncodeToString(user), sessionToken, "OFFICE", hex.EncodeToString(file), "m7-key-replay01", 1)
	if err != nil || again.Token != first.Token {
		t.Fatal("encrypted replay mismatch")
	}
	var cipher []byte
	_ = db.QueryRow(`SELECT grant_token_ciphertext FROM download_grants WHERE user_id=? AND idempotency_key='m7-key-replay01'`, user).Scan(&cipher)
	raw, _ := base64.RawURLEncoding.DecodeString(first.Token)
	if string(cipher) == string(raw) {
		t.Fatal("grant stored in plaintext")
	}
}
