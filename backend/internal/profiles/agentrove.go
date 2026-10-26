package profiles

import (
	"path"

	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// AgentroveMCPEnv carries the agentrove MCP server ({"url", "headers"} as
// JSON) into a Codex or Antigravity turn, so its token stays out of argv.
const AgentroveMCPEnv = "AGENTROVE_MCP_SERVER"

// artifactSkill is the artifacts skill the API installs in the box's ~/claude
// (artifact.InstallSkill); Claude profiles link to it through skills/.
var artifactSkill = path.Join(sandbox.ContainerClaudeDir, "skills", "artifacts", "SKILL.md")

// Antigravity reads MCP servers, skills and always-on rules from ~/.gemini/config.
const (
	agyMCPFile   = ".gemini/config/mcp_config.json"
	agySkillDir  = ".gemini/config/skills/artifacts"
	agyRulesFile = ".gemini/config/rules/agentrove.md"
)

// ChatSetup prepares a Codex or Antigravity profile for a chat turn, after
// Launch's Setup: it copies in the artifacts skill and, for Antigravity
// (which has no per-run MCP flag), writes the agentrove MCP server from
// AgentroveMCPEnv into its config along with rules (always-on instructions;
// "" removes them). It never fails the turn. Codex takes the server as -c
// overrides instead.
func (p *Profile) ChatSetup(rules string) string {
	var dir string
	switch p.Provider {
	case models.ProviderCodex:
		dir = path.Join(p.ConfigDir(), "skills", "artifacts")
	case models.ProviderAntigravity:
		dir = path.Join(p.ConfigDir(), agySkillDir)
	default:
		return ""
	}
	src := shellQuote(artifactSkill)
	script := "{ [ ! -f " + src + " ] || { mkdir -p " + shellQuote(dir) + " && cp -f " + src + " " + shellQuote(dir+"/SKILL.md") + "; }; } 2>/dev/null"
	if p.Provider == models.ProviderAntigravity {
		home := p.ConfigDir()
		script += "; " + agyChatScript(home+"/"+agyMCPFile, home+"/"+agyRulesFile, rules)
	}
	return "{ " + script + "; true; }"
}

// agyChatScript sets the "agentrove" entry of agy's mcp_config.json from
// AgentroveMCPEnv (removing it when unset), keeping the user's own servers,
// and writes or removes the rules file. Files are only rewritten on change.
func agyChatScript(mcpFile, rulesFile, rules string) string {
	const js = `const fs=require("fs"),path=require("path");const [f,rf,rules]=process.argv.slice(1);` +
		`const put=(p,body,mode)=>{fs.mkdirSync(path.dirname(p),{recursive:true});fs.writeFileSync(p,body,{mode})};` +
		`let s={};try{s=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){}` +
		`if(!s||typeof s!=="object"||Array.isArray(s))s={};` +
		`if(!s.mcpServers||typeof s.mcpServers!=="object"||Array.isArray(s.mcpServers))s.mcpServers={};` +
		`const raw=process.env.` + AgentroveMCPEnv + `;let want;` +
		`if(raw){const c=JSON.parse(raw);want={serverUrl:c.url,headers:c.headers||{},disabled:false}}` +
		`if(JSON.stringify(s.mcpServers.agentrove)!==JSON.stringify(want)){` +
		`if(want)s.mcpServers.agentrove=want;else delete s.mcpServers.agentrove;` +
		`put(f,JSON.stringify(s,null,2)+"\n",0o600)}` +
		`const body=rules?"---\ntrigger: always_on\n---\n\n"+rules+"\n":"";let cur=null;try{cur=fs.readFileSync(rf,"utf8")}catch(e){}` +
		`if(body&&cur!==body)put(rf,body,0o644);else if(!body&&cur!==null)fs.unlinkSync(rf)`
	return "node -e " + shellQuote(js) + " " + shellQuote(mcpFile) + " " + shellQuote(rulesFile) + " " + shellQuote(rules)
}
