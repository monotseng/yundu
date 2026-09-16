package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/skip2/go-qrcode"
	"yundu/internal/aiassist"
	auditservice "yundu/internal/audit"
	"yundu/internal/authorization"
	"yundu/internal/config"
	"yundu/internal/downloads"
	"yundu/internal/exchange"
	"yundu/internal/idempotency"
	"yundu/internal/identity"
	"yundu/internal/incidents"
	"yundu/internal/integrations"
	"yundu/internal/monitoring"
	"yundu/internal/notifications"
	"yundu/internal/organization"
	"yundu/internal/outbound"
	store "yundu/internal/persistence/mysql"
	"yundu/internal/recovery"
	"yundu/internal/scanning"
	"yundu/internal/secrets"
	"yundu/internal/transfer"
	webassets "yundu/internal/web"
	"yundu/internal/workflow"
)

type Server struct {
	cfg           config.Config
	db            *sql.DB
	version       string
	ready         atomic.Bool
	assets        fs.FS
	identity      *identity.Service
	authz         *authorization.Service
	organization  *organization.Service
	secretStore   *secrets.Store
	integrations  *integrations.Service
	exchange      *exchange.Service
	idempotency   *idempotency.Service
	scanning      *scanning.Service
	workflow      *workflow.Service
	transfer      *transfer.Service
	downloads     *downloads.Service
	monitoring    *monitoring.Service
	audit         *auditservice.Service
	notifications *notifications.Service
	outbound      *outbound.Service
	incidents     *incidents.Service
	aiassist      *aiassist.Service
	recovery      *recovery.Service
}

func New(cfg config.Config, db *sql.DB, version string) (*Server, error) {
	assets, err := fs.Sub(webassets.Dist, "dist")
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, db: db, version: version, assets: assets}
	s.authz = authorization.New(db)
	s.organization = organization.New(db)
	s.exchange = exchange.New(db, exchange.Limits{OfficeFileBytes: cfg.Limits.OfficeFileBytes, ProductionFileBytes: cfg.Limits.ProductionFileBytes, OfficeRequestBytes: cfg.Limits.OfficeRequestBytes, FilesPerRequest: cfg.Limits.FilesPerRequest})
	s.idempotency = idempotency.New(db)
	s.workflow = workflow.New(db)
	s.monitoring = monitoring.New(db)
	s.audit = auditservice.New(db)
	s.notifications = notifications.New(db)
	s.incidents = incidents.New(db)
	s.recovery = recovery.New(db)
	if box, boxErr := secrets.NewBox(cfg.Security.MasterKey); boxErr == nil {
		s.identity = identity.NewService(db, box)
		s.secretStore = secrets.NewStore(db, box)
		s.integrations = integrations.New(db, s.secretStore)
		s.scanning = scanning.New(db, s.integrations, s.secretStore, cfg.Antivirus, s.workflow)
		s.transfer = transfer.New(db, s.integrations, s.secretStore, cfg.Antivirus)
		s.downloads = downloads.New(db, s.integrations, s.secretStore)
		s.outbound = outbound.New(db, s.secretStore)
		s.aiassist = aiassist.New(db, s.secretStore)
	} else if cfg.Environment == "production" {
		return nil, fmt.Errorf("initialize identity secret box: %w", boxErr)
	}
	s.ready.Store(true)
	return s, nil
}

func (s *Server) SetReady(v bool) { s.ready.Store(v) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.readiness)
	mux.HandleFunc("GET /api/v1/runtime", s.runtime)
	mux.HandleFunc("POST /api/v1/auth/password", s.passwordLogin)
	mux.HandleFunc("POST /api/v1/auth/totp", s.totpLogin)
	mux.HandleFunc("POST /api/v1/auth/activate", s.activate)
	mux.HandleFunc("POST /api/v1/auth/mfa/confirm", s.confirmMFA)
	mux.HandleFunc("POST /api/v1/auth/mfa/rebind", s.rebindMFA)
	mux.HandleFunc("GET /api/v1/auth/me", s.me)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("POST /api/v1/auth/logout-all", s.logoutAll)
	mux.HandleFunc("POST /api/v1/auth/password/change", s.changePassword)
	mux.HandleFunc("POST /api/v1/admin/mfa-recoveries", s.initiateMFARecovery)
	mux.HandleFunc("POST /api/v1/admin/mfa-recoveries/{recovery_id}/approve", s.approveMFARecovery)
	mux.HandleFunc("GET /api/v1/admin/departments", s.listDepartments)
	mux.HandleFunc("POST /api/v1/admin/departments", s.createDepartment)
	mux.HandleFunc("GET /api/v1/admin/groups", s.listGroups)
	mux.HandleFunc("POST /api/v1/admin/groups", s.createGroup)
	mux.HandleFunc("PATCH /api/v1/admin/groups/{group_id}", s.updateGroup)
	mux.HandleFunc("PUT /api/v1/admin/groups/{group_id}/members", s.addGroupMember)
	mux.HandleFunc("GET /api/v1/admin/users", s.listUsers)
	mux.HandleFunc("POST /api/v1/admin/users", s.createUser)
	mux.HandleFunc("PATCH /api/v1/admin/users/{user_id}", s.updateUser)
	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/activation", s.reissueUserActivation)
	mux.HandleFunc("GET /api/v1/admin/roles", s.listRoles)
	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/roles", s.assignRole)
	mux.HandleFunc("GET /api/v1/admin/business-systems", s.listBusinessSystems)
	mux.HandleFunc("POST /api/v1/admin/business-systems", s.createBusinessSystem)
	mux.HandleFunc("POST /api/v1/admin/secrets", s.createSecret)
	mux.HandleFunc("GET /api/v1/admin/integrations", s.listIntegrations)
	mux.HandleFunc("POST /api/v1/admin/integrations/s3", s.createS3Integration)
	mux.HandleFunc("GET /api/v1/admin/integrations/{integration_id}/s3", s.getS3Integration)
	mux.HandleFunc("PATCH /api/v1/admin/integrations/{integration_id}/s3", s.updateS3Integration)
	mux.HandleFunc("DELETE /api/v1/admin/integrations/{integration_id}", s.deleteS3Integration)
	mux.HandleFunc("POST /api/v1/admin/integrations/{integration_id}/test", s.testS3Integration)
	mux.HandleFunc("POST /api/v1/admin/integrations/{integration_id}/publish", s.publishIntegration)
	mux.HandleFunc("GET /api/v1/admin/monitoring/tokens", s.listMonitoringTokens)
	mux.HandleFunc("GET /api/v1/admin/monitoring/status", s.monitoringStatus)
	mux.HandleFunc("GET /api/v1/admin/monitoring/zabbix-template", s.downloadZabbixTemplate)
	mux.HandleFunc("POST /api/v1/admin/monitoring/tokens", s.createMonitoringToken)
	mux.HandleFunc("DELETE /api/v1/admin/monitoring/tokens/{token_id}", s.revokeMonitoringToken)
	mux.HandleFunc("POST /api/v1/admin/monitoring/tokens/{token_id}/revoke", s.revokeMonitoringToken)
	mux.HandleFunc("GET /api/v1/monitoring/zabbix", s.zabbixSnapshot)
	mux.HandleFunc("GET /api/v1/audit/events", s.listAuditEvents)
	mux.HandleFunc("GET /api/v1/admin/audit-archives", s.listAuditArchives)
	mux.HandleFunc("POST /api/v1/admin/audit-archives", s.createAuditArchive)
	mux.HandleFunc("GET /api/v1/notifications", s.listNotifications)
	mux.HandleFunc("POST /api/v1/notifications/{notification_id}/read", s.readNotification)
	mux.HandleFunc("GET /api/v1/admin/security-incidents", s.listSecurityIncidents)
	mux.HandleFunc("POST /api/v1/admin/security-incidents/{incident_id}/ack", s.ackSecurityIncident)
	mux.HandleFunc("POST /api/v1/admin/security-incidents/{incident_id}/close", s.closeSecurityIncident)
	mux.HandleFunc("GET /api/v1/admin/outbound-integrations", s.listOutboundIntegrations)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/proxies", s.createOutboundProxy)
	mux.HandleFunc("PATCH /api/v1/admin/outbound-integrations/{integration_id}/proxy", s.updateOutboundProxy)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/{integration_id}/revisions", s.createOutboundProxyRevision)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/wecom-bots", s.createWeComBot)
	mux.HandleFunc("PATCH /api/v1/admin/outbound-integrations/{integration_id}/wecom-bot", s.updateWeComBot)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/smtp", s.createSMTP)
	mux.HandleFunc("DELETE /api/v1/admin/outbound-integrations/{integration_id}", s.deleteOutboundIntegration)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/{integration_id}/publish", s.publishOutboundIntegration)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/{integration_id}/test-message", s.testWeComBot)
	mux.HandleFunc("POST /api/v1/admin/outbound-integrations/{integration_id}/test-email", s.testSMTP)
	mux.HandleFunc("POST /api/v1/admin/ai-integrations", s.createAIIntegration)
	mux.HandleFunc("GET /api/v1/admin/ai-integrations/{integration_id}", s.getAIIntegration)
	mux.HandleFunc("PATCH /api/v1/admin/ai-integrations/{integration_id}", s.updateAIIntegration)
	mux.HandleFunc("POST /api/v1/admin/ai-integrations/{integration_id}/test", s.testAIIntegration)
	mux.HandleFunc("POST /api/v1/requests/{request_id}/ai-suggestions", s.aiSuggestion)
	mux.HandleFunc("GET /api/v1/admin/recovery-guard", s.recoveryState)
	mux.HandleFunc("POST /api/v1/admin/guards/release-freeze", s.releaseRecoveryFreeze)
	mux.HandleFunc("GET /api/v1/admin/exchange-channels", s.listExchangeChannels)
	mux.HandleFunc("PUT /api/v1/admin/exchange-channels/{direction}", s.publishExchangeChannel)
	mux.HandleFunc("GET /api/v1/admin/transfers", s.listTransfers)
	mux.HandleFunc("POST /api/v1/admin/transfers/{transfer_id}/retry", s.retryTransfer)
	mux.HandleFunc("GET /api/v1/downloads/available", s.listAvailableDownloads)
	mux.HandleFunc("POST /api/v1/files/{file_id}/download-grants", s.issueDownloadGrant)
	mux.HandleFunc("POST /data/v1/downloads", s.streamDownload)
	mux.HandleFunc("GET /api/v1/requests/{request_id}/downloads", s.listDownloadReceipts)
	mux.HandleFunc("POST /api/v1/requests/{request_id}/revoke", s.revokeReleasedRequest)
	mux.HandleFunc("GET /api/v1/requests", s.listRequests)
	mux.HandleFunc("GET /api/v1/requests/{request_id}/progress", s.requestProgress)
	mux.HandleFunc("GET /api/v1/bootstrap", s.requestBootstrap)
	mux.HandleFunc("GET /api/v1/requests/readiness", s.requestReadiness)
	mux.HandleFunc("POST /api/v1/requests", s.createRequest)
	mux.HandleFunc("PATCH /api/v1/requests/{request_id}/purpose", s.updateDraftPurpose)
	mux.HandleFunc("POST /api/v1/requests/{request_id}/upload-sessions", s.createUploadSession)
	mux.HandleFunc("PUT /data/v1/upload-sessions/{upload_id}/content", s.uploadContent)
	mux.HandleFunc("POST /api/v1/upload-sessions/{upload_id}/abort", s.abortUpload)
	mux.HandleFunc("POST /api/v1/requests/{request_id}/submit", s.submitRequest)
	mux.HandleFunc("GET /api/v1/requests/{request_id}/checks", s.listRequestChecks)
	mux.HandleFunc("POST /api/v1/requests/{request_id}/cancel", s.cancelRequest)
	mux.HandleFunc("GET /api/v1/admin/workflows", s.listWorkflows)
	mux.HandleFunc("POST /api/v1/admin/workflows", s.createWorkflow)
	mux.HandleFunc("POST /api/v1/admin/workflows/{workflow_id}/versions", s.createWorkflowRevision)
	mux.HandleFunc("GET /api/v1/admin/workflow-versions/{version_id}", s.getWorkflowVersion)
	mux.HandleFunc("PATCH /api/v1/admin/workflow-versions/{version_id}", s.updateWorkflowDraft)
	mux.HandleFunc("POST /api/v1/admin/workflow-versions/{version_id}/validate", s.validateWorkflow)
	mux.HandleFunc("POST /api/v1/admin/workflow-versions/{version_id}/simulate", s.simulateWorkflow)
	mux.HandleFunc("POST /api/v1/admin/workflow-versions/{version_id}/publish", s.publishWorkflow)
	mux.HandleFunc("PUT /api/v1/admin/workflow-bindings", s.bindWorkflow)
	mux.HandleFunc("GET /api/v1/admin/workflow-bindings", s.listWorkflowBindings)
	mux.HandleFunc("GET /api/v1/approval-tasks", s.listApprovalTasks)
	mux.HandleFunc("POST /api/v1/approval-tasks/{task_id}/decision", s.decideTask)
	mux.HandleFunc("POST /api/v1/admin/workflow-tasks/{task_id}/act-on-behalf", s.adminDecideTask)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusNotFound, "NOT_FOUND", "接口不存在")
	})
	mux.Handle("/", spaHandler(s.assets))
	return recoverMiddleware(securityHeaders(s.recoveryGuard(s.originGuard(mux))))
}

func (s *Server) StartWorkers(ctx context.Context) {
	go s.monitoring.Run(ctx)
	if s.secretStore != nil && s.integrations != nil {
		go s.audit.Run(ctx, s.integrations, s.secretStore)
	}
	if s.scanning != nil {
		go s.scanning.Run(ctx)
	}
	if s.transfer != nil {
		go s.transfer.Run(ctx)
	}
	if s.downloads != nil {
		go s.downloads.Run(ctx)
	}
}

type passwordLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type totpLoginRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}
type activateRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}
type confirmMFARequest struct {
	BindingToken string `json:"binding_token"`
	Code         string `json:"code"`
}
type recoveryRequest struct {
	UserID string `json:"user_id"`
	Note   string `json:"identity_verification_note"`
}
type approveRecoveryRequest struct {
	Note            string `json:"review_note"`
	ExpectedVersion uint64 `json:"expected_version"`
}
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) passwordLogin(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份加密主密钥尚未配置")
		return
	}
	var input passwordLoginRequest
	if err := s.decodeJSON(w, r, &input); err != nil {
		return
	}
	zone := s.portalZone(r)
	if zone == "UNKNOWN" {
		problem(w, http.StatusForbidden, "UNTRUSTED_PORTAL", "无法确认当前入口安全域")
		return
	}
	challenge, expires, err := s.identity.BeginPassword(r.Context(), input.Username, input.Password, zone, []byte(r.UserAgent()), clientIP(r))
	if err != nil {
		if errors.Is(err, identity.ErrRateLimited) {
			w.Header().Set("Retry-After", "900")
			problem(w, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "尝试次数过多，请稍后重试")
			return
		}
		if errors.Is(err, identity.ErrInvalidCredentials) {
			problem(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "账号、密码或验证码错误")
			return
		}
		problem(w, http.StatusInternalServerError, "IDENTITY_ERROR", "身份服务暂时不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge": challenge, "expires_at": expires})
}

func (s *Server) totpLogin(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份加密主密钥尚未配置")
		return
	}
	var input totpLoginRequest
	if err := s.decodeJSON(w, r, &input); err != nil {
		return
	}
	zone := s.portalZone(r)
	if zone == "UNKNOWN" {
		problem(w, http.StatusForbidden, "UNTRUSTED_PORTAL", "无法确认当前入口安全域")
		return
	}
	session, err := s.identity.CompleteTOTP(r.Context(), input.Challenge, input.Code, zone, []byte(r.UserAgent()), clientIP(r))
	if err != nil {
		if errors.Is(err, identity.ErrRateLimited) {
			w.Header().Set("Retry-After", "900")
			problem(w, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "尝试次数过多，请稍后重试")
			return
		}
		if errors.Is(err, identity.ErrInvalidCredentials) || errors.Is(err, identity.ErrChallengeExpired) || errors.Is(err, identity.ErrTOTPReplay) {
			problem(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "账号、密码或验证码错误")
			return
		}
		problem(w, http.StatusInternalServerError, "IDENTITY_ERROR", "身份服务暂时不可用")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: session.Token, Path: "/", HttpOnly: true, Secure: s.cfg.Security.CookieSecure, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: int(time.Until(session.ExpiresAt).Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]string{"id": session.UserID, "display_name": session.DisplayName}, "expires_at": session.ExpiresAt})
}

func (s *Server) activate(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份加密主密钥尚未配置")
		return
	}
	var input activateRequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	binding, err := s.identity.Activate(r.Context(), input.Token, input.Password)
	if err != nil {
		problem(w, http.StatusBadRequest, "ACTIVATION_FAILED", "激活凭据无效、过期或密码不符合要求")
		return
	}
	s.writeBinding(w, binding)
}

func (s *Server) rebindMFA(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份加密主密钥尚未配置")
		return
	}
	var input activateRequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	binding, err := s.identity.BeginRebind(r.Context(), input.Token, input.Password)
	if err != nil {
		problem(w, http.StatusBadRequest, "MFA_REBIND_FAILED", "重绑凭据无效、过期或密码错误")
		return
	}
	s.writeBinding(w, binding)
}

func (s *Server) writeBinding(w http.ResponseWriter, binding identity.Binding) {
	png, err := qrcode.Encode(binding.URI, qrcode.Medium, 256)
	if err != nil {
		problem(w, http.StatusInternalServerError, "QR_GENERATION_FAILED", "无法生成绑定二维码")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"binding_token": binding.Token, "secret": binding.Secret, "otpauth_uri": binding.URI, "qr_data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "expires_at": binding.ExpiresAt})
}

func (s *Server) confirmMFA(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份加密主密钥尚未配置")
		return
	}
	var input confirmMFARequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	if err := s.identity.ConfirmBinding(r.Context(), input.BindingToken, input.Code); err != nil {
		problem(w, http.StatusBadRequest, "MFA_CONFIRMATION_FAILED", "动态验证码无效或绑定流程已过期")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份服务尚未配置")
		return
	}
	cookie, err := r.Cookie(s.sessionCookieName())
	if err != nil {
		problem(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "请先登录")
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie.Value, s.portalZone(r))
	if err != nil {
		problem(w, http.StatusUnauthorized, "SESSION_INVALID", "会话已失效，请重新登录")
		return
	}
	permissions, err := s.authz.Permissions(r.Context(), user.ID)
	if err != nil {
		problem(w, http.StatusInternalServerError, "AUTHORIZATION_ERROR", "无法读取用户权限")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "username": user.Username, "display_name": user.DisplayName, "avatar_emoji": user.AvatarEmoji, "portal_zone": user.PortalZone, "permissions": permissions})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.sessionCookieName()); err == nil && s.identity != nil {
		_ = s.identity.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Security.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份服务尚未配置")
		return
	}
	cookie, err := r.Cookie(s.sessionCookieName())
	if err != nil {
		problem(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "请先登录")
		return
	}
	if err = s.identity.LogoutAll(r.Context(), cookie.Value, s.portalZone(r)); err != nil {
		problem(w, http.StatusUnauthorized, "SESSION_INVALID", "会话已失效，请重新登录")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Security.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	var input changePasswordRequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	if err := s.identity.ChangePassword(r.Context(), cookie, s.portalZone(r), input.CurrentPassword, input.NewPassword); err != nil {
		problem(w, http.StatusBadRequest, "PASSWORD_CHANGE_FAILED", "当前密码错误、新密码不符合策略或需要重新登录")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Security.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) initiateMFARecovery(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	var input recoveryRequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	result, err := s.identity.InitiateRecovery(r.Context(), cookie, s.portalZone(r), input.UserID, input.Note)
	if err != nil {
		problem(w, http.StatusForbidden, "MFA_RECOVERY_DENIED", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) approveMFARecovery(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	var input approveRecoveryRequest
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	token, expires, err := s.identity.ApproveRecovery(r.Context(), cookie, s.portalZone(r), r.PathValue("recovery_id"), input.Note, input.ExpectedVersion)
	if err != nil {
		problem(w, http.StatusForbidden, "MFA_RECOVERY_DENIED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rebind_token": token, "expires_at": expires})
}

func (s *Server) requireSessionCookie(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.identity == nil {
		problem(w, http.StatusServiceUnavailable, "IDENTITY_NOT_CONFIGURED", "身份服务尚未配置")
		return "", false
	}
	cookie, err := r.Cookie(s.sessionCookieName())
	if err != nil {
		problem(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "请先登录")
		return "", false
	}
	return cookie.Value, true
}

func (s *Server) sessionCookieName() string {
	if s.cfg.Security.CookieSecure {
		return "__Host-yundu-session"
	}
	return "yundu-session"
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Limits.JSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		problem(w, http.StatusBadRequest, "INVALID_JSON", "请求内容无效")
		return err
	}
	return nil
}

func (s *Server) portalZone(r *http.Request) string {
	if s.cfg.Environment == "production" {
		if !ipInCIDRs(clientIP(r), s.cfg.Security.TrustPortalHeaderFrom) {
			return "UNKNOWN"
		}
		zone := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Yundu-Portal-Zone")))
		for _, portal := range s.cfg.Portals {
			if portal.Zone == zone {
				return zone
			}
		}
		return "UNKNOWN"
	}
	host := strings.Split(r.Host, ":")[0]
	for name, portal := range s.cfg.Portals {
		if strings.HasPrefix(host, name+".") {
			return portal.Zone
		}
	}
	return "UNKNOWN"
}

func ipInCIDRs(value string, cidrs []string) bool {
	ip := net.ParseIP(value)
	if ip == nil {
		return false
	}
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(raw)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// The native browser download form is protected by a short-lived,
		// single-use, session-bound and portal-bound grant. Some browsers and
		// privacy products rewrite or remove Origin on navigation form posts, so
		// this one capability endpoint relies on its stronger grant validation.
		// Grant issuance remains covered by the normal Origin policy.
		if r.Method == http.MethodPost && r.URL.Path == "/data/v1/downloads" {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" && s.cfg.Environment != "production" {
			next.ServeHTTP(w, r)
			return
		}
		zone := s.portalZone(r)
		allowed := false
		for _, portal := range s.cfg.Portals {
			if portal.Zone == zone && (sameOrigin(portal.Origin, origin) || sameOriginNavigation(portal.Origin, r)) {
				allowed = true
				break
			}
		}
		if !allowed {
			problem(w, http.StatusForbidden, "ORIGIN_DENIED", "请求来源不在当前门户允许列表")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginNavigation(configured string, r *http.Request) bool {
	// Native form submissions are allowed to omit Origin (or serialize it as
	// "null") in some browser/privacy configurations. Sec-Fetch-Site is a
	// forbidden browser header, and the Referer independently anchors the
	// navigation to the configured portal.
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin") {
		return false
	}
	referer, err := url.Parse(strings.TrimSpace(r.Referer()))
	if err != nil || referer.Scheme == "" || referer.Host == "" {
		return false
	}
	referer.Path, referer.RawPath, referer.RawQuery, referer.Fragment = "", "", "", ""
	return sameOrigin(configured, referer.String())
}

func sameOrigin(configured, received string) bool {
	a, errA := url.Parse(strings.TrimSpace(configured))
	b, errB := url.Parse(strings.TrimSpace(received))
	if errA != nil || errB != nil || a.User != nil || b.User != nil || a.RawQuery != "" || b.RawQuery != "" || a.Fragment != "" || b.Fragment != "" {
		return false
	}
	if (a.Path != "" && a.Path != "/") || (b.Path != "" && b.Path != "/") {
		return false
	}
	port := func(v *url.URL) string {
		if value := v.Port(); value != "" {
			return value
		}
		if strings.EqualFold(v.Scheme, "http") {
			return "80"
		}
		if strings.EqualFold(v.Scheme, "https") {
			return "443"
		}
		return ""
	}
	host := func(v *url.URL) string { return strings.TrimSuffix(strings.ToLower(v.Hostname()), ".") }
	return (strings.EqualFold(a.Scheme, "http") || strings.EqualFold(a.Scheme, "https")) &&
		strings.EqualFold(a.Scheme, b.Scheme) && host(a) != "" && host(a) == host(b) && port(a) == port(b)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "alive", "service": "yundu-web", "version": s.version})
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		problem(w, http.StatusServiceUnavailable, "SHUTTING_DOWN", "服务正在停止")
		return
	}
	if err := store.Ready(r.Context(), s.db); err != nil {
		problem(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "数据服务不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "checks": map[string]string{"mysql": "ok"}})
}

func (s *Server) runtime(w http.ResponseWriter, r *http.Request) {
	zone := s.portalZone(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"product_name": "云渡文件交换平台", "product_short_name": "云渡",
		"product_tagline": "生产与办公网络文件交换及审批", "portal_zone": zone,
		"version": s.version,
		"limits":  map[string]any{"office_file_bytes": s.cfg.Limits.OfficeFileBytes, "production_file_bytes": s.cfg.Limits.ProductionFileBytes, "files_per_request": s.cfg.Limits.FilesPerRequest, "office_request_bytes": s.cfg.Limits.OfficeRequestBytes},
	})
}

func spaHandler(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			problem(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不允许")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			files.ServeHTTP(w, r2)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				problem(w, http.StatusInternalServerError, "INTERNAL_ERROR", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail, "timestamp": time.Now().UTC()})
}
