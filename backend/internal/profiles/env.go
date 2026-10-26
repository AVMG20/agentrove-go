package profiles

import (
	"path"
	"path/filepath"
	"strings"

	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// Credential env vars a profile must not inherit from the deployment or the
// user's env vars, per provider: a "Work" Claude profile never runs on the
// server's token, and a Codex API-key profile never on a stray OPENAI_API_KEY.
var credentialEnv = map[string][]string{
	models.ProviderClaude: {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_USAGE_URL", "CLAUDE_CONFIG_DIR"},
	models.ProviderCodex:       {"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_BASE_URL", "CODEX_HOME"},
	models.ProviderAntigravity: {"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS"},
}

// deploymentEnv are vars a profile must not inherit from the deployment's
// env only: headers and model aliases belong to the deployment's endpoint
// (ANTHROPIC_CUSTOM_HEADERS is passed through by default) and must not reach
// api.anthropic.com or another gateway. The user's own env vars keep them:
// they set them for the endpoint they use (a migrated gateway profile).
var deploymentEnv = map[string][]string{
	models.ProviderClaude: {"ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"},
}

// claudeShared are the box's ~/claude entries every Claude profile links to,
// so global skills (including the artifacts skill), agents, commands and
// CLAUDE.md apply whichever account a chat runs on. Settings and the login
// stay per profile.
var claudeShared = []string{"skills", "agents", "commands", "CLAUDE.md"}

// homeShared are the box HOME entries an Antigravity profile's own HOME links
// to, so git identity, SSH keys and gh auth keep working in its commands.
var homeShared = []string{".gitconfig", ".ssh", ".config/gh"}

// Dir is the profile's directory in the box.
func (p *Profile) Dir() string {
	return path.Join(sandbox.ContainerHome, ".agentrove", "profiles", p.ID)
}

// relDir is Dir relative to the box home (and the host's agent home).
func (p *Profile) relDir() string {
	return path.Join(".agentrove", "profiles", p.ID)
}

// ConfigDir is where the provider's CLI keeps its config and login: Claude's
// CLAUDE_CONFIG_DIR, Codex's CODEX_HOME, or Antigravity's HOME (its login is
// in ~/.gemini).
func (p *Profile) ConfigDir() string {
	if p.configDir != "" {
		return path.Join(sandbox.ContainerHome, path.Clean("/"+p.configDir))
	}
	switch p.Provider {
	case models.ProviderCodex:
		return path.Join(p.Dir(), "codex")
	case models.ProviderAntigravity:
		return path.Join(p.Dir(), "home")
	}
	return path.Join(p.Dir(), "claude")
}

// HostConfigDir is ConfigDir on the API host, under the user's agent home.
func (p *Profile) HostConfigDir(agentHome string) string {
	rel := strings.TrimPrefix(p.ConfigDir(), sandbox.ContainerHome+"/")
	return filepath.Join(agentHome, filepath.FromSlash(rel))
}

// Launch is how a profile's agent process runs in the box.
type Launch struct {
	Env map[string]string
	// Unset drops inherited vars (deployment and user env); UnsetGlobal drops
	// them from the deployment's env only.
	Unset       []string
	UnsetGlobal []string
	// Setup prepares the profile's dirs in the box (run as the box user before
	// the CLI starts); "" when there is nothing to do.
	Setup string
}

// Launch returns the env and setup for the profile's CLI processes.
func (p *Profile) Launch() Launch {
	l := Launch{Env: map[string]string{}, Unset: credentialEnv[p.Provider], UnsetGlobal: deploymentEnv[p.Provider]}
	dir := p.ConfigDir()
	switch p.Provider {
	case models.ProviderClaude:
		l.Env["CLAUDE_CONFIG_DIR"] = dir
		for k, v := range claudeTokenEnv(p) {
			l.Env[k] = v
		}
		var b strings.Builder
		b.WriteString("mkdir -p " + shellQuote(dir) + " " + shellQuote(sandbox.ContainerClaudeDir+"/skills"))
		for _, name := range claudeShared {
			if dir == sandbox.ContainerClaudeDir {
				break // it is ~/claude itself
			}
			link := shellQuote(dir + "/" + name)
			b.WriteString(" && { [ -e " + link + " ] || [ -L " + link + " ] || ln -s " + shellQuote(sandbox.ContainerClaudeDir+"/"+name) + " " + link + "; }")
		}
		l.Setup = b.String()
	case models.ProviderCodex:
		l.Env["CODEX_HOME"] = dir
		if p.AuthMode == AuthToken && p.Token != "" {
			// codex exec reads CODEX_API_KEY without a stored login.
			l.Env["CODEX_API_KEY"] = p.Token
		}
		l.Setup = "mkdir -p " + shellQuote(dir)
	case models.ProviderAntigravity:
		// agy keeps its login in ~/.gemini and has no config-dir override, so
		// the profile gets its own HOME with the box's dotfiles linked in.
		l.Env["HOME"] = dir
		if p.AuthMode == AuthToken && p.Token != "" {
			l.Env["GEMINI_API_KEY"] = p.Token
		}
		var b strings.Builder
		b.WriteString("mkdir -p " + shellQuote(dir+"/.config"))
		for _, name := range homeShared {
			link := shellQuote(dir + "/" + name)
			b.WriteString(" && { [ -e " + link + " ] || [ -L " + link + " ] || ln -s " + shellQuote(sandbox.ContainerHome+"/"+name) + " " + link + "; }")
		}
		provider := ""
		if p.AuthMode == AuthToken {
			provider = agyKeyProvider
		}
		b.WriteString(" && " + agySettingsScript(dir+"/"+agySettingsFile, provider))
		l.Setup = b.String()
	}
	return l
}

// claudeTokenEnv maps a Claude profile's token onto the variables the CLI
// reads: the profile's TokenEnv when set, else an OAuth token (sk-ant-oat…)
// as CLAUDE_CODE_OAUTH_TOKEN, an API key as ANTHROPIC_API_KEY, and a custom
// endpoint's token as a bearer token (ANTHROPIC_AUTH_TOKEN). An endpoint adds
// its base and usage URL.
func claudeTokenEnv(p *Profile) map[string]string {
	env := map[string]string{}
	if p.Token == "" {
		return env
	}
	if p.AuthMode == AuthEndpoint {
		env["ANTHROPIC_BASE_URL"] = p.BaseURL
		if p.UsageURL != "" {
			env["CLAUDE_USAGE_URL"] = p.UsageURL
		}
	}
	switch {
	case p.TokenEnv != "":
		env[p.TokenEnv] = p.Token
	case p.AuthMode == AuthEndpoint:
		env["ANTHROPIC_AUTH_TOKEN"] = p.Token
	case strings.HasPrefix(p.Token, "sk-ant-oat"):
		env["CLAUDE_CODE_OAUTH_TOKEN"] = p.Token
	default:
		env["ANTHROPIC_API_KEY"] = p.Token
	}
	return env
}

// agy only uses GEMINI_API_KEY when its settings name the Gemini API as the
// model provider; otherwise it wants a Google sign-in.
const (
	agySettingsFile = ".gemini/antigravity-cli/settings.json"
	agyKeyProvider  = "gemini"
)

// agySettingsScript sets modelProvider in agy's settings.json (merged into
// what is there) for an API-key profile, and removes the value it set when
// the profile signs in with Google instead ("" provider). The file is only
// rewritten when that changes.
func agySettingsScript(file, provider string) string {
	const js = `const fs=require("fs"),path=require("path");const [f,want]=process.argv.slice(1);` +
		`let s={},had=true;try{s=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){had=false}` +
		`if(!s||typeof s!=="object"||Array.isArray(s))s={};` +
		`if(want){if(s.modelProvider===want)process.exit(0);s.modelProvider=want}` +
		`else{if(!had||s.modelProvider!==` + `"` + agyKeyProvider + `"` + `)process.exit(0);delete s.modelProvider}` +
		`fs.mkdirSync(path.dirname(f),{recursive:true});fs.writeFileSync(f,JSON.stringify(s,null,2)+"\n")`
	return "node -e " + shellQuote(js) + " " + shellQuote(file) + " " + shellQuote(provider)
}

// ApplyEnv merges the deployment's env and the user's env vars for the
// profile, for processes the API runs itself (one-shot generation, the usage
// meter), the way Launch does in the box.
func (p *Profile) ApplyEnv(global, user map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range global {
		out[k] = v
	}
	for _, k := range deploymentEnv[p.Provider] {
		delete(out, k)
	}
	for k, v := range user {
		out[k] = v
	}
	for _, k := range credentialEnv[p.Provider] {
		delete(out, k)
	}
	if p.Provider == models.ProviderClaude {
		for k, v := range claudeTokenEnv(p) {
			out[k] = v
		}
	}
	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
