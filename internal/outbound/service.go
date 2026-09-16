package outbound

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"yundu/internal/secrets"
)

type Service struct {
	db      *sql.DB
	secrets *secrets.Store
}

func New(db *sql.DB, secrets *secrets.Store) *Service { return &Service{db: db, secrets: secrets} }

type Definition struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	Status           string          `json:"status"`
	Config           json.RawMessage `json:"config"`
	SecretConfigured bool            `json:"secret_configured"`
	Version          uint64          `json:"version"`
	UpdatedAt        time.Time       `json:"updated_at"`
}
type ProxyConfig struct {
	URL                   string   `json:"proxy_url"`
	Username              string   `json:"username,omitempty"`
	AllowedHosts          []string `json:"allowed_hosts"`
	AllowedPorts          []int    `json:"allowed_ports"`
	ConnectTimeoutSeconds int      `json:"connect_timeout_seconds"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds"`
}
type WeComConfig struct {
	ProxyID         string `json:"proxy_id"`
	EndpointSummary string `json:"endpoint_summary"`
}
type SMTPConfig struct {
	Host                    string   `json:"host"`
	Port                    int      `json:"port"`
	Username                string   `json:"username,omitempty"`
	From                    string   `json:"from"`
	AllowedRecipientDomains []string `json:"allowed_recipient_domains"`
	TLSMode                 string   `json:"tls_mode"`
}

func id() []byte { v := make([]byte, 16); _, _ = rand.Read(v); return v }
func decode(v string) ([]byte, error) {
	b, e := hex.DecodeString(strings.TrimSpace(v))
	if e != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
func (s *Service) List(ctx context.Context) ([]Definition, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT LOWER(HEX(id)),name,type,status,config_json,secret_id IS NOT NULL,version,updated_at FROM outbound_integrations WHERE status<>'DISABLED' ORDER BY updated_at DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var v Definition
		if e = rows.Scan(&v.ID, &v.Name, &v.Type, &v.Status, &v.Config, &v.SecretConfigured, &v.Version, &v.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) CreateProxy(ctx context.Context, name string, c ProxyConfig, passwordSecret, actorHex string) (Definition, error) {
	name = strings.TrimSpace(name)
	u, e := url.Parse(c.URL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return Definition{}, errors.New("proxy URL must be http(s), include a host, and contain no embedded credentials")
	}
	if len(c.AllowedHosts) == 0 || len(c.AllowedPorts) == 0 {
		return Definition{}, errors.New("fixed destination allowlist is required")
	}
	for _, h := range c.AllowedHosts {
		if strings.ToLower(strings.TrimSpace(h)) != "qyapi.weixin.qq.com" {
			return Definition{}, errors.New("V1.0 proxy destination is restricted to qyapi.weixin.qq.com")
		}
	}
	if c.ConnectTimeoutSeconds == 0 {
		c.ConnectTimeoutSeconds = 5
	}
	if c.RequestTimeoutSeconds == 0 {
		c.RequestTimeoutSeconds = 10
	}
	actor, e := decode(actorHex)
	if e != nil {
		return Definition{}, e
	}
	var secret []byte
	if passwordSecret != "" {
		if _, e = s.secrets.Get(ctx, passwordSecret, "PROXY_PASSWORD"); e != nil {
			return Definition{}, errors.New("proxy password secret unavailable")
		}
		secret, e = decode(passwordSecret)
		if e != nil {
			return Definition{}, e
		}
	}
	raw, _ := json.Marshal(c)
	identifier := id()
	_, e = s.db.ExecContext(ctx, `INSERT INTO outbound_integrations(id,name,type,status,config_json,secret_id,created_by,updated_by) VALUES(?,?,'HTTP_CONNECT_PROXY','DRAFT',?,?,?,?)`, identifier, name, raw, secret, actor, actor)
	return Definition{ID: hex.EncodeToString(identifier), Name: name, Type: "HTTP_CONNECT_PROXY", Status: "DRAFT", Config: raw, SecretConfigured: len(secret) > 0, Version: 1}, e
}
func (s *Service) CreateWeCom(ctx context.Context, name string, c WeComConfig, webhookSecret, actorHex string) (Definition, error) {
	if strings.TrimSpace(c.ProxyID) == "" {
		return Definition{}, errors.New("published HTTP CONNECT proxy is required; no direct fallback is used")
	}
	proxy, e := decode(c.ProxyID)
	if e != nil {
		return Definition{}, e
	}
	var n int
	if e = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbound_integrations WHERE id=? AND type='HTTP_CONNECT_PROXY' AND status='PUBLISHED'`, proxy).Scan(&n); e != nil || n != 1 {
		return Definition{}, errors.New("published proxy not found")
	}
	if _, e = s.secrets.Get(ctx, webhookSecret, "WECOM_WEBHOOK"); e != nil {
		return Definition{}, errors.New("webhook secret unavailable")
	}
	secret, e := decode(webhookSecret)
	if e != nil {
		return Definition{}, e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return Definition{}, e
	}
	c.EndpointSummary = "qyapi.weixin.qq.com/cgi-bin/webhook/send"
	raw, _ := json.Marshal(c)
	identifier := id()
	_, e = s.db.ExecContext(ctx, `INSERT INTO outbound_integrations(id,name,type,status,config_json,secret_id,created_by,updated_by) VALUES(?,?,'WECOM_GROUP_BOT','DRAFT',?,?,?,?)`, identifier, strings.TrimSpace(name), raw, secret, actor, actor)
	return Definition{ID: hex.EncodeToString(identifier), Name: name, Type: "WECOM_GROUP_BOT", Status: "DRAFT", Config: raw, SecretConfigured: true, Version: 1}, e
}
func (s *Service) Publish(ctx context.Context, idHex string, version uint64, actorHex string) error {
	id, e := decode(idHex)
	if e != nil {
		return e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return e
	}
	res, e := s.db.ExecContext(ctx, `UPDATE outbound_integrations SET status='PUBLISHED',version=version+1,updated_by=? WHERE id=? AND status='DRAFT' AND version=?`, actor, id, version)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("integration version conflict")
	}
	return nil
}
func (s *Service) UpdateProxy(ctx context.Context, idHex, name string, c ProxyConfig, passwordSecret, actorHex string, version uint64) error {
	u, e := url.Parse(c.URL)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("invalid proxy URL")
	}
	if len(c.AllowedHosts) != 1 || strings.ToLower(strings.TrimSpace(c.AllowedHosts[0])) != "qyapi.weixin.qq.com" {
		return errors.New("proxy destination is restricted")
	}
	idBytes, e := decode(idHex)
	if e != nil {
		return e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return e
	}
	var secret any
	if passwordSecret != "" {
		if _, e = s.secrets.Get(ctx, passwordSecret, "PROXY_PASSWORD"); e != nil {
			return errors.New("proxy password secret unavailable")
		}
		secret, e = decode(passwordSecret)
		if e != nil {
			return e
		}
	}
	raw, _ := json.Marshal(c)
	res, e := s.db.ExecContext(ctx, `UPDATE outbound_integrations SET name=?,config_json=?,secret_id=COALESCE(?,secret_id),version=version+1,updated_by=? WHERE id=? AND type='HTTP_CONNECT_PROXY' AND status='DRAFT' AND version=?`, strings.TrimSpace(name), raw, secret, actor, idBytes, version)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("integration draft version conflict")
	}
	return nil
}

func (s *Service) CreateProxyRevision(ctx context.Context, idHex, actorHex string) (Definition, error) {
	idBytes, e := decode(idHex)
	if e != nil {
		return Definition{}, e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return Definition{}, e
	}
	var name string
	var config json.RawMessage
	var secret []byte
	if e = s.db.QueryRowContext(ctx, `SELECT name,config_json,secret_id FROM outbound_integrations WHERE id=? AND type='HTTP_CONNECT_PROXY' AND status='PUBLISHED'`, idBytes).Scan(&name, &config, &secret); e != nil {
		return Definition{}, errors.New("published proxy not found")
	}
	identifier := id()
	revisionName := name + "（修订）"
	if _, e = s.db.ExecContext(ctx, `INSERT INTO outbound_integrations(id,name,type,status,config_json,secret_id,created_by,updated_by) VALUES(?,?,'HTTP_CONNECT_PROXY','DRAFT',?,?,?,?)`, identifier, revisionName, config, secret, actor, actor); e != nil {
		return Definition{}, e
	}
	return Definition{ID: hex.EncodeToString(identifier), Name: revisionName, Type: "HTTP_CONNECT_PROXY", Status: "DRAFT", Config: config, SecretConfigured: len(secret) > 0, Version: 1, UpdatedAt: time.Now().UTC()}, nil
}
func (s *Service) UpdateWeCom(ctx context.Context, idHex, name string, c WeComConfig, webhookSecret, actorHex string, version uint64) error {
	proxy, e := decode(c.ProxyID)
	if e != nil {
		return e
	}
	var n int
	if e = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbound_integrations WHERE id=? AND type='HTTP_CONNECT_PROXY' AND status='PUBLISHED'`, proxy).Scan(&n); e != nil || n != 1 {
		return errors.New("published proxy not found")
	}
	idBytes, e := decode(idHex)
	if e != nil {
		return e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return e
	}
	var secret any
	if webhookSecret != "" {
		if _, e = s.secrets.Get(ctx, webhookSecret, "WECOM_WEBHOOK"); e != nil {
			return errors.New("webhook secret unavailable")
		}
		secret, e = decode(webhookSecret)
		if e != nil {
			return e
		}
	}
	c.EndpointSummary = "qyapi.weixin.qq.com/cgi-bin/webhook/send"
	raw, _ := json.Marshal(c)
	res, e := s.db.ExecContext(ctx, `UPDATE outbound_integrations SET name=?,config_json=?,secret_id=COALESCE(?,secret_id),version=version+1,updated_by=? WHERE id=? AND type='WECOM_GROUP_BOT' AND status='DRAFT' AND version=?`, strings.TrimSpace(name), raw, secret, actor, idBytes, version)
	if e != nil {
		return e
	}
	changed, _ := res.RowsAffected()
	if changed != 1 {
		return errors.New("integration draft version conflict")
	}
	return nil
}
func (s *Service) Delete(ctx context.Context, idHex, actorHex string) error {
	idBytes, e := decode(idHex)
	if e != nil {
		return e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return e
	}
	var integrationType, status string
	if e = s.db.QueryRowContext(ctx, `SELECT type,status FROM outbound_integrations WHERE id=?`, idBytes).Scan(&integrationType, &status); e != nil {
		return errors.New("integration not found")
	}
	if status == "DRAFT" {
		_, e = s.db.ExecContext(ctx, `DELETE FROM outbound_integrations WHERE id=? AND status='DRAFT'`, idBytes)
		return e
	}
	if status != "PUBLISHED" {
		return errors.New("integration cannot be deleted")
	}
	if integrationType == "HTTP_CONNECT_PROXY" {
		var dependencies int
		if e = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbound_integrations WHERE type='WECOM_GROUP_BOT' AND status IN ('DRAFT','PUBLISHED') AND JSON_UNQUOTE(JSON_EXTRACT(config_json,'$.proxy_id'))=?`, strings.ToLower(idHex)).Scan(&dependencies); e != nil {
			return e
		}
		if dependencies > 0 {
			return errors.New("proxy is still referenced by a notification integration")
		}
	}
	res, e := s.db.ExecContext(ctx, `UPDATE outbound_integrations SET status='DISABLED',version=version+1,updated_by=? WHERE id=? AND status='PUBLISHED'`, actor, idBytes)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("integration version conflict")
	}
	return nil
}

type WeComResult struct {
	Status       string `json:"status"`
	ProviderCode int    `json:"provider_code"`
	Note         string `json:"note"`
}

func (s *Service) TestWeCom(ctx context.Context, idHex, content string, mentioned []string) (WeComResult, error) {
	id, e := decode(idHex)
	if e != nil {
		return WeComResult{}, e
	}
	var raw, secretID []byte
	var status string
	e = s.db.QueryRowContext(ctx, `SELECT config_json,secret_id,status FROM outbound_integrations WHERE id=? AND type='WECOM_GROUP_BOT'`, id).Scan(&raw, &secretID, &status)
	if e != nil || status != "PUBLISHED" {
		return WeComResult{}, errors.New("published WeCom integration not found")
	}
	var wc WeComConfig
	if json.Unmarshal(raw, &wc) != nil {
		return WeComResult{}, errors.New("invalid WeCom configuration")
	}
	webhook, e := s.secrets.Get(ctx, hex.EncodeToString(secretID), "WECOM_WEBHOOK")
	if e != nil {
		return WeComResult{}, e
	}
	target, e := url.Parse(string(webhook))
	if e != nil || target.Scheme != "https" || strings.ToLower(target.Hostname()) != "qyapi.weixin.qq.com" || target.Port() != "" || target.Path != "/cgi-bin/webhook/send" || target.Query().Get("key") == "" {
		return WeComResult{}, errors.New("invalid fixed WeCom webhook")
	}
	proxyID, e := decode(wc.ProxyID)
	if e != nil {
		return WeComResult{}, e
	}
	var proxyRaw, proxySecret []byte
	e = s.db.QueryRowContext(ctx, `SELECT config_json,secret_id FROM outbound_integrations WHERE id=? AND type='HTTP_CONNECT_PROXY' AND status='PUBLISHED'`, proxyID).Scan(&proxyRaw, &proxySecret)
	if e != nil {
		return WeComResult{}, errors.New("published proxy unavailable")
	}
	var pc ProxyConfig
	if json.Unmarshal(proxyRaw, &pc) != nil {
		return WeComResult{}, errors.New("invalid proxy configuration")
	}
	allowed := false
	for _, h := range pc.AllowedHosts {
		if strings.EqualFold(h, target.Hostname()) {
			for _, p := range pc.AllowedPorts {
				if p == 443 {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return WeComResult{}, errors.New("WeCom destination not allowed by proxy")
	}
	proxyURL, e := url.Parse(pc.URL)
	if e != nil {
		return WeComResult{}, e
	}
	if len(proxySecret) > 0 {
		password, e := s.secrets.Get(ctx, hex.EncodeToString(proxySecret), "PROXY_PASSWORD")
		if e != nil {
			return WeComResult{}, e
		}
		proxyURL.User = url.UserPassword(pc.Username, string(password))
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]any{"content": content, "mentioned_list": mentioned}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return WeComResult{Status: "UNKNOWN"}, e
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var result struct {
		ErrCode int `json:"errcode"`
	}
	if resp.StatusCode/100 != 2 || json.Unmarshal(body, &result) != nil || result.ErrCode != 0 {
		return WeComResult{Status: "FAILED", ProviderCode: result.ErrCode}, errors.New("provider rejected message")
	}
	return WeComResult{Status: "ACCEPTED", ProviderCode: 0, Note: "供应商已受理；不代表已读或 @ 身份已验证"}, nil
}

func (s *Service) CreateSMTP(ctx context.Context, name string, c SMTPConfig, passwordSecret, actorHex string) (Definition, error) {
	if strings.TrimSpace(c.Host) == "" || (c.Port != 465 && c.Port != 587) || (c.TLSMode != "TLS" && c.TLSMode != "STARTTLS") || !strings.Contains(c.From, "@") || len(c.AllowedRecipientDomains) == 0 {
		return Definition{}, errors.New("SMTP host, TLS mode, sender and recipient domain allowlist are required")
	}
	if _, e := s.secrets.Get(ctx, passwordSecret, "SMTP_PASSWORD"); e != nil {
		return Definition{}, errors.New("SMTP password secret unavailable")
	}
	secret, e := decode(passwordSecret)
	if e != nil {
		return Definition{}, e
	}
	actor, e := decode(actorHex)
	if e != nil {
		return Definition{}, e
	}
	raw, _ := json.Marshal(c)
	identifier := id()
	_, e = s.db.ExecContext(ctx, `INSERT INTO outbound_integrations(id,name,type,status,config_json,secret_id,created_by,updated_by) VALUES(?,?,'SMTP','DRAFT',?,?,?,?)`, identifier, strings.TrimSpace(name), raw, secret, actor, actor)
	return Definition{ID: hex.EncodeToString(identifier), Name: name, Type: "SMTP", Status: "DRAFT", Config: raw, SecretConfigured: true, Version: 1}, e
}

func (s *Service) TestSMTP(ctx context.Context, idHex, recipient string) (WeComResult, error) {
	id, e := decode(idHex)
	if e != nil {
		return WeComResult{}, e
	}
	var raw, secretID []byte
	e = s.db.QueryRowContext(ctx, `SELECT config_json,secret_id FROM outbound_integrations WHERE id=? AND type='SMTP' AND status='PUBLISHED'`, id).Scan(&raw, &secretID)
	if e != nil {
		return WeComResult{}, errors.New("published SMTP integration not found")
	}
	var c SMTPConfig
	if json.Unmarshal(raw, &c) != nil {
		return WeComResult{}, errors.New("invalid SMTP configuration")
	}
	parts := strings.Split(recipient, "@")
	if len(parts) != 2 {
		return WeComResult{}, errors.New("invalid test recipient")
	}
	allowed := false
	for _, d := range c.AllowedRecipientDomains {
		if strings.EqualFold(strings.TrimSpace(d), parts[1]) {
			allowed = true
		}
	}
	if !allowed {
		return WeComResult{}, errors.New("recipient domain not allowed")
	}
	password, e := s.secrets.Get(ctx, hex.EncodeToString(secretID), "SMTP_PASSWORD")
	if e != nil {
		return WeComResult{}, e
	}
	address := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var conn net.Conn
	if c.TLSMode == "TLS" {
		conn, e = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, e = dialer.DialContext(ctx, "tcp", address)
	}
	if e != nil {
		return WeComResult{Status: "UNKNOWN"}, e
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	client, e := smtp.NewClient(conn, c.Host)
	if e != nil {
		return WeComResult{}, e
	}
	defer client.Close()
	if c.TLSMode == "STARTTLS" {
		if e = client.StartTLS(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}); e != nil {
			return WeComResult{}, e
		}
	}
	if c.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", c.Username, string(password), c.Host)); e != nil {
			return WeComResult{}, e
		}
	}
	if e = client.Mail(c.From); e != nil {
		return WeComResult{}, e
	}
	if e = client.Rcpt(recipient); e != nil {
		return WeComResult{}, e
	}
	w, e := client.Data()
	if e != nil {
		return WeComResult{}, e
	}
	msg := []byte("From: " + c.From + "\r\nTo: " + recipient + "\r\nSubject: Yundu SMTP test\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nThis is an explicit Yundu SMTP integration test.\r\n")
	if _, e = w.Write(msg); e != nil {
		return WeComResult{}, e
	}
	if e = w.Close(); e != nil {
		return WeComResult{}, e
	}
	if e = client.Quit(); e != nil {
		return WeComResult{Status: "UNKNOWN"}, e
	}
	return WeComResult{Status: "ACCEPTED", Note: "SMTP server accepted the message; delivery or reading is not proven"}, nil
}
