package httpapi

import (
	"errors"
	"net/http"

	"yundu/internal/idempotency"
)

type channelInput struct {
	SourceStorageVersionID string `json:"source_storage_version_id"`
	TargetStorageVersionID string `json:"target_storage_version_id"`
	AntivirusEnabled       bool   `json:"antivirus_enabled"`
	ExpectedVersion        uint64 `json:"expected_version"`
}

func (s *Server) listExchangeChannels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "integration.read"); !ok {
		return
	}
	items, err := s.integrations.ListChannels(r.Context())
	if err != nil {
		problem(w, 500, "CHANNEL_READ_FAILED", "无法读取交换渠道")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) publishExchangeChannel(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "integration.publish")
	if !ok {
		return
	}
	var input channelInput
	if s.decodeJSON(w, r, &input) != nil {
		return
	}
	if err := s.integrations.PublishChannel(r.Context(), r.PathValue("direction"), input.SourceStorageVersionID, input.TargetStorageVersionID, input.AntivirusEnabled, input.ExpectedVersion, actor); err != nil {
		problem(w, http.StatusConflict, "CHANNEL_PUBLISH_CONFLICT", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(w, r, "transfers.read"); !ok {
		return
	}
	items, err := s.transfer.List(r.Context())
	if err != nil {
		problem(w, 500, "TRANSFER_READ_FAILED", "无法读取传输任务")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) retryTransfer(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.authorized(w, r, "transfers.retry")
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED", "重试操作必须提供 Idempotency-Key")
		return
	}
	route := "POST:/api/v1/admin/transfers/" + r.PathValue("transfer_id") + "/retry"
	claim, err := s.idempotency.Claim(r.Context(), actor, route, key, []byte(`{}`))
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
	if err := s.transfer.Retry(r.Context(), r.PathValue("transfer_id")); err != nil {
		s.idempotency.Release(r.Context(), actor, route, key)
		problem(w, 409, "TRANSFER_NOT_RETRYABLE", err.Error())
		return
	}
	_ = s.idempotency.Complete(r.Context(), actor, route, key, 204, []byte(`{}`))
	w.WriteHeader(http.StatusNoContent)
}
