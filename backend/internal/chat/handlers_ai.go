package chat

import (
	"net/http"
	"strconv"
	"strings"

	"agentrove/internal/httpx"
	"agentrove/internal/prompts"
)

// --- AI helpers ---

func (m *Module) enhancePrompt(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 1<<20); err != nil {
		return err
	}
	prompt := r.FormValue("prompt")
	out, err := m.generateText(r.Context(), user(r).ID, r.FormValue("model_id"), prompts.EnhancePrompt, "Enhance this prompt: "+prompt)
	if err != nil {
		return httpx.Errorf(502, "%v", err)
	}
	if out == "" {
		out = prompt
	}
	return httpx.OK(w, map[string]string{"enhanced_prompt": out})
}

func (m *Module) generateTitleHandler(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	var prompt string
	err = m.a.DB.QueryRowContext(r.Context(), `SELECT content_text FROM messages WHERE chat_id = ? AND role = 'user' AND deleted_at IS NULL
		AND TRIM(content_text) != '' ORDER BY created_at LIMIT 1`, c.ID).Scan(&prompt)
	if err != nil {
		return httpx.BadRequest("Chat has no messages to generate a title from")
	}
	st, err := m.a.Store.GetUserSettings(r.Context(), u.ID)
	if err != nil {
		return err
	}
	title, err := m.generateText(r.Context(), u.ID, st.TitleModelID, prompts.GenerateTitleSystemPrompt, "Generate a title for this message:\n<message>\n"+prompt+"\n</message>")
	title = clipTitle(strings.Trim(strings.TrimSpace(title), `"'`))
	if err != nil || title == "" {
		return httpx.Errorf(503, "Title generation failed")
	}
	return httpx.OK(w, map[string]string{"title": title})
}

func (m *Module) askCode(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	if _, err := m.a.Store.GetMemberChat(r.Context(), u.ID, r.PathValue("chat_id")); err != nil {
		return err
	}
	var body struct {
		Question  string  `json:"question"`
		Code      string  `json:"code"`
		FilePath  *string `json:"file_path"`
		Language  *string `json:"language"`
		StartLine *int    `json:"start_line"`
		EndLine   *int    `json:"end_line"`
		ModelID   string  `json:"model_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Question == "" || body.Code == "" {
		return httpx.Unprocessable("question and code are required")
	}
	header := "Selected text from the conversation:"
	if body.FilePath != nil && body.Language != nil && body.StartLine != nil && body.EndLine != nil {
		ref := strconv.Itoa(*body.StartLine)
		if *body.EndLine != *body.StartLine {
			ref += "-" + strconv.Itoa(*body.EndLine)
		}
		header = "File: " + *body.FilePath + " (lines " + ref + ", " + *body.Language + ")"
	}
	msg := header + "\n<selection>\n" + body.Code + "\n</selection>\n\n<question>\n" + body.Question + "\n</question>"
	answer, err := m.generateText(r.Context(), u.ID, body.ModelID, prompts.InlineChatSystemPrompt, msg)
	if err != nil {
		return httpx.Errorf(502, "%v", err)
	}
	if answer == "" {
		return httpx.Errorf(502, "AI returned an empty answer")
	}
	return httpx.OK(w, map[string]string{"answer": answer})
}
