// The SKILL.md Claude Code reads: YAML frontmatter, then the instructions.
export function buildSkillFile(name: string, description: string, body: string): string {
  const desc = description.trim().replace(/\s+/g, ' ');
  return `---\nname: ${name}\ndescription: ${JSON.stringify(desc)}\n---\n\n${body.trim()}\n`;
}
