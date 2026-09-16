package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	auditservice "yundu/internal/audit"
	"yundu/internal/incidents"
	"yundu/internal/monitoring"
	"yundu/internal/notifications"
	"yundu/internal/outbound"
)

func (s *Server) monitoringStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "monitoring.manage"); !ok {
		return
	}
	v, err := s.monitoring.Status(r.Context())
	if err != nil {
		problem(w, 500, "MONITORING_ERROR", "无法读取监控状态")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) downloadZabbixTemplate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "monitoring.manage"); !ok {
		return
	}
	raw := monitoring.RenderZabbixTemplate()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="yundu-zabbix-7.0-template.json"`)
	_, _ = w.Write([]byte(raw))
}

type monitoringTokenInput struct {
	Name         string     `json:"name"`
	AllowedCIDRs []string   `json:"allowed_cidrs"`
	ExpiresAt    *time.Time `json:"expires_at"`
}
type auditArchiveInput struct {
	From               time.Time `json:"from"`
	To                 time.Time `json:"to"`
	StorageVersionID   string    `json:"storage_version_id"`
	SigningKeySecretID string    `json:"signing_key_secret_id"`
}

func (s *Server) listAuditArchives(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "audit.read"); !ok {
		return
	}
	items, e := s.audit.ListArchives(r.Context())
	if e != nil {
		problem(w, 500, "AUDIT_ARCHIVE_ERROR", "无法读取审计归档")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createAuditArchive(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "security.manage"); !ok {
		return
	}
	if s.secretStore == nil {
		problem(w, 503, "SECRET_STORE_UNAVAILABLE", "秘密存储不可用")
		return
	}
	var in auditArchiveInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	storage, e := s.integrations.GetUsableStorageVersion(r.Context(), in.StorageVersionID)
	if e != nil {
		problem(w, 400, "INVALID_ARCHIVE_STORAGE", "独立审计归档存储版本不可用")
		return
	}
	item, e := s.audit.Archive(r.Context(), in.From, in.To, storage, s.secretStore, in.SigningKeySecretID)
	if e != nil {
		problem(w, 502, "AUDIT_ARCHIVE_FAILED", e.Error())
		return
	}
	writeJSON(w, 201, item)
}

type proxyInput struct {
	Name             string               `json:"name"`
	Config           outbound.ProxyConfig `json:"config"`
	PasswordSecretID string               `json:"password_secret_id"`
	ExpectedVersion  uint64               `json:"expected_version"`
}
type wecomInput struct {
	Name            string               `json:"name"`
	Config          outbound.WeComConfig `json:"config"`
	WebhookSecretID string               `json:"webhook_secret_id"`
	ExpectedVersion uint64               `json:"expected_version"`
}
type smtpInput struct {
	Name             string              `json:"name"`
	Config           outbound.SMTPConfig `json:"config"`
	PasswordSecretID string              `json:"password_secret_id"`
}
type testMessageInput struct {
	Content        string   `json:"content"`
	MentionedUsers []string `json:"mentioned_users"`
}

func (s *Server) listOutboundIntegrations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "notification.manage"); !ok {
		return
	}
	if s.outbound == nil {
		problem(w, 503, "SECRET_STORE_UNAVAILABLE", "秘密存储不可用")
		return
	}
	v, e := s.outbound.List(r.Context())
	if e != nil {
		problem(w, 500, "OUTBOUND_ERROR", "无法读取出站集成")
		return
	}
	writeJSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createOutboundProxy(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	var in proxyInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	v, e := s.outbound.CreateProxy(r.Context(), in.Name, in.Config, in.PasswordSecretID, actor)
	if e != nil {
		problem(w, 400, "INVALID_PROXY", e.Error())
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) createWeComBot(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	var in wecomInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	v, e := s.outbound.CreateWeCom(r.Context(), in.Name, in.Config, in.WebhookSecretID, actor)
	if e != nil {
		problem(w, 400, "INVALID_WECOM_BOT", e.Error())
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) updateOutboundProxy(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	var in proxyInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.outbound.UpdateProxy(r.Context(), r.PathValue("integration_id"), in.Name, in.Config, in.PasswordSecretID, actor, in.ExpectedVersion); e != nil {
		problem(w, 409, "OUTBOUND_DRAFT_UPDATE_FAILED", e.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) createOutboundProxyRevision(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	v, e := s.outbound.CreateProxyRevision(r.Context(), r.PathValue("integration_id"), actor)
	if e != nil {
		problem(w, 409, "OUTBOUND_REVISION_FAILED", e.Error())
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) updateWeComBot(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	var in wecomInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.outbound.UpdateWeCom(r.Context(), r.PathValue("integration_id"), in.Name, in.Config, in.WebhookSecretID, actor, in.ExpectedVersion); e != nil {
		problem(w, 409, "OUTBOUND_DRAFT_UPDATE_FAILED", e.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deleteOutboundIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	if e := s.outbound.Delete(r.Context(), r.PathValue("integration_id"), actor); e != nil {
		problem(w, 409, "OUTBOUND_DELETE_FAILED", e.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) createSMTP(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "notification.manage")
	if !ok {
		return
	}
	var in smtpInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	v, e := s.outbound.CreateSMTP(r.Context(), in.Name, in.Config, in.PasswordSecretID, actor)
	if e != nil {
		problem(w, 400, "INVALID_SMTP", e.Error())
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) publishOutboundIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "integration.publish")
	if !ok {
		return
	}
	var in publishInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.outbound.Publish(r.Context(), r.PathValue("integration_id"), in.ExpectedVersion, actor); e != nil {
		problem(w, 409, "INTEGRATION_VERSION_CONFLICT", e.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) testWeComBot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "notification.manage"); !ok {
		return
	}
	var in testMessageInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if strings.TrimSpace(in.Content) == "" || len(in.MentionedUsers) == 0 {
		problem(w, 400, "TEST_TARGET_REQUIRED", "必须明确填写固定测试文字和测试成员")
		return
	}
	v, e := s.outbound.TestWeCom(r.Context(), r.PathValue("integration_id"), in.Content, in.MentionedUsers)
	if e != nil {
		problem(w, 502, "WECOM_TEST_FAILED", e.Error())
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) testSMTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "notification.manage"); !ok {
		return
	}
	var in struct {
		Recipient string `json:"recipient"`
	}
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	v, e := s.outbound.TestSMTP(r.Context(), r.PathValue("integration_id"), in.Recipient)
	if e != nil {
		problem(w, 502, "SMTP_TEST_FAILED", e.Error())
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "audit.read"); !ok {
		return
	}
	from, e1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, e2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	cursor, _ := strconv.ParseUint(r.URL.Query().Get("cursor"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if e1 != nil || e2 != nil {
		problem(w, 400, "AUDIT_TIME_WINDOW_REQUIRED", "必须提供 RFC3339 格式的 from 和 to")
		return
	}
	page, err := s.audit.List(r.Context(), auditservice.Filter{From: from, To: to, Cursor: cursor, Limit: limit, ActorID: r.URL.Query().Get("actor_id"), RequestID: r.URL.Query().Get("request_id"), Action: r.URL.Query().Get("action"), Result: r.URL.Query().Get("result"), Direction: r.URL.Query().Get("direction")})
	if err != nil {
		if errors.Is(err, auditservice.ErrInvalidRange) {
			problem(w, 400, "INVALID_AUDIT_WINDOW", err.Error())
		} else {
			problem(w, 500, "AUDIT_QUERY_FAILED", "无法读取审计事件")
		}
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return "", false
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效，请重新登录")
		return "", false
	}
	return user.ID, true
}
func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var cursor time.Time
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, _ = time.Parse(time.RFC3339Nano, raw)
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.notifications.List(r.Context(), user, cursor, limit)
	if err != nil {
		problem(w, 500, "NOTIFICATION_ERROR", "无法读取通知")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if err := s.notifications.Read(r.Context(), user, r.PathValue("notification_id")); err != nil {
		if errors.Is(err, notifications.ErrNotFound) {
			problem(w, 404, "NOTIFICATION_NOT_FOUND", "通知不存在")
		} else {
			problem(w, 500, "NOTIFICATION_ERROR", "无法更新通知")
		}
		return
	}
	w.WriteHeader(204)
}

type incidentMutation struct {
	Resolution      string `json:"resolution"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (s *Server) listSecurityIncidents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "security.manage"); !ok {
		return
	}
	items, err := s.incidents.List(r.Context())
	if err != nil {
		problem(w, 500, "INCIDENT_ERROR", "无法读取安全事件")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) ackSecurityIncident(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorized(w, r, "security.manage")
	if !ok {
		return
	}
	var in incidentMutation
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if err := s.incidents.Acknowledge(r.Context(), r.PathValue("incident_id"), user, in.ExpectedVersion); err != nil {
		s.incidentError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": r.PathValue("incident_id"), "status": "ACKNOWLEDGED", "version": in.ExpectedVersion + 1})
}
func (s *Server) closeSecurityIncident(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorized(w, r, "security.manage")
	if !ok {
		return
	}
	var in incidentMutation
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	session, e := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if e != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	if err := s.incidents.Close(r.Context(), r.PathValue("incident_id"), user, in.Resolution, in.ExpectedVersion, session.MFAVerifiedAt); err != nil {
		s.incidentError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": r.PathValue("incident_id"), "status": "CLOSED", "version": in.ExpectedVersion + 1})
}
func (s *Server) incidentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, incidents.ErrNotFound):
		problem(w, 404, "INCIDENT_NOT_FOUND", "安全事件不存在")
	case errors.Is(err, incidents.ErrConflict):
		problem(w, 409, "INCIDENT_VERSION_CONFLICT", "安全事件状态已变化，请刷新后重试")
	case errors.Is(err, incidents.ErrResolutionRequired):
		problem(w, 400, "RESOLUTION_REQUIRED", "关闭安全事件必须填写处置说明")
	case errors.Is(err, incidents.ErrRecentMFARequired):
		problem(w, 403, "RECENT_MFA_REQUIRED", "关闭安全事件需要 5 分钟内的 MFA 验证")
	default:
		problem(w, 500, "INCIDENT_ERROR", "无法更新安全事件")
	}
}

func (s *Server) listMonitoringTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "monitoring.manage"); !ok {
		return
	}
	items, err := s.monitoring.ListTokens(r.Context())
	if err != nil {
		problem(w, 500, "MONITORING_ERROR", "无法读取监控令牌")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createMonitoringToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "monitoring.manage"); !ok {
		return
	}
	var in monitoringTokenInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	item, err := s.monitoring.CreateToken(r.Context(), in.Name, in.AllowedCIDRs, in.ExpiresAt)
	if err != nil {
		problem(w, 400, "INVALID_MONITORING_TOKEN", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, item)
}

func (s *Server) revokeMonitoringToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "monitoring.manage"); !ok {
		return
	}
	if err := s.monitoring.Revoke(r.Context(), r.PathValue("token_id")); err != nil {
		problem(w, 404, "MONITORING_TOKEN_NOT_FOUND", "监控令牌不存在或已撤销")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) zabbixSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == "" {
		problem(w, 401, "MONITORING_UNAUTHORIZED", "监控令牌无效")
		return
	}
	err := s.monitoring.Authorize(r.Context(), strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")), clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, monitoring.ErrDisabled):
			problem(w, 404, "MONITORING_NOT_CONFIGURED", "监控接口尚未配置")
		case errors.Is(err, monitoring.ErrUnauthorized):
			problem(w, 401, "MONITORING_UNAUTHORIZED", "监控令牌无效")
		case errors.Is(err, monitoring.ErrForbidden):
			problem(w, 403, "MONITORING_SOURCE_DENIED", "监控源地址不在允许列表")
		case errors.Is(err, monitoring.ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			problem(w, 429, "MONITORING_RATE_LIMITED", "监控请求过于频繁")
		default:
			problem(w, 503, "MONITORING_UNAVAILABLE", "监控服务暂时不可用")
		}
		return
	}
	snapshot, err := s.monitoring.Snapshot(r.Context())
	if err != nil {
		problem(w, 503, "MONITORING_SNAPSHOT_STALE", "监控快照不可用或已过期")
		return
	}
	writeJSON(w, 200, snapshot)
}
