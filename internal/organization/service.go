package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{1,63}$`)

type Service struct {
	db  *sql.DB
	now func() time.Time
}
type Department struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version uint64 `json:"version"`
}
type Group struct {
	ID             string   `json:"id"`
	DepartmentID   string   `json:"department_id"`
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Version        uint64   `json:"version"`
	ManagerUserIDs []string `json:"manager_user_ids"`
}
type User struct {
	ID              string           `json:"id"`
	Username        string           `json:"username"`
	DisplayName     string           `json:"display_name"`
	AvatarEmoji     string           `json:"avatar_emoji"`
	Status          string           `json:"status"`
	MFAState        string           `json:"mfa_state"`
	Version         uint64           `json:"version"`
	TeamMemberships []TeamMembership `json:"team_memberships"`
}
type TeamMembership struct {
	GroupID   string `json:"group_id"`
	IsManager bool   `json:"is_manager"`
	Priority  uint16 `json:"priority"`
}
type BusinessSystem struct {
	ID                string   `json:"id"`
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	DepartmentID      string   `json:"department_id,omitempty"`
	Status            string   `json:"status"`
	AllowedDirections []string `json:"allowed_directions"`
	Version           uint64   `json:"version"`
}
type Role struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

func New(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }
func (s *Service) CreateDepartment(ctx context.Context, code, name string) (Department, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" || len([]rune(name)) > 128 {
		return Department{}, errors.New("invalid department")
	}
	id, err := newID()
	if err != nil {
		return Department{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO departments(id,code,name,status) VALUES(?,?,?,'ACTIVE')", id, code, name)
	if err != nil {
		return Department{}, err
	}
	return Department{ID: hex.EncodeToString(id), Code: code, Name: name, Status: "ACTIVE", Version: 1}, nil
}
func (s *Service) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT HEX(id),code,name,status,version FROM departments ORDER BY code LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Department{}
	for rows.Next() {
		var v Department
		if err = rows.Scan(&v.ID, &v.Code, &v.Name, &v.Status, &v.Version); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) CreateGroup(ctx context.Context, departmentHex, code, name string) (Group, error) {
	departmentID, err := decodeID(departmentHex)
	if err != nil {
		return Group{}, err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" {
		return Group{}, errors.New("invalid group")
	}
	id, err := newID()
	if err != nil {
		return Group{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO org_groups(id,department_id,code,name,status) VALUES(?,?,?,?,'ACTIVE')", id, departmentID, code, name)
	if err != nil {
		return Group{}, err
	}
	return Group{ID: hex.EncodeToString(id), DepartmentID: departmentHex, Code: code, Name: name, Status: "ACTIVE", Version: 1}, nil
}
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT HEX(id),HEX(department_id),code,name,status,version FROM org_groups ORDER BY code LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var v Group
		if err = rows.Scan(&v.ID, &v.DepartmentID, &v.Code, &v.Name, &v.Status, &v.Version); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		v.DepartmentID = strings.ToLower(v.DepartmentID)
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	managerRows, err := s.db.QueryContext(ctx, `SELECT LOWER(HEX(group_id)),LOWER(HEX(user_id)) FROM group_memberships WHERE is_manager=TRUE AND valid_from<=UTC_TIMESTAMP(6) AND (valid_until IS NULL OR valid_until>UTC_TIMESTAMP(6)) ORDER BY group_id,priority,user_id`)
	if err != nil {
		return nil, err
	}
	defer managerRows.Close()
	managerMap := map[string][]string{}
	for managerRows.Next() {
		var groupID, userID string
		if err = managerRows.Scan(&groupID, &userID); err != nil {
			return nil, err
		}
		managerMap[groupID] = append(managerMap[groupID], userID)
	}
	if err = managerRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ManagerUserIDs = managerMap[out[i].ID]
	}
	return out, nil
}
func (s *Service) UpdateGroup(ctx context.Context, groupHex, departmentHex, name string, managerUserHexes []string, expected uint64) error {
	groupID, err := decodeID(groupHex)
	if err != nil {
		return err
	}
	departmentID, err := decodeID(departmentHex)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 128 || len(managerUserHexes) == 0 {
		return errors.New("team name and at least one manager are required")
	}
	managerIDs := make([][]byte, 0, len(managerUserHexes))
	seen := map[string]bool{}
	for _, value := range managerUserHexes {
		value = strings.ToLower(strings.TrimSpace(value))
		if seen[value] {
			continue
		}
		userID, decodeErr := decodeID(value)
		if decodeErr != nil {
			return decodeErr
		}
		seen[value] = true
		managerIDs = append(managerIDs, userID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE org_groups SET department_id=?,name=?,version=version+1 WHERE id=? AND version=?`, departmentID, name, groupID, expected)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("team version conflict")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE group_memberships SET is_manager=FALSE WHERE group_id=? AND is_manager=TRUE`, groupID); err != nil {
		return err
	}
	for priority, userID := range managerIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO group_memberships(group_id,user_id,is_manager,priority) VALUES(?,?,TRUE,?) ON DUPLICATE KEY UPDATE is_manager=TRUE,priority=VALUES(priority),valid_until=NULL`, groupID, userID, priority); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Service) ListUserGroups(ctx context.Context, userHex string) ([]Group, error) {
	userID, err := decodeID(userHex)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(g.id),HEX(g.department_id),g.code,g.name,g.status,g.version FROM org_groups g JOIN group_memberships m ON m.group_id=g.id WHERE m.user_id=? AND m.valid_from<=UTC_TIMESTAMP(6) AND (m.valid_until IS NULL OR m.valid_until>UTC_TIMESTAMP(6)) AND g.status='ACTIVE' ORDER BY g.code`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var v Group
		if err = rows.Scan(&v.ID, &v.DepartmentID, &v.Code, &v.Name, &v.Status, &v.Version); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		v.DepartmentID = strings.ToLower(v.DepartmentID)
		out = append(out, v)
	}
	return out, rows.Err()
}

var allowedAvatarEmoji = map[string]bool{
	// Keep former choices valid so existing profiles remain editable.
	"👤": true, "😀": true, "😎": true, "🧑‍💻": true, "👩‍💻": true, "👨‍💻": true, "🧑‍🔧": true, "🧑‍🚀": true, "🌟": true, "🚀": true, "🛡️": true,
	"🐼": true, "🦊": true, "🐯": true, "🦁": true, "🐨": true, "🐻": true, "🐻‍❄️": true, "🐰": true, "🐶": true, "🐱": true, "🐵": true, "🐧": true, "🦉": true, "🦅": true, "🐺": true, "🦄": true, "🐬": true, "🐳": true, "🦦": true, "🦥": true, "🐙": true, "🦋": true,
}

func normalizeAvatarEmoji(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "🐼"
	}
	if !allowedAvatarEmoji[value] {
		return "", errors.New("invalid avatar emoji")
	}
	return value, nil
}

func (s *Service) CreateUser(ctx context.Context, username, displayName, avatarEmoji string) (User, string, time.Time, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	displayName = strings.TrimSpace(displayName)
	if !regexp.MustCompile(`^[a-z0-9._-]{3,64}$`).MatchString(username) || displayName == "" {
		return User{}, "", time.Time{}, errors.New("invalid user")
	}
	avatarEmoji, err := normalizeAvatarEmoji(avatarEmoji)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	id, err := newID()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	tokenID, _ := newID()
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return User{}, "", time.Time{}, err
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := s.now().UTC().Add(24 * time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_accounts(id,username,display_name,avatar_emoji,status,mfa_state) VALUES(?,?,?,?,'PENDING_ACTIVATION','UNBOUND')", id, username, displayName, avatarEmoji); err != nil {
		return User{}, "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO activation_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'ACTIVATE',?)", tokenID, id, hash[:], expires); err != nil {
		return User{}, "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, "", time.Time{}, err
	}
	return User{ID: hex.EncodeToString(id), Username: username, DisplayName: displayName, AvatarEmoji: avatarEmoji, Status: "PENDING_ACTIVATION", MFAState: "UNBOUND", Version: 1}, token, expires, nil
}
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT HEX(id),username,display_name,avatar_emoji,status,mfa_state,version FROM user_accounts ORDER BY username LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var v User
		if err = rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.AvatarEmoji, &v.Status, &v.MFAState, &v.Version); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	membershipRows, err := s.db.QueryContext(ctx, `SELECT LOWER(HEX(user_id)),LOWER(HEX(group_id)),is_manager,priority FROM group_memberships WHERE valid_from<=UTC_TIMESTAMP(6) AND (valid_until IS NULL OR valid_until>UTC_TIMESTAMP(6)) ORDER BY user_id,priority,group_id`)
	if err != nil {
		return nil, err
	}
	defer membershipRows.Close()
	memberships := map[string][]TeamMembership{}
	for membershipRows.Next() {
		var userID string
		var item TeamMembership
		if err = membershipRows.Scan(&userID, &item.GroupID, &item.IsManager, &item.Priority); err != nil {
			return nil, err
		}
		memberships[userID] = append(memberships[userID], item)
	}
	if err = membershipRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].TeamMemberships = memberships[out[i].ID]
	}
	return out, nil
}
func (s *Service) UpdateUser(ctx context.Context, userHex, displayName, avatarEmoji, status, groupHex string, manager bool, priority uint16, expected uint64) error {
	id, err := decodeID(userHex)
	if err != nil {
		return err
	}
	displayName, status = strings.TrimSpace(displayName), strings.ToUpper(strings.TrimSpace(status))
	avatarEmoji, err = normalizeAvatarEmoji(avatarEmoji)
	if err != nil {
		return err
	}
	if displayName == "" || len([]rune(displayName)) > 128 || (status != "ACTIVE" && status != "DISABLED") {
		return errors.New("invalid user profile or status")
	}
	groupID, err := decodeID(groupHex)
	if err != nil {
		return errors.New("a valid team is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE user_accounts SET display_name=?,avatar_emoji=?,status=?,session_version=session_version+IF(status<>?,1,0),version=version+1 WHERE id=? AND version=? AND status IN ('ACTIVE','DISABLED')`, displayName, avatarEmoji, status, status, id, expected)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("user version or lifecycle state conflict")
	}
	if status == "DISABLED" {
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6) WHERE user_id=? AND revoked_at IS NULL`, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE group_memberships SET valid_until=UTC_TIMESTAMP(6) WHERE user_id=? AND group_id<>? AND valid_until IS NULL`, id, groupID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO group_memberships(group_id,user_id,is_manager,priority) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE is_manager=VALUES(is_manager),priority=VALUES(priority),valid_until=NULL`, groupID, id, manager, priority); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) ReissueActivation(ctx context.Context, userHex string, expected uint64) (string, time.Time, error) {
	id, err := decodeID(userHex)
	if err != nil {
		return "", time.Time{}, err
	}
	tokenID, _ := newID()
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := s.now().UTC().Add(24 * time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE user_accounts SET version=version+1 WHERE id=? AND version=? AND status='PENDING_ACTIVATION'`, id, expected)
	if err != nil {
		return "", time.Time{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return "", time.Time{}, errors.New("user is no longer pending activation")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE activation_tokens SET consumed_at=UTC_TIMESTAMP(6) WHERE user_id=? AND purpose='ACTIVATE' AND consumed_at IS NULL`, id); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO activation_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'ACTIVATE',?)`, tokenID, id, hash[:], expires); err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}
func (s *Service) AddGroupMember(ctx context.Context, groupHex, userHex string, manager bool, priority uint16) error {
	groupID, err := decodeID(groupHex)
	if err != nil {
		return err
	}
	userID, err := decodeID(userHex)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO group_memberships(group_id,user_id,is_manager,priority) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE is_manager=VALUES(is_manager),priority=VALUES(priority),valid_until=NULL`, groupID, userID, manager, priority)
	return err
}
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT HEX(id),code,name FROM roles ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var v Role
		if err = rows.Scan(&v.ID, &v.Code, &v.Name); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) AssignRole(ctx context.Context, userHex, roleHex, scopeType, scopeHex string) error {
	userID, err := decodeID(userHex)
	if err != nil {
		return err
	}
	roleID, err := decodeID(roleHex)
	if err != nil {
		return err
	}
	scopeType = strings.ToUpper(scopeType)
	var scope any
	if scopeType == "GLOBAL" {
		scope = nil
	} else if scopeType == "DEPARTMENT" || scopeType == "GROUP" {
		scope, err = decodeID(scopeHex)
		if err != nil {
			return err
		}
	} else {
		return errors.New("invalid role scope")
	}
	assignmentID, _ := newID()
	_, err = s.db.ExecContext(ctx, "INSERT INTO user_role_assignments(id,user_id,role_id,scope_type,scope_id) VALUES(?,?,?,?,?)", assignmentID, userID, roleID, scopeType, scope)
	return err
}
func (s *Service) CreateBusinessSystem(ctx context.Context, code, name, departmentHex string, directions []string) (BusinessSystem, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" {
		return BusinessSystem{}, errors.New("invalid business system")
	}
	var department any
	if departmentHex != "" {
		var err error
		department, err = decodeID(departmentHex)
		if err != nil {
			return BusinessSystem{}, err
		}
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, d := range directions {
		d = strings.ToUpper(d)
		if d != "PROD_TO_OFFICE" && d != "OFFICE_TO_PROD" {
			return BusinessSystem{}, errors.New("invalid direction")
		}
		if !seen[d] {
			clean = append(clean, d)
			seen[d] = true
		}
	}
	if len(clean) == 0 {
		return BusinessSystem{}, errors.New("direction required")
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return BusinessSystem{}, err
	}
	ident, _ := newID()
	_, err = s.db.ExecContext(ctx, "INSERT INTO business_systems(id,code,name,department_id,allowed_directions) VALUES(?,?,?,?,?)", ident, code, name, department, raw)
	if err != nil {
		return BusinessSystem{}, err
	}
	return BusinessSystem{ID: hex.EncodeToString(ident), Code: code, Name: name, DepartmentID: departmentHex, Status: "ACTIVE", AllowedDirections: clean, Version: 1}, nil
}
func (s *Service) ListBusinessSystems(ctx context.Context) ([]BusinessSystem, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT HEX(id),code,name,COALESCE(HEX(department_id),''),status,allowed_directions,version FROM business_systems ORDER BY code LIMIT 500")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BusinessSystem{}
	for rows.Next() {
		var v BusinessSystem
		var raw []byte
		if err = rows.Scan(&v.ID, &v.Code, &v.Name, &v.DepartmentID, &v.Status, &raw, &v.Version); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		v.DepartmentID = strings.ToLower(v.DepartmentID)
		if err = json.Unmarshal(raw, &v.AllowedDirections); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func newID() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func decodeID(value string) ([]byte, error) {
	id, err := hex.DecodeString(value)
	if err != nil || len(id) != 16 {
		return nil, errors.New("invalid id")
	}
	return id, nil
}
