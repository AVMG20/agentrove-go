// Package config loads runtime settings from the environment (and an optional
// .env file). Names match the Python backend so existing .env files keep working.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment string
	Port        int
	BaseURL     string
	FrontendURL string
	APIPrefix   string

	SecretKey                string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	RequireEmailVerification bool
	RegistrationDisabled     bool

	StoragePath  string
	DatabasePath string
	// HostStoragePath is StoragePath as the Docker daemon sees it, when the
	// API itself runs in a container with storage mounted elsewhere.
	HostStoragePath string

	AllowedOrigins []string

	MaxUploadSize int64

	MailUsername string
	MailPassword string
	MailFrom     string
	MailFromName string
	MailServer   string
	MailPort     int

	GitAuthorName  string
	GitAuthorEmail string

	MCPEnabled             bool
	MCPTokenLifetime       time.Duration
	ArtifactsEnabled       bool
	ArtifactViewTokenTTL   time.Duration
	AgentEnv               map[string]string
	AgentEnvPassthroughKey []string
	ClaudeUsageEndpoints   map[string]string

	DockerImage        string
	DockerNetwork      string
	DockerHost         string
	DockerMemLimit     string
	DockerCPUPeriod    int64
	DockerCPUQuota     int64
	DockerPidsLimit    int64
	DockerNodeMaxOldMB int

	// How long a Claude CLI process may sit idle before it's closed. The
	// session id is persisted so the next turn resumes it losslessly.
	ChatProcessIdleTTL time.Duration
	// How long a Docker sandbox may go without any activity (turn, terminal,
	// file/git request) before its container is stopped. 0 disables.
	SandboxIdleTTL time.Duration

	ClaudeBinary string
	LogLevel     string
}

func Load() (*Config, error) {
	loadDotEnv(".env")
	c := &Config{
		Environment:              env("ENVIRONMENT", "development"),
		Port:                     envInt("PORT", 8080),
		BaseURL:                  env("BASE_URL", "http://localhost:8080"),
		FrontendURL:              env("FRONTEND_URL", "http://localhost:3000"),
		APIPrefix:                "/api/v1",
		SecretKey:                env("SECRET_KEY", ""),
		AccessTokenTTL:           time.Duration(envInt("ACCESS_TOKEN_EXPIRE_MINUTES", 15)) * time.Minute,
		RefreshTokenTTL:          time.Duration(envInt("REFRESH_TOKEN_EXPIRE_DAYS", 30)) * 24 * time.Hour,
		RequireEmailVerification: envBool("REQUIRE_EMAIL_VERIFICATION", false),
		RegistrationDisabled:     envBool("REGISTRATION_DISABLED", false),
		StoragePath:              env("STORAGE_PATH", "/data/agentrove/storage"),
		DatabasePath:             env("DATABASE_PATH", ""),
		HostStoragePath:          env("HOST_STORAGE_PATH", ""),
		AllowedOrigins:           envList("ALLOWED_ORIGINS", []string{"http://localhost:3000"}),
		MaxUploadSize:            int64(envInt("MAX_UPLOAD_SIZE", 5*1024*1024)),
		MailUsername:             env("MAIL_USERNAME", "apikey"),
		MailPassword:             env("MAIL_PASSWORD", ""),
		MailFrom:                 env("MAIL_FROM", "noreply@agentrove.local"),
		MailFromName:             env("MAIL_FROM_NAME", "Agentrove"),
		MailServer:               env("MAIL_SERVER", "smtp.sendgrid.net"),
		MailPort:                 envInt("MAIL_PORT", 587),
		GitAuthorName:            env("GIT_AUTHOR_NAME", ""),
		GitAuthorEmail:           env("GIT_AUTHOR_EMAIL", ""),
		MCPEnabled:               envBool("AGENTROVE_MCP_ENABLED", true),
		MCPTokenLifetime:         time.Duration(envInt("AGENTROVE_MCP_TOKEN_LIFETIME_SECONDS", 604800)) * time.Second,
		ArtifactsEnabled:         envBool("AGENTROVE_ARTIFACTS_ENABLED", true),
		ArtifactViewTokenTTL:     time.Duration(envInt("ARTIFACT_VIEW_TOKEN_LIFETIME_SECONDS", 1800)) * time.Second,
		AgentEnv:                 ParseEnvLines(env("AGENT_ENV", "")),
		AgentEnvPassthroughKey:   envList("AGENT_ENV_PASSTHROUGH", []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN"}),
		// Claude proxy hosts -> their 5h/weekly usage endpoint, fetched with
		// the user's ANTHROPIC_AUTH_TOKEN. Deployment-set only.
		ClaudeUsageEndpoints: parsePairs(env("CLAUDE_USAGE_ENDPOINTS", "claude.bitsec.dev=https://claude-admin.bitsec.dev/api/usage")),
		DockerImage:          env("DOCKER_IMAGE", "agentrove-sandbox:latest"),
		DockerNetwork:        env("DOCKER_NETWORK", "agentrove-sandbox-net"),
		DockerHost:           env("DOCKER_HOST", ""),
		DockerMemLimit:       env("DOCKER_MEM_LIMIT", "8g"),
		DockerCPUPeriod:      int64(envInt("DOCKER_CPU_PERIOD", 100000)),
		DockerCPUQuota:       int64(envInt("DOCKER_CPU_QUOTA", 200000)),
		DockerPidsLimit:      int64(envInt("DOCKER_PIDS_LIMIT", 512)),
		DockerNodeMaxOldMB:   envInt("DOCKER_NODE_MAX_OLD_SPACE_MB", 8192),
		ChatProcessIdleTTL:   time.Duration(envInt("CHAT_PROCESS_IDLE_TTL_SECONDS", 600)) * time.Second,
		SandboxIdleTTL:       time.Duration(envInt("SANDBOX_IDLE_TTL_SECONDS", 900)) * time.Second,
		ClaudeBinary:         env("CLAUDE_BINARY", "claude"),
		LogLevel:             env("LOG_LEVEL", "INFO"),
	}

	if c.DatabasePath == "" {
		c.DatabasePath = filepath.Join(c.StoragePath, "agentrove.db")
	}
	if c.HostStoragePath == "" {
		c.HostStoragePath = c.StoragePath
	}
	if len(c.SecretKey) < 32 {
		return nil, fmt.Errorf("SECRET_KEY must be set and at least 32 characters long")
	}
	if err := os.MkdirAll(c.StoragePath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage path: %w", err)
	}
	return c, nil
}

// AgentHomeDir is the user's persistent HOME, shared by all their workspaces
// and both sandbox kinds (mounted at /home/user in the Docker box), so CLI
// logins, toolchains (rustup, sdkman, nvm), caches and git config survive.
func (c *Config) AgentHomeDir(userID string) string {
	return filepath.Join(c.StoragePath, "agent-homes", userID)
}

// UserWorkspacesDir holds the user's storage-managed projects (cloned or
// empty), mounted at /home/user/projects in the Docker box.
func (c *Config) UserWorkspacesDir(userID string) string {
	return filepath.Join(c.StoragePath, "workspaces", userID)
}

func (c *Config) UploadsDir() string { return filepath.Join(c.StoragePath, "uploads") }

// AgentGlobalEnv is the deployment-wide env for agent sessions and terminals:
// passthrough keys first so an explicit AGENT_ENV line wins.
func (c *Config) AgentGlobalEnv() map[string]string {
	out := map[string]string{}
	for _, k := range c.AgentEnvPassthroughKey {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	for k, v := range c.AgentEnv {
		out[k] = v
	}
	return out
}

func (c *Config) IsProduction() bool { return c.Environment == "production" }

func ParseEnvLines(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if ok && k != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

func parsePairs(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" && v != "" {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envList(key string, def []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
