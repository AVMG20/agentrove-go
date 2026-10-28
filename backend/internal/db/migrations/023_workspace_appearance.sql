-- Preset icon and color of a project's sidebar folder (keys the frontend
-- maps to an icon and a palette color). NULL is the plain folder.
ALTER TABLE workspaces ADD COLUMN icon TEXT;
ALTER TABLE workspaces ADD COLUMN color TEXT;
