-- Why a refresh token was revoked: 'rotated' marks a normal refresh, so a
-- second tab replaying it moments later isn't mistaken for token theft.
ALTER TABLE refresh_tokens ADD COLUMN revoked_reason TEXT;
