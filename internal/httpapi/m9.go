package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"yundu/internal/aiassist"
	"yundu/internal/exchange"
)

type aiIntegrationInput struct {
	Name            string          `json:"name"`
	Config          aiassist.Config `json:"config"`
	APIKeySecretID  string          `json:"api_key_secret_id"`
	ExpectedVersion uint64          `json:"expected_version"`
}
type aiTextInput struct {
	Text string `json:"text"`
}
type updatePurposeInput struct {
	Purpose         string `json:"purpose"`
	ExpectedVersion uint64 `json:"expected_version"`
}
type releaseRecoveryInput struct {
	Reason          string `json:"reason"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (s *Server) recoveryState(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "security.manage"); !ok {
		return
	}
	v, e := s.recovery.State(r.Context())
	if e != nil {
		problem(w, 500, "RECOVERY_GUARD_ERROR", "无法读取恢复保护状态")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) releaseRecoveryFreeze(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorized(w, r, "security.manage")
	if !ok {
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
	var in releaseRecoveryInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e = s.recovery.Release(r.Context(), user, in.Reason, in.ExpectedVersion, session.MFAVerifiedAt); e != nil {
		problem(w, 409, "RECOVERY_RELEASE_DENIED", e.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) recoveryGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authPath := r.URL.Path == "/api/v1/auth/password" || r.URL.Path == "/api/v1/auth/totp"
		downloadPath := strings.HasPrefix(r.URL.Path, "/data/v1/downloads") || strings.HasSuffix(r.URL.Path, "/download-grants")
		if !authPath && !downloadPath {
			next.ServeHTTP(w, r)
			return
		}
		state, e := s.recovery.State(r.Context())
		if e != nil {
			problem(w, 503, "RECOVERY_GUARD_UNAVAILABLE", "恢复保护状态不可用")
			return
		}
		if authPath && state.AuthenticationBlockedUntil != nil && time.Now().UTC().Before(*state.AuthenticationBlockedUntil) {
			problem(w, 503, "AUTH_RECOVERY_HOLD", "数据库恢复后认证临时冻结")
			return
		}
		if downloadPath && state.Frozen {
			problem(w, 503, "RECOVERY_FREEZE", "数据库恢复核验完成前禁止文件领取")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) updateDraftPurpose(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	var in updatePurposeInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.exchange.UpdateDraftPurpose(r.Context(), user, r.PathValue("request_id"), in.Purpose, in.ExpectedVersion); e != nil {
		if errors.Is(e, exchange.ErrConflict) {
			problem(w, 409, "REQUEST_VERSION_CONFLICT", "草稿状态或版本已变化")
		} else {
			problem(w, 400, "INVALID_PURPOSE", "交换用途无效")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"version": in.ExpectedVersion + 1})
}

func (s *Server) createAIIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "ai.manage")
	if !ok {
		return
	}
	if s.aiassist == nil {
		problem(w, 503, "SECRET_STORE_UNAVAILABLE", "秘密存储不可用")
		return
	}
	var in aiIntegrationInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	id, e := s.aiassist.CreateDraft(r.Context(), in.Name, in.Config, in.APIKeySecretID, actor)
	if e != nil {
		problem(w, 400, "INVALID_AI_INTEGRATION", e.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": "DRAFT", "version": 1})
}
func (s *Server) getAIIntegration(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "ai.manage"); !ok {
		return
	}
	v, e := s.aiassist.GetDraft(r.Context(), r.PathValue("integration_id"))
	if e != nil {
		problem(w, 404, "AI_DRAFT_NOT_FOUND", "模型集成草稿不存在")
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (s *Server) updateAIIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "ai.manage")
	if !ok {
		return
	}
	var in aiIntegrationInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.aiassist.UpdateDraft(r.Context(), r.PathValue("integration_id"), in.Name, in.Config, in.APIKeySecretID, actor, in.ExpectedVersion); e != nil {
		problem(w, 409, "AI_DRAFT_UPDATE_FAILED", e.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) testAIIntegration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "ai.manage")
	if !ok {
		return
	}
	if s.aiassist == nil {
		problem(w, 503, "SECRET_STORE_UNAVAILABLE", "秘密存储不可用")
		return
	}
	var in aiTextInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	if e := s.aiassist.TestDraft(r.Context(), r.PathValue("integration_id"), actor, in.Text); e != nil {
		problem(w, 502, "AI_TEST_FAILED", "模型契约测试失败")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "PASSED", "note": "仅验证固定合成文字契约，未发送申请或附件内容"})
}
func (s *Server) aiSuggestion(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requestUser(w, r, "request.create")
	if !ok {
		return
	}
	if s.aiassist == nil {
		problem(w, 404, "AI_DISABLED", "AI 辅助未启用")
		return
	}
	var in aiTextInput
	if s.decodeJSON(w, r, &in) != nil {
		return
	}
	suggestion, e := s.aiassist.Suggest(r.Context(), user, r.PathValue("request_id"), in.Text)
	if e != nil {
		switch {
		case errors.Is(e, aiassist.ErrDisabled):
			problem(w, 404, "AI_DISABLED", "AI 辅助未启用")
		case errors.Is(e, aiassist.ErrDenied):
			problem(w, 403, "AI_NOT_ALLOWED", "仅本人 INTERNAL 草稿可使用 AI 辅助")
		case errors.Is(e, aiassist.ErrQuota):
			problem(w, 429, "AI_QUOTA_EXCEEDED", "AI 使用配额已用尽")
		case errors.Is(e, aiassist.ErrBusy):
			problem(w, 429, "AI_BUSY", "AI 并发已满，请稍后重试")
		default:
			problem(w, 502, "AI_PROVIDER_ERROR", "AI 服务调用失败，可继续手工填写")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, suggestion)
}
