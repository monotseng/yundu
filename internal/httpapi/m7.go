package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"yundu/internal/downloads"
	"yundu/internal/idempotency"
)

type grantInput struct {
	ExpectedVersion uint64 `json:"expected_version"`
}
type revokeInput struct {
	Reason          string `json:"reason"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (s *Server) listAvailableDownloads(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	items, err := s.downloads.ListAvailable(r.Context(), user.ID, s.portalZone(r))
	if err != nil {
		problem(w, 500, "DOWNLOAD_LIST_FAILED", "无法读取待领取文件")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) issueDownloadGrant(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	zone := s.portalZone(r)
	user, err := s.identity.Authenticate(r.Context(), cookie, zone)
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "领取授权必须提供 Idempotency-Key")
		return
	}
	var input grantInput
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	grant, err := s.downloads.Issue(r.Context(), user.ID, cookie, zone, r.PathValue("file_id"), key, input.ExpectedVersion)
	if err != nil {
		if errors.Is(err, downloads.ErrBusy) {
			w.Header().Set("Retry-After", "3")
			problem(w, 429, "RESOURCE_BUSY", "下载并发已满，请稍后重试")
		} else if errors.Is(err, downloads.ErrIdempotency) {
			problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键无效、载荷冲突或绑定了其他会话")
		} else {
			problem(w, 403, "DOWNLOAD_DENIED", "当前文件不可领取")
		}
		return
	}
	response, _ := json.Marshal(grant)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(response)
}

func (s *Server) streamDownload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Range") != "" {
		w.Header().Set("Content-Range", "bytes */*")
		problem(w, http.StatusRequestedRangeNotSatisfiable, "RANGE_NOT_SUPPORTED", "当前版本不支持断点下载，请重新领取完整文件")
		return
	}
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	zone := s.portalZone(r)
	user, err := s.identity.Authenticate(r.Context(), cookie, zone)
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	if err = r.ParseForm(); err != nil {
		problem(w, 400, "INVALID_GRANT", "领取授权无效")
		return
	}
	delivery, err := s.downloads.Start(r.Context(), user.ID, cookie, zone, r.PostFormValue("grant"))
	if err != nil {
		problem(w, 403, "DOWNLOAD_DENIED", "领取授权已失效、已使用或与当前会话不匹配")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmtInt(delivery.ExpectedBytes))
	w.Header().Set("Content-Disposition", contentDisposition(delivery.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = s.downloads.Stream(delivery, w)
}

func (s *Server) listDownloadReceipts(w http.ResponseWriter, r *http.Request) {
	cookie, ok := s.requireSessionCookie(w, r)
	if !ok {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), cookie, s.portalZone(r))
	if err != nil {
		problem(w, 401, "SESSION_INVALID", "会话已失效")
		return
	}
	items, err := s.downloads.ListReceipts(r.Context(), user.ID, r.PathValue("request_id"))
	if err != nil {
		problem(w, 403, "DOWNLOAD_HISTORY_DENIED", "无法读取领取记录")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) revokeReleasedRequest(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "request.revoke")
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "撤销必须提供 Idempotency-Key")
		return
	}
	var input revokeInput
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	payload, _ := json.Marshal(input)
	route := "POST:/api/v1/requests/" + r.PathValue("request_id") + "/revoke"
	claim, err := s.idempotency.Claim(r.Context(), actor, route, key, payload)
	if err != nil {
		if errors.Is(err, idempotency.ErrConflict) {
			problem(w, 409, "IDEMPOTENCY_CONFLICT", "幂等键冲突")
		} else {
			problem(w, 400, "INVALID_IDEMPOTENCY_KEY", "幂等键无效")
		}
		return
	}
	if claim.Replay {
		w.WriteHeader(claim.StatusCode)
		return
	}
	if err = s.downloads.RevokeRequest(r.Context(), actor, r.PathValue("request_id"), input.Reason, input.ExpectedVersion); err != nil {
		s.idempotency.Release(r.Context(), actor, route, key)
		problem(w, 409, "REVOKE_CONFLICT", "申请状态、版本或理由不允许撤销")
		return
	}
	_ = s.idempotency.Complete(r.Context(), actor, route, key, 204, []byte(`{}`))
	w.WriteHeader(204)
}

func contentDisposition(filename string) string {
	ext := filepath.Ext(filename)
	fallback := "download"
	if len(ext) <= 12 && strings.IndexFunc(ext, func(r rune) bool {
		return !(r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) < 0 {
		fallback += ext
	}
	encoded := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if strings.Contains(encoded, "filename*=") {
		return `attachment; filename="` + fallback + `"; ` + strings.TrimPrefix(encoded, "attachment; ")
	}
	return encoded
}
func fmtInt(v int64) string { return strings.TrimSpace(fmt.Sprintf("%d", v)) }
