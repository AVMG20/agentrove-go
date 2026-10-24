-- Whether agents in the user's box get the browser tools (Playwright MCP).
ALTER TABLE user_settings ADD COLUMN browser_tools_enabled INTEGER NOT NULL DEFAULT 1;
