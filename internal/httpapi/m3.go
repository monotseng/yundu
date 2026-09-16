package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/exchange"
	"yundu/internal/idempotency"
)

type requestInput struct {
	Direction        string `json:"direction"`
	GroupID          string `json:"group_id"`
	BusinessSystemID string `json:"business_system_id"`
	Classification   string `json:"classification"`
	Purpose          string `json:"purpose"`
	ExternalTicket   string `json:"external_ticket"`
}
type uploadSessionInput struct {
	OriginalName    string `json:"original_name"`
	SizeBytes       int64  `json:"size_bytes"`
	ContentType     string `json:"content_type"`
	ExpectedVersion uint64 `json:"expected_version"`
}
type submitInput struct {
	ExpectedVersion     uint64 `json:"expected_version"`
	DeclarationAccepted bool   `json:"declaration_accepted"`
}
type cancelInput struct {
	Reason          string `json:"reason"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (s *Server) requestBootstrap(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效，请重新登录")
		return
	}
	groups, err := s.organization.ListUserGroups(r.Context(), user.ID)
	if err != nil {
		problem(w, 500, "BOOTSTRAP_FAILED", "无法读取申请选项")
		return
	}
	systems, err := s.organization.ListBusinessSystems(r.Context())
	if err != nil {
		problem(w, 500, "BOOTSTRAP_FAILED", "无法读取申请选项")
		return
	}
	zone := s.portalZone(r)
	directions := []string{}
	if zone == "OFFICE" {
		directions = []string{"OFFICE_TO_PROD"}
	} else if zone == "PRODUCTION" {
		directions = []string{"PROD_TO_OFFICE"}
	}
	writeJSON(w, 200, map[string]any{"portal_zone": zone, "directions": directions, "groups": groups, "business_systems": systems, "limits": map[string]any{"office_file_bytes": s.cfg.Limits.OfficeFileBytes, "production_file_bytes": s.cfg.Limits.ProductionFileBytes, "files_per_request": s.cfg.Limits.FilesPerRequest, "office_request_bytes": s.cfg.Limits.OfficeRequestBytes}})
}
func (s *Server) requestReadiness(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requestUser(w, r, "request.create"); !ok {
		return
	}
	direction := strings.ToUpper(r.URL.Query().Get("direction"))
	groupID, businessID := r.URL.Query().Get("group_id"), r.URL.Query().Get("business_system_id")
	sourceZone, targetZone := "OFFICE", "PRODUCTION"
	if direction == "PROD_TO_OFFICE" {
		sourceZone, targetZone = targetZone, sourceZone
	}
	_, sourceErr := s.integrations.GetPublishedStorage(r.Context(), sourceZone)
	_, targetErr := s.integrations.GetPublishedStorage(r.Context(), targetZone)
	channels, channelErr := s.integrations.ListChannels(r.Context())
	channelReady := false
	if channelErr == nil {
		for _, item := range channels {
			if item.Direction == direction && item.Status == "PUBLISHED" {
				channelReady = true
			}
		}
	}
	selection, workflowErr := s.workflow.Resolve(r.Context(), direction, groupID, businessID)
	workflowVersion := ""
	if workflowErr == nil {
		workflowVersion = selection.VersionID
	}
	ready := sourceErr == nil && targetErr == nil && channelReady && workflowErr == nil
	writeJSON(w, 200, map[string]any{"ready": ready, "source_storage_ready": sourceErr == nil, "target_storage_ready": targetErr == nil, "channel_ready": channelReady, "workflow_ready": workflowErr == nil, "workflow_version_id": workflowVersion})
}

func (s *Server) requestUser(w http.ResponseWriter, r *http.Request, permission string) (string, bool) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return "", false
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效，请重新登录")
		return "", false
	}
	allowed, err := s.authz.Allowed(r.Context(), user.ID, permission, "", "")
	if err != nil {
		problem(w, 500, "AUTHORIZATION_ERROR", "权限服务暂时不可用")
		return "", false
	}
	if !allowed {
		problem(w, 403, "PERMISSION_DENIED", "没有执行此操作的权限")
		return "", false
	}
	return user.ID, true
}
func (s *Server) createRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "创建申请必须提供 Idempotency-Key")
		return
	}
	var in requestInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	payload, _ := json.Marshal(in)
	claim, err := s.idempotency.Claim(r.Context(), user, "POST:/api/v1/requests", key, payload)
	if err != nil {
		if errors.Is(err, idempotency.ErrConflict) {
			problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键正在处理或已用于不同请求")
		} else {
			problem(w, 400, "INVALID_IDEMPOTENCY_KEY", "幂等键格式无效")
		}
		return
	}
	if claim.Replay {
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(claim.StatusCode)
		_, _ = w.Write(claim.Response)
		return
	}
	item, err := s.exchange.Create(r.Context(), user, s.portalZone(r), in.Direction, in.GroupID, in.BusinessSystemID, in.Classification, in.Purpose, in.ExternalTicket)
	if err != nil {
		s.idempotency.Release(r.Context(), user, "POST:/api/v1/requests", key)
		problem(w, 422, "REQUEST_REJECTED", "申请参数、所属团队或来源门户不符合要求")
		return
	}
	response, _ := json.Marshal(item)
	if err = s.idempotency.Complete(r.Context(), user, "POST:/api/v1/requests", key, 201, response); err != nil {
		problem(w, 500, "IDEMPOTENCY_STORE_FAILED", "申请已创建但幂等结果登记失败")
		return
	}
	writeJSON(w, 201, item)
}
func (s *Server) listRequests(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.read.own")
	if !ok {
		return
	}
	items, err := s.exchange.ListOwn(r.Context(), user)
	if err != nil {
		problem(w, 500, "REQUEST_LIST_FAILED", "无法读取申请列表")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createUploadSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "预占上传必须提供 Idempotency-Key")
		return
	}
	var in uploadSessionInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	route := "POST:/api/v1/requests/" + r.PathValue("request_id") + "/upload-sessions"
	payload, _ := json.Marshal(in)
	claim, err := s.idempotency.Claim(r.Context(), user, route, key, payload)
	if err != nil {
		if errors.Is(err, idempotency.ErrConflict) {
			problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键正在处理或已用于不同请求")
		} else {
			problem(w, 400, "INVALID_IDEMPOTENCY_KEY", "幂等键格式无效")
		}
		return
	}
	if claim.Replay {
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(claim.StatusCode)
		_, _ = w.Write(claim.Response)
		return
	}
	zone := "PRODUCTION"
	if s.portalZone(r) == "OFFICE" {
		zone = "OFFICE"
	}
	storage, err := s.integrations.GetPublishedStorage(r.Context(), zone)
	if err != nil {
		s.idempotency.Release(r.Context(), user, route, key)
		problem(w, 503, "SOURCE_STORAGE_UNAVAILABLE", "来源门户存储尚未发布")
		return
	}
	session, err := s.exchange.ReserveUpload(r.Context(), user, r.PathValue("request_id"), s.portalZone(r), storage.ID, storage.Config.Bucket, in.OriginalName, in.ContentType, in.SizeBytes, in.ExpectedVersion)
	if err != nil {
		s.idempotency.Release(r.Context(), user, route, key)
		if errors.Is(err, exchange.ErrQuota) {
			problem(w, 413, "UPLOAD_QUOTA_EXCEEDED", "文件数量或容量超过申请限制")
			return
		}
		if errors.Is(err, exchange.ErrConflict) {
			problem(w, 409, "REQUEST_VERSION_CONFLICT", "申请版本或状态已变化")
			return
		}
		problem(w, 422, "UPLOAD_RESERVATION_REJECTED", err.Error())
		return
	}
	response, _ := json.Marshal(session)
	if err = s.idempotency.Complete(r.Context(), user, route, key, 201, response); err != nil {
		problem(w, 500, "IDEMPOTENCY_STORE_FAILED", "上传已预占但幂等结果登记失败")
		return
	}
	writeJSON(w, 201, session)
}
func (s *Server) uploadContent(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	session, err := s.exchange.BeginUpload(r.Context(), user, r.PathValue("upload_id"))
	if err != nil {
		problem(w, 409, "UPLOAD_SESSION_INVALID", "上传会话不存在、过期或已使用")
		return
	}
	if r.ContentLength != session.ExpectedBytes {
		_ = s.exchange.FailUpload(r.Context(), user, session.ID)
		problem(w, 400, "CONTENT_LENGTH_MISMATCH", "请求体长度与预占长度不一致")
		return
	}
	version, err := s.integrations.GetStorageVersion(r.Context(), session.StorageVersionID)
	if err != nil {
		_ = s.exchange.FailUpload(r.Context(), user, session.ID)
		problem(w, 503, "SOURCE_STORAGE_UNAVAILABLE", "预占的存储版本不可用")
		return
	}
	access, err := s.secretStore.Get(r.Context(), version.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		_ = s.exchange.FailUpload(r.Context(), user, session.ID)
		problem(w, 503, "CREDENTIAL_UNAVAILABLE", "存储凭据不可用")
		return
	}
	secret, err := s.secretStore.Get(r.Context(), version.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		clear(access)
		_ = s.exchange.FailUpload(r.Context(), user, session.ID)
		problem(w, 503, "CREDENTIAL_UNAVAILABLE", "存储凭据不可用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, session.ExpectedBytes)
	result, err := s3adapter.Upload(r.Context(), version.Config, session.ObjectKey, r.Header.Get("Content-Type"), r.Body, session.ExpectedBytes, string(access), string(secret))
	clear(access)
	clear(secret)
	if err != nil {
		_ = s.exchange.FailUpload(r.Context(), user, session.ID)
		problem(w, 502, "UPLOAD_FAILED", "对象存储上传失败")
		return
	}
	if err = s.exchange.CompleteUpload(r.Context(), user, session.ID, result.VersionID, result.SHA256, result.Bytes); err != nil {
		problem(w, 500, "UPLOAD_FINALIZE_FAILED", "对象已写入但登记失败，已进入对账范围")
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) abortUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "取消上传必须提供 Idempotency-Key")
		return
	}
	if err := s.exchange.AbortUpload(r.Context(), user, r.PathValue("upload_id")); err != nil {
		if errors.Is(err, exchange.ErrConflict) {
			problem(w, 409, "UPLOAD_ALREADY_COMPLETED", "已完成上传不能取消")
			return
		}
		problem(w, 404, "UPLOAD_SESSION_NOT_FOUND", "上传会话不存在")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) submitRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "提交申请必须提供 Idempotency-Key")
		return
	}
	var in submitInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if !in.DeclarationAccepted {
		problem(w, 422, "DECLARATION_REQUIRED", "必须确认文件与用途声明")
		return
	}
	route := "POST:/api/v1/requests/" + r.PathValue("request_id") + "/submit"
	payload, _ := json.Marshal(in)
	claim, err := s.idempotency.Claim(r.Context(), user, route, key, payload)
	if err != nil {
		problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键冲突")
		return
	}
	if claim.Replay {
		w.Header().Set("Idempotent-Replayed", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(claim.StatusCode)
		_, _ = w.Write(claim.Response)
		return
	}
	groupID, businessID, direction, err := s.exchange.RequestScope(r.Context(), user, r.PathValue("request_id"))
	if err != nil {
		s.idempotency.Release(r.Context(), user, route, key)
		problem(w, 404, "REQUEST_NOT_FOUND", "申请不存在")
		return
	}
	selection, err := s.workflow.Resolve(r.Context(), direction, groupID, businessID)
	if err != nil {
		s.idempotency.Release(r.Context(), user, route, key)
		problem(w, 422, "WORKFLOW_NOT_BOUND", "当前方向和范围没有已发布流程")
		return
	}
	version, err := s.exchange.Submit(r.Context(), user, r.PathValue("request_id"), in.ExpectedVersion, s.cfg.Antivirus.Enabled, selection.VersionID, selection.Hash)
	if err != nil {
		s.idempotency.Release(r.Context(), user, route, key)
		if errors.Is(err, exchange.ErrConflict) {
			problem(w, 409, "REQUEST_VERSION_CONFLICT", "申请版本或状态已变化")
			return
		}
		problem(w, 422, "SUBMIT_REJECTED", err.Error())
		return
	}
	body := map[string]any{"status": "SCANNING", "version": version}
	response, _ := json.Marshal(body)
	if err = s.idempotency.Complete(r.Context(), user, route, key, 202, response); err != nil {
		problem(w, 500, "IDEMPOTENCY_STORE_FAILED", "提交已受理但结果登记失败")
		return
	}
	writeJSON(w, 202, body)
}
func (s *Server) listRequestChecks(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.read.own")
	if !ok {
		return
	}
	items, err := s.scanning.ListChecks(r.Context(), r.PathValue("request_id"), user)
	if err != nil {
		problem(w, 404, "REQUEST_NOT_FOUND", "申请不存在")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) cancelRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "取消申请必须提供 Idempotency-Key")
		return
	}
	var in cancelInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.exchange.Cancel(r.Context(), user, r.PathValue("request_id"), in.Reason, in.ExpectedVersion); err != nil {
		problem(w, 409, "REQUEST_CANCEL_CONFLICT", err.Error())
		return
	}
	_ = s.workflow.CancelRequest(r.Context(), r.PathValue("request_id"))
	w.WriteHeader(204)
}
