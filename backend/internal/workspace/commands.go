package workspace

// builtinSlashCommands is Claude's BUILTIN_SLASH_COMMANDS list (app/constants.py).
var builtinSlashCommands = []BuiltinSlashCommand{
	{Value: "/compact", Label: "Compact", Description: "Free up context by summarizing the conversation so far. Optional: /compact [instructions for summarization]"},
	{Value: "/clear", Label: "Clear", Description: "Start a new session with empty context; previous session stays on disk (resumable with /resume)"},
	{Value: "/review", Label: "Review", Description: "Review a GitHub pull request; for your working diff use /code-review"},
	{Value: "/code-review", Label: "Code Review", Description: "Review the current diff for correctness bugs and reuse/simplification/efficiency cleanups. Optional: /code-review [low|medium|high|xhigh|max] [--fix] [--comment] [target]"},
	{Value: "/security-review", Label: "Security Review", Description: "Complete a security review of the pending changes on the current branch"},
	{Value: "/simplify", Label: "Simplify", Description: "Review changed code for reuse, simplification, efficiency, and altitude cleanups, then apply the fixes"},
	{Value: "/init", Label: "Init", Description: "Initialize a new CLAUDE.md file with codebase documentation"},
	{Value: "/debug", Label: "Debug", Description: "Enable debug logging for this session and help diagnose issues"},
	{Value: "/doctor", Label: "Doctor", Description: "Health-check the Claude Code setup and fix installation, settings, and configuration issues"},
	{Value: "/insights", Label: "Insights", Description: "Generate a report analyzing your Claude Code sessions"},
	{Value: "/recap", Label: "Recap", Description: "Generate a one-line session recap now"},
	{Value: "/loop", Label: "Loop", Description: "Run a prompt or slash command on a recurring interval (e.g. /loop 5m /foo, defaults to 10m)"},
	{Value: "/batch", Label: "Batch", Description: "Research and plan a large-scale change, then execute it in parallel across isolated worktree agents that each open a PR"},
	{Value: "/deep-research", Label: "Deep Research", Description: "Fan out web searches, fetch and cross-check sources, and synthesize a cited report"},
	{Value: "/verify", Label: "Verify", Description: "Verify a code change end-to-end by driving the affected flow, not just tests or typecheck"},
	{Value: "/run", Label: "Run", Description: "Launch and drive this project's app to see a change working"},
	{Value: "/run-skill-generator", Label: "Run Skill Generator", Description: "Author or improve a per-project run skill that tells agents how to build, launch, and drive this app"},
	{Value: "/goal", Label: "Goal", Description: "Set a goal — keep working until the condition is met"},
	{Value: "/update-config", Label: "Update Config", Description: "Configure Claude Code settings via settings.json (permissions, hooks, env vars)"},
	{Value: "/claude-api", Label: "Claude API", Description: "Build apps with the Claude API or Anthropic SDK"},
	{Value: "/dataviz", Label: "Data Viz", Description: "Design guidance for charts, graphs, and dashboards"},
	{Value: "/fewer-permission-prompts", Label: "Fewer Permission Prompts", Description: "Scan transcripts for common read-only tool calls and add a project allowlist to reduce permission prompts"},
	{Value: "/team-onboarding", Label: "Team Onboarding", Description: "Help teammates ramp on Claude Code with a guide from your usage"},
	{Value: "/reload-skills", Label: "Reload Skills", Description: "Pick up skills added or changed on disk during this session"},
}
