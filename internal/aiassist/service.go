package aiassist

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"yundu/internal/secrets"
)

var (
	ErrDisabled = errors.New("AI assistance is disabled")
	ErrDenied   = errors.New("AI assistance is only available to the owner of an INTERNAL draft")
	ErrQuota    = errors.New("AI assistance quota exceeded")
	ErrBusy     = errors.New("AI assistance is busy")
)

type Config struct {
	BaseURL            string `json:"base_url"`
	Model              string `json:"model"`
	DailyPerUser       int    `json:"daily_per_user"`
	MonthlyTokenBudget uint64 `json:"monthly_token_budget"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	MaxInputChars      int    `json:"max_input_chars"`
	MaxOutputTokens    int    `json:"max_output_tokens"`
}
type Suggestion struct {
	Text             string  `json:"text"`
	Model            string  `json:"model"`
	UsageKnown       bool    `json:"usage_known"`
	PromptTokens     *uint64 `json:"prompt_tokens,omitempty"`
	CompletionTokens *uint64 `json:"completion_tokens,omitempty"`
	Notice           string  `json:"notice"`
}
type DraftDetail struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Version          uint64 `json:"version"`
	Config           Config `json:"config"`
	SecretConfigured bool   `json:"secret_configured"`
}
type Service struct {
	db      *sql.DB
	secrets *secrets.Store
	now     func() time.Time
}

func New(db *sql.DB, secrets *secrets.Store) *Service {
	return &Service{db: db, secrets: secrets, now: time.Now}
}
func randomID() []byte { v := make([]byte, 16); _, _ = rand.Read(v); return v }
func decode(v string) ([]byte, error) {
	b, e := hex.DecodeString(strings.TrimSpace(v))
	if e != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
func validate(c *Config) error {
	u, e := url.Parse(strings.TrimSpace(c.BaseURL))
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base_url must be a fixed HTTPS origin/path without credentials or query")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil || strings.EqualFold(u.Hostname(), "localhost") {
		return errors.New("literal and local AI hosts are not allowed")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("model is required")
	}
	if c.DailyPerUser == 0 {
		c.DailyPerUser = 20
	}
	if c.DailyPerUser < 1 || c.DailyPerUser > 20 {
		return errors.New("daily quota cannot exceed 20")
	}
	if c.MonthlyTokenBudget == 0 {
		return errors.New("monthly token budget is required")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 30
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 30 {
		return errors.New("timeout cannot exceed 30 seconds")
	}
	if c.MaxInputChars == 0 {
		c.MaxInputChars = 4000
	}
	if c.MaxInputChars < 1 || c.MaxInputChars > 4000 {
		return errors.New("input limit cannot exceed 4000 characters")
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = 1000
	}
	if c.MaxOutputTokens < 1 || c.MaxOutputTokens > 1000 {
		return errors.New("output limit cannot exceed 1000 tokens")
	}
	return nil
}
func (s *Service) CreateDraft(ctx context.Context, name string, c Config, apiKeySecret, userHex string) (string, error) {
	if err := validate(&c); err != nil {
		return "", err
	}
	if _, err := s.secrets.Get(ctx, apiKeySecret, "LLM_API_KEY"); err != nil {
		return "", errors.New("LLM API key unavailable")
	}
	user, e := decode(userHex)
	if e != nil {
		return "", e
	}
	secret, e := decode(apiKeySecret)
	if e != nil {
		return "", e
	}
	definition, version := randomID(), randomID()
	raw, _ := json.Marshal(c)
	digest := sha256.Sum256(raw)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT INTO integration_definitions(id,name,type,zone,status,created_by) VALUES(?,?,'LLM','SHARED','DRAFT',?)`, definition, strings.TrimSpace(name), user); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO integration_versions(id,integration_id,revision,config_json,config_sha256,status,created_by) VALUES(?,?,1,?,?,'DRAFT',?)`, version, definition, raw, digest[:], user); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO integration_version_secrets(integration_version_id,field_name,secret_id) VALUES(?,'api_key',?)`, version, secret); e != nil {
		return "", e
	}
	return hex.EncodeToString(definition), tx.Commit()
}
func (s *Service) GetDraft(ctx context.Context, definitionHex string) (DraftDetail, error) {
	definition, err := decode(definitionHex)
	if err != nil {
		return DraftDetail{}, err
	}
	var out DraftDetail
	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT LOWER(HEX(d.id)),d.name,d.status,d.version,v.config_json,EXISTS(SELECT 1 FROM integration_version_secrets s WHERE s.integration_version_id=v.id AND s.field_name='api_key') FROM integration_definitions d JOIN integration_versions v ON v.integration_id=d.id WHERE d.id=? AND d.type='LLM' AND d.status='DRAFT' AND v.status='DRAFT' ORDER BY v.revision DESC LIMIT 1`, definition).Scan(&out.ID, &out.Name, &out.Status, &out.Version, &raw, &out.SecretConfigured)
	if err != nil {
		return DraftDetail{}, err
	}
	if err = json.Unmarshal(raw, &out.Config); err != nil {
		return DraftDetail{}, err
	}
	return out, nil
}
func (s *Service) UpdateDraft(ctx context.Context, definitionHex, name string, c Config, apiKeySecret, userHex string, expected uint64) error {
	if err := validate(&c); err != nil {
		return err
	}
	definition, err := decode(definitionHex)
	if err != nil {
		return err
	}
	if _, err = decode(userHex); err != nil {
		return err
	}
	var newSecret []byte
	if strings.TrimSpace(apiKeySecret) != "" {
		if _, err = s.secrets.Get(ctx, apiKeySecret, "LLM_API_KEY"); err != nil {
			return errors.New("LLM API key unavailable")
		}
		newSecret, err = decode(apiKeySecret)
		if err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(c)
	digest := sha256.Sum256(raw)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var versionID []byte
	if err = tx.QueryRowContext(ctx, `SELECT v.id FROM integration_definitions d JOIN integration_versions v ON v.integration_id=d.id WHERE d.id=? AND d.type='LLM' AND d.status='DRAFT' AND d.version=? AND v.status='DRAFT' ORDER BY v.revision DESC LIMIT 1 FOR UPDATE`, definition, expected).Scan(&versionID); err != nil {
		return errors.New("integration draft version conflict")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE integration_definitions SET name=?,version=version+1 WHERE id=?`, strings.TrimSpace(name), definition); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE integration_versions SET config_json=?,config_sha256=? WHERE id=?`, raw, digest[:], versionID); err != nil {
		return err
	}
	if len(newSecret) > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE integration_version_secrets SET secret_id=? WHERE integration_version_id=? AND field_name='api_key'`, newSecret, versionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Service) Suggest(ctx context.Context, userHex, requestHex, input string) (out Suggestion, err error) {
	if !utf8.ValidString(input) || len([]rune(input)) < 1 || len([]rune(input)) > 4000 {
		return out, errors.New("explicit input must contain 1 to 4000 valid UTF-8 characters")
	}
	user, e := decode(userHex)
	if e != nil {
		return out, ErrDenied
	}
	request, e := decode(requestHex)
	if e != nil {
		return out, ErrDenied
	}
	var owner []byte
	var classification, status string
	if e = s.db.QueryRowContext(ctx, `SELECT requester_id,classification,status FROM exchange_requests WHERE id=?`, request).Scan(&owner, &classification, &status); e != nil || !bytes.Equal(owner, user) || classification != "INTERNAL" || status != "DRAFT" {
		return out, ErrDenied
	}
	var versionID, raw, secretID []byte
	e = s.db.QueryRowContext(ctx, `SELECT v.id,v.config_json,s.secret_id FROM integration_definitions d JOIN integration_versions v ON v.id=d.current_version_id JOIN integration_version_secrets s ON s.integration_version_id=v.id AND s.field_name='api_key' WHERE d.type='LLM' AND d.status='PUBLISHED' AND v.status='PUBLISHED' ORDER BY d.updated_at DESC LIMIT 1`).Scan(&versionID, &raw, &secretID)
	if e == sql.ErrNoRows {
		return out, ErrDisabled
	}
	if e != nil {
		return out, e
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil || validate(&c) != nil {
		return out, ErrDisabled
	}
	charged := uint64((len([]rune(input))+3)/4 + c.MaxOutputTokens)
	invocation := randomID()
	slot, err := s.reserve(ctx, user, request, versionID, invocation, c, charged, len([]rune(input)))
	if err != nil {
		return out, err
	}
	defer s.release(context.Background(), slot, invocation)
	apiKey, e := s.secrets.Get(ctx, hex.EncodeToString(secretID), "LLM_API_KEY")
	if e != nil {
		return out, e
	}
	started := s.now()
	text, prompt, completion, e := invoke(ctx, c, string(apiKey), input)
	latency := uint64(time.Since(started).Milliseconds())
	code := "OK"
	state := "SUCCEEDED"
	if e != nil {
		code = "PROVIDER_ERROR"
		state = "FAILED"
	}
	var outputChars any
	if e == nil {
		outputChars = len([]rune(text))
	}
	_, _ = s.db.ExecContext(context.Background(), `UPDATE ai_invocations SET status=?,result_code=?,output_chars=?,prompt_tokens=?,completion_tokens=?,latency_ms=?,finished_at=UTC_TIMESTAMP(6) WHERE id=?`, state, code, outputChars, prompt, completion, latency, invocation)
	if e != nil {
		return out, e
	}
	out = Suggestion{Text: text, Model: c.Model, UsageKnown: prompt != nil && completion != nil, PromptTokens: prompt, CompletionTokens: completion, Notice: "建议仅为不可信纯文本；预览并明确采用后才修改草稿，不能改变授权或状态。"}
	return out, nil
}
func (s *Service) reserve(ctx context.Context, user, request, version, invocation []byte, c Config, charged uint64, inputChars int) (int, error) {
	now := s.now().UTC()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `INSERT INTO ai_daily_usage(user_id,usage_date,invocation_count) VALUES(?,?,1) ON DUPLICATE KEY UPDATE invocation_count=invocation_count+1`, user, now.Format("2006-01-02"))
	if e != nil {
		return 0, e
	}
	var daily int
	if e = tx.QueryRowContext(ctx, `SELECT invocation_count FROM ai_daily_usage WHERE user_id=? AND usage_date=?`, user, now.Format("2006-01-02")).Scan(&daily); e != nil {
		return 0, e
	}
	if daily > c.DailyPerUser {
		return 0, ErrQuota
	}
	month := now.Format("2006-01")
	_, e = tx.ExecContext(ctx, `INSERT INTO ai_monthly_usage(usage_month,charged_tokens) VALUES(?,?) ON DUPLICATE KEY UPDATE charged_tokens=charged_tokens+VALUES(charged_tokens)`, month, charged)
	if e != nil {
		return 0, e
	}
	var total uint64
	if e = tx.QueryRowContext(ctx, `SELECT charged_tokens FROM ai_monthly_usage WHERE usage_month=?`, month).Scan(&total); e != nil {
		return 0, e
	}
	if total > c.MonthlyTokenBudget {
		return 0, ErrQuota
	}
	var slot int
	e = tx.QueryRowContext(ctx, `SELECT slot_no FROM ai_concurrency_slots WHERE holder_id IS NULL OR lease_expires_at<=? ORDER BY slot_no LIMIT 1 FOR UPDATE SKIP LOCKED`, now).Scan(&slot)
	if e == sql.ErrNoRows {
		return 0, ErrBusy
	}
	if e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE ai_concurrency_slots SET holder_id=?,lease_expires_at=?,fencing_token=fencing_token+1 WHERE slot_no=?`, invocation, now.Add(35*time.Second), slot); e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO ai_invocations(id,user_id,request_id,integration_version_id,status,input_chars,charged_tokens) VALUES(?,?,?,?,'PENDING',?,?)`, invocation, user, request, version, inputChars, charged); e != nil {
		return 0, e
	}
	return slot, tx.Commit()
}
func (s *Service) release(ctx context.Context, slot int, holder []byte) {
	_, _ = s.db.ExecContext(ctx, `UPDATE ai_concurrency_slots SET holder_id=NULL,lease_expires_at=NULL WHERE slot_no=? AND holder_id=?`, slot, holder)
}
func invoke(ctx context.Context, c Config, key, input string) (string, *uint64, *uint64, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	payload, _ := json.Marshal(map[string]any{"model": c.Model, "messages": []map[string]string{{"role": "system", "content": "仅将用户提供的申请用途整理为简洁、专业的中文纯文本。不得推断或更改密级、方向、人员、文件、期限、状态或授权；不得输出 HTML、链接或工具调用。"}, {"role": "user", "content": input}}, "max_tokens": c.MaxOutputTokens, "temperature": 0.2})
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if e != nil {
		return "", nil, nil, e
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: safeDialContext}
	client := &http.Client{Transport: transport, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return "", nil, nil, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		return "", nil, nil, e
	}
	if response.StatusCode/100 != 2 {
		return "", nil, nil, errors.New("AI provider rejected request")
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Prompt     uint64 `json:"prompt_tokens"`
			Completion uint64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &decoded) != nil || len(decoded.Choices) != 1 {
		return "", nil, nil, errors.New("invalid AI response")
	}
	text := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if !utf8.ValidString(text) || text == "" {
		return "", nil, nil, errors.New("invalid AI text")
	}
	runes := []rune(text)
	if len(runes) > 4000 {
		text = string(runes[:4000])
	}
	var prompt, completion *uint64
	if decoded.Usage != nil {
		prompt = &decoded.Usage.Prompt
		completion = &decoded.Usage.Completion
	}
	return text, prompt, completion, nil
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	resolved, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, e
	}
	for _, candidate := range resolved {
		if forbiddenIP(candidate.IP) {
			continue
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
	}
	return nil, fmt.Errorf("AI destination resolves only to forbidden addresses")
}
func forbiddenIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func (s *Service) TestDraft(ctx context.Context, definitionHex, userHex, input string) error {
	if strings.TrimSpace(input) == "" {
		return errors.New("explicit test text is required")
	}
	definition, e := decode(definitionHex)
	if e != nil {
		return e
	}
	user, e := decode(userHex)
	if e != nil {
		return e
	}
	var versionID, raw, secretID []byte
	e = s.db.QueryRowContext(ctx, `SELECT v.id,v.config_json,s.secret_id FROM integration_versions v JOIN integration_definitions d ON d.id=v.integration_id JOIN integration_version_secrets s ON s.integration_version_id=v.id AND s.field_name='api_key' WHERE d.id=? AND d.type='LLM' AND d.status='DRAFT' AND v.status='DRAFT' ORDER BY v.revision DESC LIMIT 1`, definition).Scan(&versionID, &raw, &secretID)
	if e != nil {
		return e
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil || validate(&c) != nil {
		return errors.New("invalid LLM configuration")
	}
	key, e := s.secrets.Get(ctx, hex.EncodeToString(secretID), "LLM_API_KEY")
	if e != nil {
		return e
	}
	_, _, _, callErr := invoke(ctx, c, string(key), input)
	status, code, detail := "PASSED", "", "fixed synthetic text response contract passed"
	if callErr != nil {
		status, code, detail = "FAILED", "PROVIDER_ERROR", "provider request failed"
	}
	testID := randomID()
	_, e = s.db.ExecContext(ctx, `INSERT INTO integration_test_runs(id,integration_version_id,status,stage,error_code,safe_detail,started_by,finished_at) VALUES(?,?,?,'CHAT_COMPLETIONS',?,?,?,UTC_TIMESTAMP(6))`, testID, versionID, status, nullable(code), detail, user)
	if e != nil {
		return e
	}
	return callErr
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
