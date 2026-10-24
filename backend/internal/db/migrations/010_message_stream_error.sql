-- Why a failed turn failed, shown by the UI beside the message instead of in its text.
ALTER TABLE messages ADD COLUMN stream_error TEXT;
