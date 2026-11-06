package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// A GitHub automation keeps one chat per pull request: the run for a new PR
// and the runs for every later push share it, so the agent re-reviews with
// what it said last time in context (automation_github_threads).

const (
	eventPROpened = "pull_request_opened"
	eventPRPushed = "pull_request_synchronize"
	// A push to a PR whose thread is still running retries this soon.
	githubBusyRetry = time.Minute
)

type prThread struct {
	chatID  *string
	headSHA *string
}

func (s *Service) getThread(ctx context.Context, automationID string, number int) (*prThread, error) {
	t := &prThread{}
	err := s.a.DB.QueryRowContext(ctx, `SELECT chat_id, head_sha FROM automation_github_threads WHERE automation_id = ? AND number = ?`,
		automationID, number).Scan(&t.chatID, &t.headSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// setThreadChat records the chat a PR's runs continue in.
func (s *Service) setThreadChat(ctx context.Context, automationID string, number int, chatID string) error {
	_, err := s.a.DB.ExecContext(ctx, `INSERT INTO automation_github_threads (automation_id, number, chat_id, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (automation_id, number) DO UPDATE SET chat_id = excluded.chat_id, updated_at = excluded.updated_at`,
		automationID, number, chatID, db.Now())
	return err
}

// startThread records the chat a new PR's run started in. Whatever the head
// is now, that run reviews it, so the head is re-taken as the baseline (a push
// delivered alongside the PR's opening must not queue a second run).
func (s *Service) startThread(ctx context.Context, automationID string, number int, chatID string) error {
	_, err := s.a.DB.ExecContext(ctx, `INSERT INTO automation_github_threads (automation_id, number, chat_id, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (automation_id, number) DO UPDATE SET chat_id = excluded.chat_id, head_sha = NULL, updated_at = excluded.updated_at`,
		automationID, number, chatID, db.Now())
	return err
}

// baselineThread remembers a PR's head without running: the commits up to it
// are covered by the PR-opened run, or predate the automation.
func (s *Service) baselineThread(ctx context.Context, automationID string, number int, sha string) error {
	_, err := s.a.DB.ExecContext(ctx, `INSERT INTO automation_github_threads (automation_id, number, head_sha, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (automation_id, number) DO UPDATE SET head_sha = excluded.head_sha, updated_at = excluded.updated_at
		WHERE automation_github_threads.head_sha IS NULL`,
		automationID, number, sha, db.Now())
	return err
}

// claimPush moves the thread's head from old to new (compare-and-swap) so a
// webhook check and a scheduled poll racing never run the same push twice.
func (s *Service) claimPush(ctx context.Context, automationID string, number int, old, new string) (bool, error) {
	res, err := s.a.DB.ExecContext(ctx, `UPDATE automation_github_threads SET head_sha = ?, updated_at = ?
		WHERE automation_id = ? AND number = ? AND head_sha = ?`, new, db.Now(), automationID, number, old)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Service) chatBusy(chatID string) bool {
	return s.a.Hooks.ChatTurnActive != nil && s.a.Hooks.ChatTurnActive(chatID)
}

func httpStatus(err error) int {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func isBusy(err error) bool { return httpStatus(err) == http.StatusConflict }

// fireThread runs ev in the PR's thread, or starts the thread when it has
// none yet or its chat was deleted. An archived thread is continued: starting
// a turn restores it to the sidebar.
func (s *Service) fireThread(ctx context.Context, x *Automation, t *prThread, ev *ActivityEvent) error {
	if t != nil && t.chatID != nil {
		if chat, err := s.a.Store.GetChat(ctx, x.UserID, *t.chatID); err == nil {
			return s.continueChat(x, chat.ID, ev)
		} else if status := httpStatus(err); status != http.StatusNotFound {
			return err
		}
	}
	chatID, err := s.fire(ctx, x, ev)
	if err != nil {
		return err
	}
	return s.setThreadChat(ctx, x.ID, *ev.Number, chatID)
}

func (s *Service) continueChat(x *Automation, chatID string, ev *ActivityEvent) error {
	if s.a.Hooks.StartChatTurn == nil {
		return httpx.Errorf(503, "Chat runtime unavailable")
	}
	_, err := s.a.Hooks.StartChatTurn(app.ChatTurnRequest{
		UserID: x.UserID, ChatID: chatID, Prompt: EventPrompt(x.Prompt, ev), ModelID: x.ModelID, PermissionMode: x.PermissionMode,
		ThinkingMode: x.ThinkingMode, Worktree: x.Worktree, SelectedPersonaName: x.SelectedPersonaName, Unattended: true,
	})
	return err
}

// pollPushes runs the thread of every open PR whose branch moved since its
// last run. It reports retry when a push waits on a thread that is still
// running. PRs created after openedCursor are left alone when PR-opened is
// watched: their opened run hasn't happened yet and will see these commits.
func (s *Service) pollPushes(ctx context.Context, x *Automation, gh *GitHubClient, openedCursor *time.Time) (retry bool, err error) {
	pulls, err := gh.ListOpenPulls(ctx, *x.GithubRepo)
	if err != nil {
		return false, err
	}
	fired := 0
	for _, pr := range pulls {
		if openedCursor != nil && pr.CreatedAt.After(*openedCursor) {
			continue
		}
		t, err := s.getThread(ctx, x.ID, pr.Number)
		if err != nil {
			return retry, err
		}
		if t == nil || t.headSHA == nil {
			if err := s.baselineThread(ctx, x.ID, pr.Number, pr.HeadSHA); err != nil {
				return retry, err
			}
			continue
		}
		prev := *t.headSHA
		if prev == pr.HeadSHA {
			continue
		}
		if fired >= maxGitHubEventsPerPoll || (t.chatID != nil && s.chatBusy(*t.chatID)) {
			retry = true
			continue
		}
		ok, err := s.claimPush(ctx, x.ID, pr.Number, prev, pr.HeadSHA)
		if err != nil {
			return retry, err
		}
		if !ok {
			continue
		}
		n := pr.Number
		ev := &ActivityEvent{
			Event: eventPRPushed, Repo: *x.GithubRepo, Number: &n, Title: pr.Title, URL: pr.URL, Author: pr.Author,
			Body: fmt.Sprintf("New commits were pushed to this pull request: its head moved from %s to %s. "+
				"Check it again, focusing on what changed since %s (e.g. `git fetch origin pull/%d/head && git diff %s %s`; "+
				"after a force-push the old commit may be gone, so review the whole PR again). "+
				"If you pushed these commits yourself earlier in this thread, there is nothing new to check: say so briefly and stop.",
				short(prev), short(pr.HeadSHA), short(prev), pr.Number, prev, pr.HeadSHA),
			CreatedAt: time.Now().UTC(),
		}
		fired++
		if err := s.fireThread(ctx, x, t, ev); err != nil {
			// Hand the push back so a later check runs it instead of losing it.
			_, _ = s.claimPush(context.WithoutCancel(ctx), x.ID, pr.Number, pr.HeadSHA, prev)
			if isBusy(err) {
				// The thread started a turn since we looked.
				retry = true
				continue
			}
			return retry, err
		}
	}
	return retry, nil
}

// fireComment runs a comment in the thread of the PR it's on, so a reply like
// "fixed, please look again" reaches the run that asked for the change. A
// comment elsewhere, or one that finds its thread busy, gets its own chat.
func (s *Service) fireComment(ctx context.Context, x *Automation, ev *ActivityEvent) error {
	if ev.Number != nil {
		t, err := s.getThread(ctx, x.ID, *ev.Number)
		if err != nil {
			return err
		}
		if t != nil && t.chatID != nil {
			if !s.chatBusy(*t.chatID) {
				err := s.fireThread(ctx, x, t, ev)
				if !isBusy(err) {
					return err
				}
			}
			slog.Info("PR thread busy; running comment in its own chat", "automation", x.ID, "pr", *ev.Number)
		}
	}
	_, err := s.fire(ctx, x, ev)
	return err
}

// retrySoon pulls the next check forward (webhook automations otherwise only
// poll hourly).
func (s *Service) retrySoon(ctx context.Context, id string) {
	soon := db.T(time.Now().Add(githubBusyRetry))
	if _, err := s.a.DB.ExecContext(ctx, `UPDATE automations SET next_run_at = ? WHERE id = ? AND next_run_at > ?`, soon, id, soon); err != nil {
		slog.Warn("automation retry", "automation", id, "err", err)
	}
}

// notePushBase seeds a PR first seen through a push delivery with the commit
// before the push, so the next check runs it even though the PR predates the
// automation (a poll alone could only take the new head as its baseline).
func (s *Service) notePushBase(ctx context.Context, automationID string, number int, before string) {
	if before == "" || number <= 0 {
		return
	}
	if err := s.baselineThread(ctx, automationID, number, before); err != nil {
		slog.Warn("record PR push base", "automation", automationID, "pr", number, "err", err)
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
