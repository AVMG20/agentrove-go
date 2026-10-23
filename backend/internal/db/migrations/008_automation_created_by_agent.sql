-- Automations an agent created through MCP; agents may only edit or delete
-- these, and the UI can flag them.
ALTER TABLE automations ADD COLUMN created_by_agent INTEGER NOT NULL DEFAULT 0;
