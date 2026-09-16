package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Environment string            `yaml:"environment"`
	Server      Server            `yaml:"server"`
	Database    Database          `yaml:"database"`
	Security    Security          `yaml:"security"`
	Portals     map[string]Portal `yaml:"portals"`
	Limits      Limits            `yaml:"limits"`
	Antivirus   Antivirus         `yaml:"antivirus"`
}

type Server struct {
	ListenAddress   string        `yaml:"listen_address"`
	PublicBaseURL   string        `yaml:"public_base_url"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	TrustedProxies  []string      `yaml:"trusted_proxies"`
}

type Database struct {
	Host           string        `yaml:"host"`
	Port           int           `yaml:"port"`
	Name           string        `yaml:"name"`
	Username       string        `yaml:"username"`
	Password       string        `yaml:"password"`
	Parameters     string        `yaml:"parameters"`
	MaxOpen        int           `yaml:"max_open"`
	MaxIdle        int           `yaml:"max_idle"`
	MaxIdleTime    time.Duration `yaml:"max_idle_time"`
	MaxLifetime    time.Duration `yaml:"max_lifetime"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
}

type Security struct {
	MasterKey             string   `yaml:"master_key"`
	CookieSecure          bool     `yaml:"cookie_secure"`
	TrustPortalHeaderFrom []string `yaml:"trust_portal_header_from"`
}

type Portal struct {
	Zone   string `yaml:"zone"`
	Origin string `yaml:"origin"`
}

type Limits struct {
	JSONBodyBytes       int64 `yaml:"json_body_bytes"`
	OfficeFileBytes     int64 `yaml:"office_file_bytes"`
	ProductionFileBytes int64 `yaml:"production_file_bytes"`
	FilesPerRequest     int   `yaml:"files_per_request"`
	OfficeRequestBytes  int64 `yaml:"office_request_bytes"`
	Uploads             int   `yaml:"uploads"`
	Downloads           int   `yaml:"downloads"`
	Copies              int   `yaml:"copies"`
}
type Antivirus struct {
	Enabled bool          `yaml:"enabled"`
	Address string        `yaml:"address"`
	Timeout time.Duration `yaml:"timeout"`
}

var envPattern = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	expanded := envPattern.ReplaceAllStringFunc(string(raw), func(match string) string {
		name := envPattern.FindStringSubmatch(match)[1]
		return os.Getenv(name)
	})
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaults(c *Config) {
	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = "127.0.0.1:9080"
	}
	if c.Server.ShutdownTimeout == 0 {
		c.Server.ShutdownTimeout = 120 * time.Second
	}
	if c.Database.Port == 0 {
		c.Database.Port = 3306
	}
	if c.Database.Parameters == "" {
		c.Database.Parameters = "charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27"
	}
	if c.Database.MaxOpen == 0 {
		c.Database.MaxOpen = 20
	}
	if c.Database.MaxIdle == 0 {
		c.Database.MaxIdle = 5
	}
	if c.Database.MaxIdleTime == 0 {
		c.Database.MaxIdleTime = 5 * time.Minute
	}
	if c.Database.MaxLifetime == 0 {
		c.Database.MaxLifetime = 30 * time.Minute
	}
	if c.Database.ConnectTimeout == 0 {
		c.Database.ConnectTimeout = 5 * time.Second
	}
	if c.Limits.JSONBodyBytes == 0 {
		c.Limits.JSONBodyBytes = 1 << 20
	}
	if c.Limits.OfficeFileBytes == 0 {
		c.Limits.OfficeFileBytes = 30 << 20
	}
	if c.Limits.ProductionFileBytes == 0 {
		c.Limits.ProductionFileBytes = 100 << 20
	}
	if c.Limits.FilesPerRequest == 0 {
		c.Limits.FilesPerRequest = 5
	}
	if c.Limits.OfficeRequestBytes == 0 {
		c.Limits.OfficeRequestBytes = 150 << 20
	}
	if c.Limits.Uploads == 0 {
		c.Limits.Uploads = 6
	}
	if c.Limits.Downloads == 0 {
		c.Limits.Downloads = 6
	}
	if c.Limits.Copies == 0 {
		c.Limits.Copies = 2
	}
	if c.Antivirus.Timeout == 0 {
		c.Antivirus.Timeout = 10 * time.Minute
	}
}

func (c Config) Validate() error {
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return errors.New("environment must be development, test, or production")
	}
	if c.Database.Host == "" || c.Database.Name == "" || c.Database.Username == "" {
		return errors.New("database host, name, and username are required")
	}
	if c.Database.Password == "" {
		return errors.New("database password is required")
	}
	if c.Database.MaxOpen < 1 || c.Database.MaxIdle < 0 || c.Database.MaxIdle > c.Database.MaxOpen {
		return errors.New("invalid database connection pool")
	}
	if c.Limits.OfficeFileBytes > 30<<20 || c.Limits.FilesPerRequest > 5 || c.Limits.OfficeRequestBytes > 150<<20 {
		return errors.New("office-to-production limits cannot exceed hard safety boundaries")
	}
	if c.Antivirus.Enabled && strings.TrimSpace(c.Antivirus.Address) == "" {
		return errors.New("enabled antivirus requires clamd address")
	}
	for name, p := range c.Portals {
		if p.Zone != "OFFICE" && p.Zone != "PRODUCTION" {
			return fmt.Errorf("portal %s has invalid zone", name)
		}
		if p.Origin == "" {
			return fmt.Errorf("portal %s origin is required", name)
		}
		parsed, err := url.Parse(p.Origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("portal %s origin is invalid", name)
		}
	}
	if c.Environment == "production" {
		if !c.Security.CookieSecure {
			return errors.New("production requires secure cookies")
		}
		if len(c.Security.TrustPortalHeaderFrom) == 0 {
			return errors.New("production requires trusted portal ingress CIDRs")
		}
		for _, cidr := range c.Security.TrustPortalHeaderFrom {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("invalid trusted portal ingress CIDR %q", cidr)
			}
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.Security.MasterKey))
		if err != nil || len(key) != 32 {
			return errors.New("production requires a base64 encoded 32-byte master key")
		}
		for name, p := range c.Portals {
			parsed, _ := url.Parse(p.Origin)
			if parsed.Scheme != "https" {
				return fmt.Errorf("production portal %s requires HTTPS origin", name)
			}
		}
		base, err := url.Parse(c.Server.PublicBaseURL)
		if err != nil || base.Scheme != "https" || base.Host == "" {
			return errors.New("production public base URL must use HTTPS")
		}
	}
	return nil
}
