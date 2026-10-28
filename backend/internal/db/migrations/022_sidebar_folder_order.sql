-- Order of the project folders in the sidebar thread list: a JSON list of
-- workspace ids. Folders missing from it (or all, when NULL) come first, most
-- recent activity first.
ALTER TABLE user_settings ADD COLUMN sidebar_folder_order TEXT;
