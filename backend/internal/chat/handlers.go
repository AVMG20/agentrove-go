package chat

import (
	"net/http"

	"agentrove/internal/auth"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/chat"
	req := m.a.Auth.Require
	mux.HandleFunc("POST "+p+"/chats", req(m.createChat))
	mux.HandleFunc("POST "+p+"/chat", req(m.sendMessage))
	mux.HandleFunc("POST "+p+"/enhance-prompt", req(m.enhancePrompt))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/generate-title", req(m.generateTitleHandler))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/ask-code", req(m.askCode))
	mux.HandleFunc("GET "+p+"/chats", req(m.listChats))
	mux.HandleFunc("GET "+p+"/chats/search", req(m.searchChats))
	mux.HandleFunc("GET "+p+"/chats/active-streams", req(m.activeStreams))
	mux.HandleFunc("GET "+p+"/chats/background-tasks", req(m.backgroundTasks))
	mux.HandleFunc("GET "+p+"/chats/events", req(m.chatEventsFeed))
	mux.HandleFunc("GET "+p+"/chats/streams", req(m.streamsFeed))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}", req(m.getChat))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/context-usage", req(m.contextUsage))
	mux.HandleFunc("PATCH "+p+"/chats/{chat_id}", req(m.updateChat))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/viewed", req(m.markViewed))
	mux.HandleFunc("DELETE "+p+"/chats/all", req(m.deleteAllChats))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}", req(m.deleteChat))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/messages", req(m.listMessages))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/status", req(m.streamStatus))
	mux.HandleFunc("GET "+p+"/messages/{message_id}/events", req(m.messageEvents))
	mux.HandleFunc("GET "+p+"/messages/{message_id}/render", req(m.messageRender))
	mux.HandleFunc("POST "+p+"/messages/{message_id}/checkpoint/restore-all", req(m.restoreCheckpoint))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/background-tasks/{task_id}/stop", req(m.stopBackgroundTask))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/subagents", req(m.listSubagents))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/subagents/{subagent_id}", req(m.getSubagent))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/stream", req(m.cancelStream))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/permissions/{request_id}/respond", req(m.respondPermission))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/elicitation/{request_id}/respond", req(m.respondElicitation))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/queue", req(m.queueMessage))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/queue", req(m.getQueue))
	mux.HandleFunc("PATCH "+p+"/chats/{chat_id}/queue/{message_id}", req(m.updateQueued))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/queue/{message_id}", req(m.deleteQueued))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/queue/{message_id}/send-now", req(m.sendNow))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/queue", req(m.clearQueue))

	models := m.a.Cfg.APIPrefix + "/models"
	mux.HandleFunc("GET "+models, req(m.listModels))
	mux.HandleFunc("GET "+models+"/", req(m.listModels))

	att := m.a.Cfg.APIPrefix + "/attachments"
	mux.HandleFunc("GET "+att+"/temp/preview", req(m.tempPreview))
	mux.HandleFunc("GET "+att+"/{attachment_id}/preview", req(m.attachmentPreview))
	mux.HandleFunc("GET "+att+"/{attachment_id}/download", req(m.attachmentDownload))
}

func user(r *http.Request) *models.User { return auth.UserFrom(r.Context()) }

func (m *Module) listModels(w http.ResponseWriter, r *http.Request) error {
	switch k := r.URL.Query().Get("agent_kind"); k {
	case "", models.ProviderClaude:
		return httpx.OK(w, models.ClaudeModels)
	case "all":
		return httpx.OK(w, models.AllModels())
	default:
		if ms := models.ModelsFor(k); ms != nil {
			return httpx.OK(w, ms)
		}
		return httpx.OK(w, []models.ModelInfo{})
	}
}
