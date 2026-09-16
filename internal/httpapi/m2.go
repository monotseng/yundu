package httpapi

import (
	"net/http"
	"strings"

	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/integrations"
)

type departmentInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
type groupInput struct {
	DepartmentID    string   `json:"department_id"`
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	ManagerUserIDs  []string `json:"manager_user_ids"`
	ExpectedVersion uint64   `json:"expected_version"`
}
type userInput struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	AvatarEmoji     string `json:"avatar_emoji"`
	Status          string `json:"status"`
	ExpectedVersion uint64 `json:"expected_version"`
	GroupID         string `json:"group_id"`
	IsManager       bool   `json:"is_manager"`
	Priority        uint16 `json:"priority"`
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	var in userInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.organization.UpdateUser(r.Context(), r.PathValue("user_id"), in.DisplayName, in.AvatarEmoji, in.Status, in.GroupID, in.IsManager, in.Priority, in.ExpectedVersion); err != nil {
		problem(w, 409, "USER_UPDATE_FAILED", err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) reissueUserActivation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	var in struct {
		ExpectedVersion uint64 `json:"expected_version"`
	}
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	token, expires, err := s.organization.ReissueActivation(r.Context(), r.PathValue("user_id"), in.ExpectedVersion)
	if err != nil {
		problem(w, 409, "ACTIVATION_REISSUE_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"activation_token": token, "expires_at": expires})
}

type secretInput struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
}
type s3Input struct {
	Name            string                `json:"name"`
	Zone            string                `json:"zone"`
	Config          integrations.S3Config `json:"config"`
	ExpectedVersion uint64                `json:"expected_version,omitempty"`
}
type publishInput struct {
	ExpectedVersion uint64 `json:"expected_version"`
}
type memberInput struct {
	UserID    string `json:"user_id"`
	IsManager bool   `json:"is_manager"`
	Priority  uint16 `json:"priority"`
}
type roleInput struct {
	RoleID    string `json:"role_id"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id"`
}
type businessSystemInput struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	DepartmentID      string   `json:"department_id"`
	AllowedDirections []string `json:"allowed_directions"`
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request, permission string) (string, bool) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return "", false
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, http.StatusUnauthorized, "SESSION_INVALID", "会话已失效，请重新登录")
		return "", false
	}
	allowed, err := s.authz.Allowed(r.Context(), user.ID, permission, "", "")
	if err != nil {
		problem(w, http.StatusInternalServerError, "AUTHORIZATION_ERROR", "权限服务暂时不可用")
		return "", false
	}
	if !allowed {
		problem(w, http.StatusForbidden, "PERMISSION_DENIED", "没有执行此操作的权限")
		return "", false
	}
	return user.ID, true
}
func (s *Server) listDepartments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	items, err := s.organization.ListDepartments(r.Context())
	if err != nil {
		problem(w, 500, "ORGANIZATION_ERROR", "无法读取部门")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createDepartment(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	var in departmentInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, err := s.organization.CreateDepartment(r.Context(), in.Code, in.Name)
	if err != nil {
		problem(w, 400, "INVALID_DEPARTMENT", err.Error())
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	items, err := s.organization.ListGroups(r.Context())
	if err != nil {
		problem(w, 500, "ORGANIZATION_ERROR", "无法读取组")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	var in groupInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, err := s.organization.CreateGroup(r.Context(), in.DepartmentID, in.Code, in.Name)
	if err != nil {
		problem(w, 400, "INVALID_GROUP", err.Error())
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	var in groupInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.organization.UpdateGroup(r.Context(), r.PathValue("group_id"), in.DepartmentID, in.Name, in.ManagerUserIDs, in.ExpectedVersion); err != nil {
		problem(w, http.StatusConflict, "TEAM_UPDATE_FAILED", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) addGroupMember(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	var in memberInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.organization.AddGroupMember(r.Context(), r.PathValue("group_id"), in.UserID, in.IsManager, in.Priority); err != nil {
		problem(w, 400, "INVALID_GROUP_MEMBERSHIP", "无法保存团队成员关系")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	items, err := s.organization.ListRoles(r.Context())
	if err != nil {
		problem(w, 500, "ORGANIZATION_ERROR", "无法读取角色")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) assignRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	var in roleInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.organization.AssignRole(r.Context(), r.PathValue("user_id"), in.RoleID, in.ScopeType, in.ScopeID); err != nil {
		problem(w, 400, "INVALID_ROLE_ASSIGNMENT", "无法保存角色授权")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listBusinessSystems(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	items, err := s.organization.ListBusinessSystems(r.Context())
	if err != nil {
		problem(w, 500, "ORGANIZATION_ERROR", "无法读取业务系统")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createBusinessSystem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "organization.manage"); !ok {
		return
	}
	var in businessSystemInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, err := s.organization.CreateBusinessSystem(r.Context(), in.Code, in.Name, in.DepartmentID, in.AllowedDirections)
	if err != nil {
		problem(w, 400, "INVALID_BUSINESS_SYSTEM", "业务系统参数无效或编码重复")
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	items, err := s.organization.ListUsers(r.Context())
	if err != nil {
		problem(w, 500, "ORGANIZATION_ERROR", "无法读取用户")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "user.manage"); !ok {
		return
	}
	var in userInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, token, expires, err := s.organization.CreateUser(r.Context(), in.Username, in.DisplayName, in.AvatarEmoji)
	if err != nil {
		problem(w, 400, "INVALID_USER", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"user": item, "activation_token": token, "expires_at": expires})
}
func (s *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "secret.manage")
	if !ok {
		return
	}
	if s.secretStore == nil {
		problem(w, 503, "SECRET_STORE_UNAVAILABLE", "秘密存储不可用")
		return
	}
	var in secretInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	in.Purpose = strings.ToUpper(in.Purpose)
	allowedPurpose := map[string]bool{"S3_ACCESS_KEY": true, "S3_SECRET_KEY": true, "WECOM_WEBHOOK": true, "PROXY_PASSWORD": true, "SMTP_PASSWORD": true, "AUDIT_SIGNING_KEY": true, "LLM_API_KEY": true}
	if !allowedPurpose[in.Purpose] {
		problem(w, 400, "INVALID_SECRET_PURPOSE", "不支持的秘密用途")
		return
	}
	id, err := s.secretStore.Put(r.Context(), in.Name, in.Purpose, []byte(in.Value), actor)
	if err != nil {
		problem(w, 400, "SECRET_WRITE_FAILED", "秘密写入失败")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "purpose": in.Purpose, "configured": true})
}
func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "integration.read"); !ok {
		return
	}
	items, err := s.integrations.List(r.Context())
	if err != nil {
		problem(w, 500, "INTEGRATION_ERROR", "无法读取集成配置")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createS3Integration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "integration.manage")
	if !ok {
		return
	}
	var in s3Input
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if _, err := s.secretStore.Get(r.Context(), in.Config.AccessKeySecretID, "S3_ACCESS_KEY"); err != nil {
		problem(w, 400, "INVALID_ACCESS_KEY_REFERENCE", "访问密钥引用无效")
		return
	}
	if _, err := s.secretStore.Get(r.Context(), in.Config.SecretKeySecretID, "S3_SECRET_KEY"); err != nil {
		problem(w, 400, "INVALID_SECRET_KEY_REFERENCE", "秘密密钥引用无效")
		return
	}
	item, err := s.integrations.CreateS3Draft(r.Context(), in.Name, in.Zone, in.Config, actor)
	if err != nil {
		if strings.Contains(err.Error(), "already configured") {
			problem(w, 409, "DUPLICATE_STORAGE", "该安全域已存在相同访问地址和 Bucket 的存储配置")
			return
		}
		problem(w, 400, "INVALID_INTEGRATION", err.Error())
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) getS3Integration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "integration.read"); !ok {
		return
	}
	item, err := s.integrations.GetS3Draft(r.Context(), r.PathValue("integration_id"))
	if err != nil {
		problem(w, 404, "INTEGRATION_DRAFT_NOT_FOUND", "S3 存储草稿不存在")
		return
	}
	writeJSON(w, 200, item)
}
func (s *Server) updateS3Integration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "integration.manage"); !ok {
		return
	}
	var in s3Input
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if _, err := s.secretStore.Get(r.Context(), in.Config.AccessKeySecretID, "S3_ACCESS_KEY"); err != nil {
		problem(w, 400, "INVALID_ACCESS_KEY_REFERENCE", "访问密钥引用无效")
		return
	}
	if _, err := s.secretStore.Get(r.Context(), in.Config.SecretKeySecretID, "S3_SECRET_KEY"); err != nil {
		problem(w, 400, "INVALID_SECRET_KEY_REFERENCE", "秘密密钥引用无效")
		return
	}
	if err := s.integrations.UpdateS3Draft(r.Context(), r.PathValue("integration_id"), in.Name, in.Zone, in.Config, in.ExpectedVersion); err != nil {
		problem(w, 409, "INTEGRATION_UPDATE_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deleteS3Integration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "integration.manage"); !ok {
		return
	}
	if err := s.integrations.DeleteS3Draft(r.Context(), r.PathValue("integration_id")); err != nil {
		problem(w, 409, "INTEGRATION_DELETE_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) testS3Integration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "integration.manage")
	if !ok {
		return
	}
	version, err := s.integrations.GetLatestVersion(r.Context(), r.PathValue("integration_id"))
	if err != nil {
		problem(w, 404, "INTEGRATION_NOT_FOUND", "集成版本不存在")
		return
	}
	access, err := s.secretStore.Get(r.Context(), version.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		problem(w, 503, "CREDENTIAL_UNAVAILABLE", "存储凭据不可用")
		return
	}
	secret, err := s.secretStore.Get(r.Context(), version.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		problem(w, 503, "CREDENTIAL_UNAVAILABLE", "存储凭据不可用")
		return
	}
	result, err := s3adapter.Probe(r.Context(), version.Config, string(access), string(secret))
	clear(access)
	clear(secret)
	if err != nil {
		_ = s.integrations.RecordTest(r.Context(), version.ID, actor, "FAILED", "S3_PROBE", "PROBE_FAILED", safeIntegrationError(err))
		detail := safeIntegrationError(err)
		if strings.Contains(err.Error(), "VERSIONING_REQUIRED") {
			detail = "Bucket 未启用版本控制；请在对象存储端启用 Versioning 后重新测试"
		}
		problem(w, 502, "S3_PROBE_FAILED", detail)
		return
	}
	if err = s.integrations.RecordTest(r.Context(), version.ID, actor, "PASSED", "COMPLETE", "", ""); err != nil {
		problem(w, 500, "TEST_RECORD_FAILED", "测试结果无法持久化")
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) publishIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "integration.publish")
	if !ok {
		return
	}
	var in publishInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.integrations.Publish(r.Context(), r.PathValue("integration_id"), in.ExpectedVersion, actor); err != nil {
		problem(w, 409, "INTEGRATION_PUBLISH_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func safeIntegrationError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 300 {
		text = text[:300]
	}
	for _, marker := range []string{"access_key", "secret_key", "X-Amz-Signature", "Authorization"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(marker)) {
			return "外部存储测试失败，敏感错误已隐藏"
		}
	}
	return text
}
