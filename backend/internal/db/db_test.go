package db

import "testing"

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	path := t.TempDir() + "/t.db"
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	// Reopening must skip applied migrations.
	if d, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, idx := range []string{"idx_channel_messages_source", "idx_channel_messages_member",
		"idx_channel_deliveries_message", "idx_artifacts_chat", "idx_chats_automation"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("index %s missing", idx)
		}
	}
}
