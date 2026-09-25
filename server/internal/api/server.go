package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/rest"
	"remi/server/internal/account"
	"remi/server/internal/actionitems"
	"remi/server/internal/advice"
	"remi/server/internal/agenttools"
	"remi/server/internal/announcements"
	"remi/server/internal/apps"
	"remi/server/internal/auth"
	"remi/server/internal/authflow"
	"remi/server/internal/automodel"
	"remi/server/internal/calendarmeetings"
	"remi/server/internal/candidates"
	"remi/server/internal/chat"
	"remi/server/internal/chatfiles"
	"remi/server/internal/chatfirst"
	"remi/server/internal/config"
	"remi/server/internal/conversations"
	"remi/server/internal/csat"
	"remi/server/internal/dailysummaries"
	"remi/server/internal/desktopprompts"
	"remi/server/internal/desktopusage"
	"remi/server/internal/developer"
	"remi/server/internal/devkeys"
	"remi/server/internal/emailprefs"
	"remi/server/internal/externalapi"
	"remi/server/internal/fairuse"
	"remi/server/internal/feedbackadmin"
	"remi/server/internal/firmware"
	"remi/server/internal/focussessions"
	"remi/server/internal/folders"
	"remi/server/internal/framerequests"
	"remi/server/internal/goals"
	"remi/server/internal/health"
	"remi/server/internal/hume"
	"remi/server/internal/imports"
	"remi/server/internal/integrations"
	"remi/server/internal/jit"
	"remi/server/internal/knowledgegraph"
	"remi/server/internal/mcp"
	"remi/server/internal/mcpkeys"
	"remi/server/internal/memories"
	"remi/server/internal/metrics"
	"remi/server/internal/migrationapi"
	"remi/server/internal/mobilefeedback"
	"remi/server/internal/notificationapi"
	"remi/server/internal/notifications"
	"remi/server/internal/oauthapp"
	"remi/server/internal/omni"
	"remi/server/internal/payments"
	"remi/server/internal/people"
	"remi/server/internal/phonecalls"
	"remi/server/internal/proactivity"
	"remi/server/internal/proxy"
	"remi/server/internal/realtime"
	"remi/server/internal/referrals"
	"remi/server/internal/releases"
	"remi/server/internal/scores"
	"remi/server/internal/screenactivity"
	"remi/server/internal/screenframes"
	"remi/server/internal/speechprofile"
	"remi/server/internal/stagedtasks"
	"remi/server/internal/staticmap"
	"remi/server/internal/stt"
	"remi/server/internal/syncjobs"
	"remi/server/internal/taskintelligence"
	"remi/server/internal/toolsapi"
	"remi/server/internal/transcripts"
	"remi/server/internal/trends"
	"remi/server/internal/trigger"
	"remi/server/internal/tts"
	"remi/server/internal/users"
	"remi/server/internal/voice"
	"remi/server/internal/voicechat"
	"remi/server/internal/workstreams"
	"remi/server/internal/wrapped"
)

func BuildServer(cfg config.Config, db *sql.DB, redisClient *redis.Client, audioHandler http.Handler, actionHandler actionitems.Handler, conversationHandler conversations.Handler, transcriptHandler transcripts.Handler, sttHandler stt.Handler, memoryHandler memories.Handler, userHandler users.Handler, chatHandler chat.Handler, sessionHandler chat.SessionsHandler, desktopHandler chat.DesktopHandler, notificationHandler notifications.Handler, folderHandler folders.Handler, goalHandler goals.Handler, integrationHandler integrations.Handler, accountHandler account.Handler, syncHandler syncjobs.Handler, scoreHandler scores.Handler, meetingHandler calendarmeetings.Handler, autoHandler automodel.Handler, mapHandler staticmap.Handler, csatHandler csat.Handler, fileHandler chatfiles.Handler, ttsHandler tts.Handler, realtimeHandler realtime.Handler, trendHandler trends.Handler, dailySummaryHandler dailysummaries.Handler, focusHandler focussessions.Handler, screenHandler screenactivity.Handler, peopleHandler people.Handler, usageHandler desktopusage.Handler, thumbnailStore chatfiles.ThumbnailStore, stagedHandler stagedtasks.Handler, workstreamHandler workstreams.Handler, candidateHandler candidates.Handler, speechProfileHandler speechprofile.Handler, referralHandler referrals.Handler, mobileFeedbackHandler mobilefeedback.Handler, frameRequestHandler framerequests.Handler) *rest.Server {
	if integrationHandler.DB == nil {
		integrationHandler.DB = db
	}
	if integrationHandler.Provider == nil && cfg.LLMEndpoint != "" {
		integrationHandler.Provider = chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
	}
	if memoryHandler.Provider == nil && cfg.LLMEndpoint != "" {
		memoryHandler.Provider = chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
	}
	if userHandler.Provider == nil && cfg.LLMEndpoint != "" {
		userHandler.Provider = chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
	}
	verifier := auth.NewFirebaseVerifier(cfg.FirebaseProjectID)
	devKeyService := devkeys.Service{DB: db}
	authHandler := authflow.Handler{Redis: redisClient, BaseURL: os.Getenv("BASE_API_URL")}
	protected := auth.MiddlewareWithAPIKey(cfg.AuthMode, verifier, func(ctx context.Context, token, method, path string) (string, error) {
		return devKeyService.Authenticate(ctx, token, method, path)
	})
	server := rest.MustNewServer(rest.RestConf{Host: hostFromAddr(cfg.HTTPAddr), Port: portFromAddr(cfg.HTTPAddr), Timeout: cfg.ReadHeaderTimeout.Milliseconds()})
	mcpHandler := mcp.Handler{DB: db, Keys: mcpkeys.Service{DB: db}}
	mcpKeyHandler := mcpkeys.Handler{Service: mcpkeys.Service{DB: db}}
	devKeyHandler := devkeys.Handler{Service: devkeys.Service{DB: db}}
	developerHandler := developer.Handler{Actions: actionHandler.Service, Memories: memoryHandler.Service, Conversations: conversationHandler.Service}
	oauthHandler := mcpkeys.OAuthHandler{Service: mcpkeys.Service{DB: db}, Verifier: verifier}
	appOAuthHandler := oauthapp.Handler{DB: db, Verifier: verifier}
	notificationAPIHandler := notificationapi.Handler{DB: db}
	triggerHandler := trigger.NewHandler(redisClient)
	triggerHandler.DB = db
	screenFrameAdjudicationHandler := screenframes.Handler{DB: db, Store: frameRequestHandler.Store}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/v1/account/cutover/control", Handler: protected(http.HandlerFunc(accountHandler.CutoverControl)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/account-deletion-wipes/run", Handler: accountHandler.RunWipe},
		{Method: http.MethodPost, Path: "/v1/agents/hume/callback", Handler: hume.Handler{DB: db}.Callback},
		{Method: http.MethodPost, Path: "/v1/chat-first/blocks/validate", Handler: protected(http.HandlerFunc(chatfirst.Handler{DB: db}.Validate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/chat/materialize-prompts", Handler: protected(http.HandlerFunc(chatfirst.Handler{DB: db}.Materialize)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat/materialize-prompts", Handler: protected(http.HandlerFunc(chatfirst.Handler{DB: db}.Materialize)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/chat/deferrals", Handler: protected(http.HandlerFunc(chatfirst.Handler{DB: db}.Deferral)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/jit/rollout-decision", Handler: protected(http.HandlerFunc(jit.Handler{}.RolloutDecision)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/jit/trigger-snapshot", Handler: protected(http.HandlerFunc(jit.Handler{}.TriggerSnapshot)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/jit/trigger-feedback", Handler: protected(http.HandlerFunc(jit.Handler{}.TriggerFeedback)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/jit/proactivity/reservations", Handler: protected(http.HandlerFunc(jit.Handler{}.Reservation)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/jit/knowledge-ledger/prompt-snapshot", Handler: protected(http.HandlerFunc(jit.Handler{}.PromptSnapshot)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/jit/knowledge-ledger/mirror-snapshot", Handler: protected(http.HandlerFunc(jit.Handler{}.MirrorSnapshot)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/ai-profile/synthesize", Handler: protected(http.HandlerFunc(userHandler.SynthesizeAIProfile)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/migration/requests", Handler: protected(http.HandlerFunc(migrationapi.Handler{DB: db}.Mutate)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/migration/requests", Handler: protected(http.HandlerFunc(migrationapi.Handler{DB: db}.Requests)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/migration/batch-requests", Handler: protected(http.HandlerFunc(migrationapi.Handler{DB: db}.Batch)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/migration/requests/data-protection-level/finalize", Handler: protected(http.HandlerFunc(migrationapi.Handler{DB: db}.Finalize)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/analytics/chat_message", Handler: protected(http.HandlerFunc(chatHandler.Analytics)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/analytics/memory_summary", Handler: protected(http.HandlerFunc(legacyMemorySummaryAnalytics)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversation-finalization-jobs/run", Handler: conversations.Handler{Service: conversationHandler.Service, Queue: conversationHandler.Queue, Transcripts: conversationHandler.Transcripts, Provider: conversationHandler.Provider}.RunFinalizationJob},
		{Method: http.MethodPost, Path: "/v1/workflow-migrations/task-goal-links", Handler: protected(http.HandlerFunc(workstreamHandler.ImportTaskGoalLinks)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/admin/feedback/reports", Handler: feedbackadmin.Handler{DB: db}.Dates},
		{Method: http.MethodGet, Path: "/v1/admin/feedback/reports/:report_date", Handler: feedbackadmin.Handler{DB: db}.Report},
		{Method: http.MethodPost, Path: "/v1/admin/feedback/reports/:report_date/generate", Handler: feedbackadmin.Handler{DB: db}.Generate},
		{Method: http.MethodPost, Path: "/v1/admin/feedback/reports/generate-yesterday", Handler: feedbackadmin.Handler{DB: db}.GenerateYesterday},
		{Method: http.MethodGet, Path: "/v1/admin/feedback/events/:event_id/context", Handler: feedbackadmin.Handler{DB: db}.Context},
		{Method: http.MethodPost, Path: "/v2/sync-jobs/run", Handler: syncjobs.Handler{Queue: syncHandler.Queue, Conversations: syncHandler.Conversations, Audio: syncHandler.Audio}.Run},
		{Method: http.MethodPost, Path: "/v2/audio-merge-jobs/run", Handler: syncjobs.Handler{Queue: syncHandler.Queue, Conversations: syncHandler.Conversations, Audio: syncHandler.Audio}.RunAudioMerge},
		{Method: http.MethodGet, Path: "/v1/users/analytics/memory_summary", Handler: protected(http.HandlerFunc(legacyMemorySummaryAnalytics)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/notification", Handler: notificationAPIHandler.Admin},
		{Method: http.MethodPost, Path: "/v1/integrations/notification", Handler: notificationAPIHandler.Integration},
		{Method: http.MethodPost, Path: "/v1/connectors/synthesize", Handler: protected(http.HandlerFunc(integrationHandler.Synthesize)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/oauth/authorize", Handler: appOAuthHandler.Authorize},
		{Method: http.MethodPost, Path: "/v1/oauth/token", Handler: appOAuthHandler.Token},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_id/user/memories", Handler: externalapi.Handler{DB: db}.Memories},
		{Method: http.MethodPost, Path: "/v2/integrations/:app_id/user/memories", Handler: externalapi.Handler{DB: db}.Memories},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_id/user/conversations", Handler: externalapi.Handler{DB: db}.Conversations},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_id/memories", Handler: externalapi.Handler{DB: db}.Memories},
		{Method: http.MethodPost, Path: "/v2/integrations/:app_id/memories", Handler: externalapi.Handler{DB: db}.Memories},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_id/conversations", Handler: externalapi.Handler{DB: db}.Conversations},
		{Method: http.MethodPost, Path: "/v2/integrations/:app_id/search/conversations", Handler: externalapi.Handler{DB: db}.SearchConversations},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_id/tasks", Handler: externalapi.Handler{DB: db}.Tasks},
		{Method: http.MethodPost, Path: "/v2/integrations/:app_id/notification", Handler: externalapi.Handler{DB: db}.Notification},
		{Method: http.MethodGet, Path: "/v1/what-matters-now", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Evaluate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/what-matters-now/evaluate", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Evaluate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/task-intelligence/interventions", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Intervention)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/task-intelligence/feedback", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Feedback)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/task-intelligence/outcomes", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Outcome)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/task-intelligence/context-snapshot", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Snapshot)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/task-intelligence/open-loop-snapshot", Handler: protected(http.HandlerFunc(taskintelligence.Handler{DB: db}.Snapshot)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/auth/authorize", Handler: authHandler.Authorize},
		{Method: http.MethodGet, Path: "/v1/auth/callback/google", Handler: authHandler.GoogleCallback},
		{Method: http.MethodPost, Path: "/v1/auth/callback/apple", Handler: authHandler.AppleCallback},
		{Method: http.MethodPost, Path: "/v1/auth/token", Handler: authHandler.Token},
		{Method: http.MethodPost, Path: "/v1/auth/local-dev/custom-token", Handler: authHandler.LocalDevCustomToken},
		{Method: http.MethodPost, Path: "/v2/chat-context", Handler: deprecatedEndpoint},
		{Method: http.MethodGet, Path: "/v1/proxy/deepgram/ws/v1/listen", Handler: deprecatedEndpoint},
		{Method: http.MethodPost, Path: "/v1/proxy/deepgram/ws/v1/listen", Handler: deprecatedEndpoint},
		{Method: http.MethodGet, Path: "/v1/announcements/changelogs", Handler: announcements.Handler{DB: db}.Public},
		{Method: http.MethodGet, Path: "/v1/fair-use/status", Handler: protected(http.HandlerFunc(fairuse.Handler{DB: db}.Status)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/admin/fair-use/flagged", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodGet, Path: "/v1/admin/fair-use/user/:uid", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodPost, Path: "/v1/admin/fair-use/user/:uid/reset", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodPost, Path: "/v1/admin/fair-use/user/:uid/set-stage", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodPost, Path: "/v1/admin/fair-use/user/:uid/resolve-event/:event_id", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodGet, Path: "/v1/admin/fair-use/case/:case_ref", Handler: fairuse.Handler{DB: db}.Admin},
		{Method: http.MethodGet, Path: "/v1/announcements/features", Handler: announcements.Handler{DB: db}.Public},
		{Method: http.MethodGet, Path: "/v1/announcements/general", Handler: announcements.Handler{DB: db}.Public},
		{Method: http.MethodGet, Path: "/v1/announcements/pending", Handler: protected(http.HandlerFunc(announcements.Handler{DB: db}.Pending)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/announcements/:announcement_id/dismiss", Handler: protected(http.HandlerFunc(announcements.Handler{DB: db}.Dismiss)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/announcements/all", Handler: announcements.Handler{DB: db}.Admin},
		{Method: http.MethodGet, Path: "/v1/announcements/:announcement_id", Handler: announcements.Handler{DB: db}.Admin},
		{Method: http.MethodPost, Path: "/v1/announcements", Handler: announcements.Handler{DB: db}.Admin},
		{Method: http.MethodPut, Path: "/v1/announcements/:announcement_id", Handler: announcements.Handler{DB: db}.Admin},
		{Method: http.MethodDelete, Path: "/v1/announcements/:announcement_id", Handler: announcements.Handler{DB: db}.Admin},
		{Method: http.MethodGet, Path: "/", Handler: desktopHealthHandler(false)},
		{Method: http.MethodGet, Path: "/health", Handler: desktopHealthHandler(false)},
		{Method: http.MethodGet, Path: "/v1/health", Handler: desktopHealthHandler(false)},
		{Method: http.MethodGet, Path: "/email/unsubscribe", Handler: emailprefs.Handler{DB: db}.ServeHTTP},
		{Method: http.MethodPost, Path: "/email/unsubscribe", Handler: emailprefs.Handler{DB: db}.ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/firmware/latest", Handler: firmware.Handler{}.ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/firmware/stable", Handler: firmware.Handler{}.ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/firmware/version", Handler: firmware.Handler{}.ServeHTTP},
		{Method: http.MethodGet, Path: "/ready", Handler: desktopHealthHandler(true)},
		{Method: http.MethodPost, Path: "/v2/agent/provision", Handler: retiredAgentVM},
		{Method: http.MethodPost, Path: "/v2/agent/vm/stop-self", Handler: retiredAgentVM},
		{Method: http.MethodGet, Path: "/v2/agent/status", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("null"))
		}},
		{Method: http.MethodPost, Path: "/v1/desktop/proactivity/completions", Handler: protected(http.HandlerFunc(proactivity.Handler{Provider: graphProvider(cfg)}.Complete)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/proxy/gemini/models/:model", Handler: protected(http.HandlerFunc(proxy.Handler{Redis: redisClient}.Gemini)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/proxy/gemini-stream/models/:model", Handler: protected(http.HandlerFunc(proxy.Handler{Redis: redisClient}.GeminiStream)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/proxy/deepgram/v1/listen", Handler: protected(http.HandlerFunc(proxy.Handler{Redis: redisClient}.Deepgram)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/desktop/prompts", Handler: protected(http.HandlerFunc(desktopprompts.Handler{DB: db}.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/config/api-keys", Handler: protected(http.HandlerFunc(apiKeysHandler)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/agent/tools", Handler: protected(http.HandlerFunc(agenttools.Handler{Actions: actionHandler.Service, Memories: memoryHandler.Service, Integrations: integrationHandler.Service}.List)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/agent/execute-tool", Handler: protected(http.HandlerFunc(agenttools.Handler{Actions: actionHandler.Service, Memories: memoryHandler.Service, Integrations: integrationHandler.Service}.Execute)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/tools/conversations", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tools/conversations/search", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tools/conversations/search-chunks", Handler: protected(http.HandlerFunc(toolsapi.Handler{DB: db}.SearchChunks)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/tools/memories", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tools/memories/search", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/tools/action-items", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tools/action-items", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/tools/action-items/:action_item_id", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tools/calendar-events", Handler: protected(http.HandlerFunc(toolsapi.Handler{Conversations: conversationHandler.Service, Memories: memoryHandler.Service, Actions: actionHandler.Service, Integrations: integrationHandler.Service}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/keys", Handler: protected(http.HandlerFunc(devKeyHandler.List)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/keys", Handler: protected(http.HandlerFunc(devKeyHandler.Create)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/dev/keys/:key_id", Handler: protected(http.HandlerFunc(devKeyHandler.Delete)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/memories", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/memories", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/memories/batch", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/memories/:memory_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/dev/user/memories/:memory_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/dev/user/memories/:memory_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/action-items", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/action-items", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/action-items/batch", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/dev/user/action-items/:action_item_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/dev/user/action-items/:action_item_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/conversations", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/conversations", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/conversations/:conversation_id", Handler: protected(http.HandlerFunc(developerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/folders", Handler: protected(http.HandlerFunc(folderHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/goals", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/goals", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/dev/user/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/dev/user/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/goals/:goal_id/history", Handler: protected(http.HandlerFunc(goalHandler.History)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/goals/:goal_id/progress", Handler: protected(http.HandlerFunc(goalHandler.Progress)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/daily-summaries", Handler: protected(http.HandlerFunc(dailySummaryHandler.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/dev/user/daily-summaries/:summary_id", Handler: protected(http.HandlerFunc(dailySummaryHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/mcp/keys", Handler: protected(http.HandlerFunc(mcpKeyHandler.List)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/mcp/keys", Handler: protected(http.HandlerFunc(mcpKeyHandler.Create)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/mcp/keys/:key_id", Handler: protected(http.HandlerFunc(mcpKeyHandler.Delete)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/mcp/oauth/grants", Handler: protected(http.HandlerFunc(mcpKeyHandler.Grants)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/mcp/oauth/grants/:grant_id", Handler: protected(http.HandlerFunc(mcpKeyHandler.RevokeGrant)).ServeHTTP},
		{Method: http.MethodGet, Path: "/authorize", Handler: oauthHandler.Authorize},
		{Method: http.MethodPost, Path: "/authorize", Handler: oauthHandler.Authorize},
		{Method: http.MethodPost, Path: "/token", Handler: oauthHandler.Token},
		{Method: http.MethodGet, Path: "/.well-known/oauth-protected-resource", Handler: oauthHandler.ProtectedResource},
		{Method: http.MethodGet, Path: "/.well-known/oauth-protected-resource/v1/mcp/sse", Handler: oauthHandler.ProtectedResource},
		{Method: http.MethodGet, Path: "/.well-known/oauth-authorization-server", Handler: oauthHandler.AuthorizationServer},
		{Method: http.MethodGet, Path: "/v1/mcp/profile", Handler: mcpHandler.Profile},
		{Method: http.MethodGet, Path: "/v1/mcp/memories", Handler: mcpHandler.Memories},
		{Method: http.MethodPost, Path: "/v1/mcp/memories", Handler: mcpHandler.Memories},
		{Method: http.MethodGet, Path: "/v1/mcp/memories/search", Handler: mcpHandler.Memories},
		{Method: http.MethodDelete, Path: "/v1/mcp/memories/:memory_id", Handler: mcpHandler.Memories},
		{Method: http.MethodPatch, Path: "/v1/mcp/memories/:memory_id", Handler: mcpHandler.Memories},
		{Method: http.MethodGet, Path: "/v1/mcp/conversations", Handler: mcpHandler.Conversations},
		{Method: http.MethodGet, Path: "/v1/mcp/conversations/search", Handler: mcpHandler.Conversations},
		{Method: http.MethodGet, Path: "/v1/mcp/conversations/:conversation_id", Handler: mcpHandler.Conversations},
		{Method: http.MethodGet, Path: "/v1/mcp/action-items", Handler: mcpHandler.ActionItems},
		{Method: http.MethodPost, Path: "/v1/mcp/action-items", Handler: mcpHandler.ActionItems},
		{Method: http.MethodGet, Path: "/v1/mcp/action-items/search", Handler: mcpHandler.ActionItems},
		{Method: http.MethodPatch, Path: "/v1/mcp/action-items/:action_item_id", Handler: mcpHandler.ActionItems},
		{Method: http.MethodDelete, Path: "/v1/mcp/action-items/:action_item_id", Handler: mcpHandler.ActionItems},
		{Method: http.MethodPost, Path: "/v1/mcp/action-items/:action_item_id/complete", Handler: mcpHandler.ActionItems},
		{Method: http.MethodGet, Path: "/v1/mcp/goals", Handler: mcpHandler.Goals},
		{Method: http.MethodGet, Path: "/v1/mcp/chat", Handler: mcpHandler.Chat},
		{Method: http.MethodGet, Path: "/v1/mcp/people", Handler: mcpHandler.People},
		{Method: http.MethodGet, Path: "/v1/mcp/screen-activity", Handler: mcpHandler.ScreenActivity},
		{Method: http.MethodGet, Path: "/v1/mcp/daily-summaries", Handler: mcpHandler.DailySummaries},
		{Method: http.MethodPost, Path: "/v1/mcp/sse", Handler: mcpHandler.Stream},
		{Method: http.MethodGet, Path: "/v1/mcp/sse", Handler: mcpHandler.Stream},
		{Method: http.MethodHead, Path: "/v1/mcp/sse", Handler: mcpHandler.Stream},
		{Method: http.MethodDelete, Path: "/v1/mcp/sse", Handler: mcpHandler.Stream},
		{Method: http.MethodGet, Path: "/v1/mcp/sse/info", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"endpoint": "/v1/mcp/sse", "auth": "Bearer <api_key>", "server": "omi-mcp-server"})
		})},
		{Method: http.MethodGet, Path: "/.well-known/apple-developer-domain-association.txt", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(nil)
		}},
		{Method: http.MethodGet, Path: "/appcast.xml", Handler: releases.Handler{DB: db}.Appcast},
		{Method: http.MethodGet, Path: "/updates/latest", Handler: releases.Handler{DB: db}.Latest},
		{Method: http.MethodGet, Path: "/download", Handler: releases.Handler{DB: db}.Download},
		{Method: http.MethodGet, Path: "/v2/desktop/appcast.xml", Handler: releases.Handler{DB: db}.Appcast},
		{Method: http.MethodGet, Path: "/v2/desktop/download/latest", Handler: releases.Handler{DB: db}.Download},
		{Method: http.MethodGet, Path: "/v2/desktop/download/beta", Handler: releases.Handler{DB: db}.BetaDownload},
		{Method: http.MethodGet, Path: "/v2/desktop/update-feed/windows", Handler: releases.Handler{DB: db}.WindowsUpdateFeed},
		{Method: http.MethodGet, Path: "/v2/desktop/download/windows", Handler: releases.Handler{DB: db}.WindowsDownload},
		{Method: http.MethodGet, Path: "/v2/desktop/update-policy", Handler: releases.Handler{DB: db}.UpdatePolicy},
		{Method: http.MethodPost, Path: "/v2/desktop/clear-cache", Handler: releases.Handler{DB: db}.ClearCache},
		{Method: http.MethodGet, Path: "/v2/desktop/releases/:release_id", Handler: releases.Handler{DB: db}.ManifestV2},
		{Method: http.MethodPost, Path: "/v2/desktop/releases", Handler: releases.Handler{DB: db}.RegisterManifest},
		{Method: http.MethodPost, Path: "/v2/desktop/beta/candidates/reserve", Handler: releases.Handler{DB: db}.ReserveBetaCandidate},
		{Method: http.MethodPut, Path: "/v2/desktop/beta/admission", Handler: releases.Handler{DB: db}.SetBetaAdmission},
		{Method: http.MethodPost, Path: "/v2/desktop/beta/promote-candidate", Handler: releases.Handler{DB: db}.PromoteBetaCandidate},
		{Method: http.MethodPost, Path: "/v2/desktop/beta/breakglass", Handler: releases.Handler{DB: db}.BetaBreakglass},
		{Method: http.MethodGet, Path: "/v2/desktop/previews/:slug", Handler: releases.Handler{DB: db}.Preview},
		{Method: http.MethodGet, Path: "/v2/desktop/previews/:slug/:source_sha", Handler: releases.Handler{DB: db}.Preview},
		{Method: http.MethodPost, Path: "/v2/desktop/previews/publish", Handler: releases.Handler{DB: db}.PublishPreview},
		{Method: http.MethodDelete, Path: "/v2/desktop/previews/:slug", Handler: releases.Handler{DB: db}.DelistPreview},
		{Method: http.MethodPost, Path: "/updates/releases", Handler: releases.Handler{DB: db, Secret: os.Getenv("RELEASE_SECRET")}.Create},
		{Method: http.MethodPatch, Path: "/updates/releases/promote", Handler: releases.Handler{DB: db, Secret: os.Getenv("RELEASE_SECRET")}.Promote},
		{Method: http.MethodGet, Path: "/healthz", Handler: health.Handler(func() bool { return true }).ServeHTTP},
		{Method: http.MethodGet, Path: "/metrics", Handler: metrics.Handler{}.ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/wrapped/:year", Handler: protected(http.HandlerFunc(wrapped.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/wrapped/:year/generate", Handler: protected(http.HandlerFunc(wrapped.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/.well-known/openai-apps-challenge", Handler: openAIAppsChallenge},
		{Method: http.MethodPost, Path: "/v1/import/limitless", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/import/jobs", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/import/jobs/:job_id", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.Item)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/import/jobs/:job_id/cancel", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/import/jobs/:job_id", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/import/limitless/conversations", Handler: protected(http.HandlerFunc(imports.Handler{DB: db}.DeleteLimitless)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/phone/numbers/verify", Handler: protected(http.HandlerFunc(phonecalls.Handler{DB: db}.Verify)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/phone/numbers/verify/check", Handler: protected(http.HandlerFunc(phonecalls.Handler{DB: db}.Check)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/phone/numbers", Handler: protected(http.HandlerFunc(phonecalls.Handler{DB: db}.Numbers)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/phone/numbers/:phone_number_id", Handler: protected(http.HandlerFunc(phonecalls.Handler{DB: db}.Delete)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/phone/token", Handler: protected(http.HandlerFunc(phonecalls.Handler{DB: db}.Token)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/phone/twiml", Handler: http.HandlerFunc(phonecalls.Handler{DB: db}.TwiML)},
		{Method: http.MethodGet, Path: "/v1/knowledge-graph", Handler: protected(http.HandlerFunc(knowledgegraph.Handler{DB: db}.Get)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/knowledge-graph/canonical", Handler: protected(http.HandlerFunc(knowledgegraph.Handler{DB: db}.Canonical)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/knowledge-graph/rebuild", Handler: protected(http.HandlerFunc(knowledgegraph.Handler{DB: db, Provider: graphProvider(cfg)}.Rebuild)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/knowledge-graph/extract", Handler: protected(http.HandlerFunc(knowledgegraph.Handler{DB: db, Provider: graphProvider(cfg)}.Extract)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/knowledge-graph", Handler: protected(http.HandlerFunc(knowledgegraph.Handler{DB: db}.Delete)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/payments/available-plans", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.AvailablePlans)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/payments/overage-info", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.OverageInfo)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/payments/checkout-session", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Checkout)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/payments/upgrade-subscription", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Upgrade)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/payments/subscription", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Cancel)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/payments/cancel", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Cancel)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/payments/success", Handler: payments.Handler{DB: db}.ReturnPage},
		{Method: http.MethodGet, Path: "/v1/payments/cancel", Handler: payments.Handler{DB: db}.ReturnPage},
		{Method: http.MethodGet, Path: "/v1/payments/portal-return", Handler: payments.Handler{DB: db}.ReturnPage},
		{Method: http.MethodGet, Path: "/v1/stripe/return/:account_id", Handler: payments.Handler{DB: db}.ReturnPage},
		{Method: http.MethodPost, Path: "/v1/stripe/webhook", Handler: http.HandlerFunc(payments.Handler{DB: db}.Webhook)},
		{Method: http.MethodPost, Path: "/v1/stripe/connect/webhook", Handler: http.HandlerFunc(payments.Handler{DB: db}.ConnectWebhook)},
		{Method: http.MethodPost, Path: "/v1/payments/customer-portal", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Portal)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/stripe/connect-accounts", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Connect)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/stripe/supported-countries", Handler: http.HandlerFunc(payments.Handler{DB: db}.SupportedCountries)},
		{Method: http.MethodGet, Path: "/v1/stripe/onboarded", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Onboarded)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/stripe/refresh/:account_id", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Refresh)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/:app_id/subscription", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.AppSubscription)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/apps/:app_id/subscription", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.AppSubscription)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/subscription", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Subscription)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/me/byok-active", Handler: protected(http.HandlerFunc(userHandler.BYOKActive)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/me/byok-active", Handler: protected(http.HandlerFunc(userHandler.BYOKActive)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/paywall", Handler: protected(http.HandlerFunc(userHandler.Paywall)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/usage-quota", Handler: protected(http.HandlerFunc(userHandler.UsageQuota)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/trial", Handler: protected(http.HandlerFunc(userHandler.Trial)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/referral", Handler: protected(http.HandlerFunc(referralHandler.Link)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/advice", Handler: protected(http.HandlerFunc(advice.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/advice", Handler: protected(http.HandlerFunc(advice.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/advice/mark-all-read", Handler: protected(http.HandlerFunc(advice.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/advice/:advice_id", Handler: protected(http.HandlerFunc(advice.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/advice/:advice_id", Handler: protected(http.HandlerFunc(advice.Handler{DB: db}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/me/referral/claim", Handler: protected(http.HandlerFunc(referralHandler.Claim)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/mobile/feedback", Handler: protected(http.HandlerFunc(mobileFeedbackHandler.Submit)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/frame-requests", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/frame-requests/pending", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/frame-requests/status/:request_id", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/frame-requests/temporary/:request_id/image", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/frame-requests/:request_id/state", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/frame-requests/:request_id/upload", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/frame-requests/:request_id/promote", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/photos/:photo_id/image", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/screenshots", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/screenshots/:frame_id/image", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/conversations/:conversation_id/screenshots/:frame_id", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/conversations/:conversation_id/screenshots", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/screenshot-sharing", Handler: protected(http.HandlerFunc(frameRequestHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/shared/screenshots", Handler: frameRequestHandler.ServeHTTP},
		{Method: http.MethodGet, Path: "/r/:code", Handler: http.HandlerFunc(referralHandler.Capture)},
		{Method: http.MethodGet, Path: "/v1/paypal/payment-details", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.PayPal)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/paypal/payment-details", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.PayPal)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/payment-methods/status", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Methods)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/payment-methods/default", Handler: protected(http.HandlerFunc(payments.Handler{DB: db}.Default)).ServeHTTP},
		{Method: http.MethodGet, Path: "/readyz", Handler: health.Handler(func() bool { return true }).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/audio/stream", Handler: protected(audioHandler).ServeHTTP},
		{Method: http.MethodGet, Path: "/v4/listen", Handler: protected(audioHandler).ServeHTTP},
		{Method: http.MethodGet, Path: "/v4/web/listen", Handler: protected(audioHandler).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/omni/relay", Handler: protected(http.HandlerFunc(omni.NewHandler().ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/trigger/listen", Handler: protected(http.HandlerFunc(triggerHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/tts/synthesize", Handler: protected(http.HandlerFunc(ttsHandler.Synthesize)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/tts/synthesize", Handler: protected(http.HandlerFunc(ttsHandler.Synthesize)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/realtime/session", Handler: protected(http.HandlerFunc(realtimeHandler.Mint)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/realtime/usage", Handler: protected(http.HandlerFunc(realtimeHandler.Usage)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/trends", Handler: http.HandlerFunc(trendHandler.Get).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/daily-summaries", Handler: protected(http.HandlerFunc(dailySummaryHandler.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/daily-summaries/:summary_id", Handler: protected(http.HandlerFunc(dailySummaryHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/daily-summaries/:summary_id", Handler: protected(http.HandlerFunc(dailySummaryHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/daily-summaries/:summary_id/visibility", Handler: protected(http.HandlerFunc(dailySummaryHandler.Visibility)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/daily-summaries/:summary_id/shared", Handler: http.HandlerFunc(dailySummaryHandler.Shared).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/daily-summaries", Handler: protected(http.HandlerFunc(dailySummaryHandler.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/daily-summaries/:summary_id/regenerate", Handler: protected(http.HandlerFunc(dailySummaryHandler.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/focus-sessions", Handler: protected(http.HandlerFunc(focusHandler.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/focus-sessions", Handler: protected(http.HandlerFunc(focusHandler.List)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/focus-sessions/:session_id", Handler: protected(http.HandlerFunc(focusHandler.Delete)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/focus-stats", Handler: protected(http.HandlerFunc(focusHandler.Stats)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/screen-activity/sync", Handler: protected(http.HandlerFunc(screenHandler.Sync)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/screen-activity", Handler: protected(http.HandlerFunc(screenHandler.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/screen-activity/summary", Handler: protected(http.HandlerFunc(screenHandler.Summary)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/people", Handler: protected(http.HandlerFunc(peopleHandler.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/people", Handler: protected(http.HandlerFunc(peopleHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/people/:person_id", Handler: protected(http.HandlerFunc(peopleHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/people/:person_id", Handler: protected(http.HandlerFunc(peopleHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/people/:person_id/name", Handler: protected(http.HandlerFunc(peopleHandler.Rename)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/people/:person_id/speech-samples/:sample_index", Handler: protected(http.HandlerFunc(peopleHandler.DeleteSample)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/desktop-usage/daily", Handler: protected(http.HandlerFunc(usageHandler.Record)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/usage", Handler: protected(http.HandlerFunc(userHandler.UsageStats)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/llm-usage", Handler: protected(http.HandlerFunc(userHandler.LLMUsage)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/llm-usage/top-features", Handler: protected(http.HandlerFunc(userHandler.LLMTopFeatures)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/me/llm-usage", Handler: protected(http.HandlerFunc(userHandler.LLMUsage)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/me/llm-usage/total", Handler: protected(http.HandlerFunc(userHandler.LLMTotal)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/stt/transcribe", Handler: protected(http.HandlerFunc(sttHandler.Transcribe)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/voice-message/transcribe", Handler: protected(http.HandlerFunc(voice.Handler{Provider: sttHandler.Provider}.Transcribe)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/voice-messages", Handler: protected(http.HandlerFunc(voicechat.Handler{Chat: chatHandler.Service, Provider: sttHandler.Provider}.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/voice-message/transcribe-stream", Handler: protected(http.HandlerFunc(voice.Handler{Provider: sttHandler.Provider}.Stream)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/profile", Handler: protected(http.HandlerFunc(userHandler.Profile)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/profile", Handler: protected(http.HandlerFunc(userHandler.Profile)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/geolocation", Handler: protected(http.HandlerFunc(userHandler.Geolocation)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/developer/webhook/:wtype", Handler: protected(http.HandlerFunc(userHandler.DeveloperWebhook)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/developer/webhook/:wtype", Handler: protected(http.HandlerFunc(userHandler.DeveloperWebhook)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/developer/webhook/:wtype/disable", Handler: protected(http.HandlerFunc(userHandler.DeveloperWebhook)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/developer/webhook/:wtype/enable", Handler: protected(http.HandlerFunc(userHandler.DeveloperWebhook)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/developer/webhooks/status", Handler: protected(http.HandlerFunc(userHandler.DeveloperWebhooksStatus)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/developer/button-event", Handler: protected(http.HandlerFunc(userHandler.ButtonEvent)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app-categories", Handler: http.HandlerFunc(apps.HandlerCategories).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app-capabilities", Handler: http.HandlerFunc(apps.HandlerCapabilities).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app/proactive-notification-scopes", Handler: http.HandlerFunc(apps.HandlerNotificationScopes).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app/payment-plans", Handler: http.HandlerFunc(apps.HandlerPaymentPlans).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app/plans", Handler: http.HandlerFunc(apps.HandlerPaymentPlans).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/app/generate-description", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/app/generate-description-emoji", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Generate)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/app/generate-prompts", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/app/generate", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/app/generate-icon", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/app/thumbnails", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, ThumbnailStore: thumbnailStore}.Thumbnail)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/personas/twitter/profile", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Twitter)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/personas/twitter/verify-ownership", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Twitter)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/personas/twitter/initial-message", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Provider: graphProvider(cfg)}.Twitter)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.List)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/enabled", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Enabled)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/summary-app-ids", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.SummaryIDs).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/summary-app-ids/:app_id", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.SummaryIDs).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/summary-app-ids/:app_id", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.SummaryIDs).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/tester", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Testers).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/tester/access", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Testers).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/apps/tester/access", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Testers).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/tester/check", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.TesterCheck)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/public/unapproved", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Unapproved).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/personas/:persona_id", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.PersonaAdmin).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/personas/:persona_id", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.PersonaAdmin)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/personas/:persona_id", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.PersonaAdmin).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/:app_id/keys", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AppAPIKeys)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/:app_id/keys", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AppAPIKeys)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/apps/:app_id/keys/:key_id", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AppAPIKeys)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/:app_id/refresh-manifest", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.RefreshManifest)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/mcp", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.MCP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/mcp/callback", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.MCPOAuthCallback).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/:app_id/mcp/refresh", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.MCP)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/migrate-owner", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}, Verifier: verifier}.MigrateOwner)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/:app_id", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Item)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Create)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/apps/:app_id", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Update)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/apps/:app_id", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Delete)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/apps/:app_id/change-visibility", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Visibility)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/approved-apps", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Public).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/popular", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Public)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/personas", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Persona)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/personas", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Persona)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/user/persona", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Persona)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/apps/:app_id/popular", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AdminToggle).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/:app_id/approve", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AdminToggle).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/:app_id/reject", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.AdminToggle).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/enable", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Toggle)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/disable", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Toggle)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/apps", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Catalog).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/apps/capability/:capability_id/grouped", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Catalog).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/apps/search", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Search)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/apps/review", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Review)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/apps/:app_id/reviews", Handler: http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Reviews).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/apps/:app_id/review", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Review)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/apps/:app_id/review/reply", Handler: protected(http.HandlerFunc(apps.Handler{Service: apps.Service{DB: db}}.Reply)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/language", Handler: protected(http.HandlerFunc(userHandler.Language)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/language", Handler: protected(http.HandlerFunc(userHandler.Language)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/available-languages", Handler: protected(http.HandlerFunc(userHandler.AvailableLanguages)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/transcription-preferences", Handler: protected(http.HandlerFunc(userHandler.TranscriptionPreferences)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/transcription-preferences", Handler: protected(http.HandlerFunc(userHandler.TranscriptionPreferences)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/notification-settings", Handler: protected(http.HandlerFunc(userHandler.NotificationSettings)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/notification-settings", Handler: protected(http.HandlerFunc(userHandler.NotificationSettings)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/assistant-settings", Handler: protected(http.HandlerFunc(userHandler.AssistantSettings)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/assistant-settings", Handler: protected(http.HandlerFunc(userHandler.AssistantSettings)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/ai-profile", Handler: protected(http.HandlerFunc(userHandler.AIProfile)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/ai-profile", Handler: protected(http.HandlerFunc(userHandler.AIProfile)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/training-data-opt-in", Handler: protected(http.HandlerFunc(userHandler.TrainingDataOptIn)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/training-data-opt-in", Handler: protected(http.HandlerFunc(userHandler.TrainingDataOptIn)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/location-context-consent", Handler: protected(http.HandlerFunc(userHandler.LocationContextConsent)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/users/location-context-consent", Handler: protected(http.HandlerFunc(userHandler.LocationContextConsent)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/preferences/app", Handler: protected(http.HandlerFunc(userHandler.AppPreferences)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/users/preferences/app", Handler: protected(http.HandlerFunc(userHandler.AppPreferences)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/export", Handler: protected(http.HandlerFunc(userHandler.Export)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/onboarding", Handler: protected(http.HandlerFunc(userHandler.Onboarding)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/onboarding", Handler: protected(http.HandlerFunc(userHandler.Onboarding)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/private-cloud-sync", Handler: protected(http.HandlerFunc(userHandler.PrivateCloudSync)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/private-cloud-sync", Handler: protected(http.HandlerFunc(userHandler.PrivateCloudSync)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/screen-frame-egress/settings", Handler: protected(http.HandlerFunc(userHandler.ScreenFrameSettings)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/screen-frame-egress/settings", Handler: protected(http.HandlerFunc(userHandler.ScreenFrameSettings)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/screen-frame-egress/adjudications", Handler: protected(http.HandlerFunc(screenFrameAdjudicationHandler.ServeHTTP)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/store-recording-permission", Handler: protected(http.HandlerFunc(userHandler.StoreRecordingPermission)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/store-recording-permission", Handler: protected(http.HandlerFunc(userHandler.StoreRecordingPermission)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/store-recording-permission", Handler: protected(http.HandlerFunc(userHandler.StoreRecordingPermission)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/daily-summary-settings", Handler: protected(http.HandlerFunc(notificationHandler.DailySummarySettings)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/daily-summary-settings", Handler: protected(http.HandlerFunc(notificationHandler.DailySummarySettings)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/daily-summary-settings/test", Handler: protected(http.HandlerFunc(dailySummaryHandler.Generate)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/mentor-notification-settings", Handler: protected(http.HandlerFunc(notificationHandler.MentorSettings)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/users/mentor-notification-settings", Handler: protected(http.HandlerFunc(notificationHandler.MentorSettings)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/users/fcm-token", Handler: protected(http.HandlerFunc(notificationHandler.Token)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/users/time-zone", Handler: protected(http.HandlerFunc(notificationHandler.TimeZone)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/daily-score", Handler: protected(http.HandlerFunc(scoreHandler.Daily)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/scores", Handler: protected(http.HandlerFunc(scoreHandler.All)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/auto/model-pick", Handler: protected(http.HandlerFunc(autoHandler.Pick)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/static-map", Handler: protected(http.HandlerFunc(mapHandler.Get)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/csat/config", Handler: protected(http.HandlerFunc(csatHandler.Config)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/csat/ratings", Handler: protected(http.HandlerFunc(csatHandler.Rating)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/files", Handler: protected(http.HandlerFunc(fileHandler.Upload)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/files", Handler: protected(http.HandlerFunc(fileHandler.Upload)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/folders", Handler: protected(http.HandlerFunc(folderHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/folders", Handler: protected(http.HandlerFunc(folderHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/folders/:folder_id", Handler: protected(http.HandlerFunc(folderHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/folders/:folder_id", Handler: protected(http.HandlerFunc(folderHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/folders/:folder_id", Handler: protected(http.HandlerFunc(folderHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/folders/:folder_id/conversations", Handler: protected(http.HandlerFunc(folderHandler.Conversations)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/folders/reorder", Handler: protected(http.HandlerFunc(folderHandler.Reorder)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/folder", Handler: protected(http.HandlerFunc(folderHandler.Move)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/folders/:folder_id/conversations/bulk-move", Handler: protected(http.HandlerFunc(folderHandler.Bulk)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/all", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/completed", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/canonical", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/canonical/list", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/goals", Handler: protected(http.HandlerFunc(goalHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/:goal_id/detail", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/goals/:goal_id", Handler: protected(http.HandlerFunc(goalHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/goals/:goal_id/progress", Handler: protected(http.HandlerFunc(goalHandler.Progress)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/goals/:goal_id/progress-events", Handler: protected(http.HandlerFunc(goalHandler.Events)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/:goal_id/progress-events", Handler: protected(http.HandlerFunc(goalHandler.Events)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/:goal_id/history", Handler: protected(http.HandlerFunc(goalHandler.History)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/suggest", Handler: protected(http.HandlerFunc(goalHandler.Suggest)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/goals/extract-progress", Handler: protected(http.HandlerFunc(goalHandler.ExtractProgress)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/:goal_id/advice", Handler: protected(http.HandlerFunc(goalHandler.Advice)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/goals/advice", Handler: protected(http.HandlerFunc(goalHandler.Advice)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/goals/:goal_id/focus", Handler: protected(http.HandlerFunc(goalHandler.Focus)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/goals/:goal_id/focus", Handler: protected(http.HandlerFunc(goalHandler.Unfocus)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/goals/:goal_id/lifecycle", Handler: protected(http.HandlerFunc(goalHandler.Lifecycle)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/integrations/apple-health/sync", Handler: protected(http.HandlerFunc(integrationHandler.AppleHealth)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/integrations/:app_key/oauth-url", Handler: protected(http.HandlerFunc(integrationHandler.OAuthURL)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations", Handler: protected(http.HandlerFunc(integrationHandler.TaskIntegrations)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/default", Handler: protected(http.HandlerFunc(integrationHandler.DefaultTaskIntegration)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/task-integrations/default", Handler: protected(http.HandlerFunc(integrationHandler.DefaultTaskIntegration)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/task-integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/task-integrations/:app_key", Handler: protected(http.HandlerFunc(integrationHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/:app_key/oauth-url", Handler: protected(http.HandlerFunc(integrationHandler.OAuthURL)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/task-integrations/:app_key/tasks", Handler: protected(http.HandlerFunc(integrationHandler.CreateTask)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/asana/workspaces", Handler: protected(http.HandlerFunc(integrationHandler.TaskCatalog)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/asana/projects/:workspace_gid", Handler: protected(http.HandlerFunc(integrationHandler.TaskCatalog)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/clickup/teams", Handler: protected(http.HandlerFunc(integrationHandler.TaskCatalog)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/clickup/spaces/:team_id", Handler: protected(http.HandlerFunc(integrationHandler.TaskCatalog)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/task-integrations/clickup/lists/:space_id", Handler: protected(http.HandlerFunc(integrationHandler.TaskCatalog)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/integrations/:app_key/callback", Handler: http.HandlerFunc(integrationHandler.OAuthCallback).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/calendar/onboarding/status", Handler: protected(http.HandlerFunc(integrationHandler.CalendarStatus)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/calendar/google/events", Handler: protected(http.HandlerFunc(integrationHandler.GoogleEvents)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/calendar/capture-gaps", Handler: protected(http.HandlerFunc(integrationHandler.CalendarCaptureGaps)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/x/oauth-url", Handler: protected(http.HandlerFunc(integrationHandler.XOAuthURL)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/x/oauth/callback", Handler: http.HandlerFunc(integrationHandler.XOAuthCallback).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/x/connection-status", Handler: protected(http.HandlerFunc(integrationHandler.XConnectionStatus)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/x/posts", Handler: protected(http.HandlerFunc(integrationHandler.XPosts)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/x/sync", Handler: protected(http.HandlerFunc(integrationHandler.XSync)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/x/disconnect", Handler: protected(http.HandlerFunc(integrationHandler.XDisconnect)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/calendar/meetings", Handler: protected(http.HandlerFunc(meetingHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/calendar/meetings", Handler: protected(http.HandlerFunc(meetingHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/calendar/meetings/:meeting_id", Handler: protected(http.HandlerFunc(meetingHandler.Item)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/calendar/onboarding/skip", Handler: protected(http.HandlerFunc(integrationHandler.CalendarSkip)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/calendar/onboarding/reset", Handler: protected(http.HandlerFunc(integrationHandler.CalendarReset)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/users/delete-account", Handler: protected(http.HandlerFunc(accountHandler.Delete)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/sync-capture-manifest", Handler: protected(http.HandlerFunc(syncHandler.Manifest)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/sync/audio/:conversation_id/precache", Handler: protected(http.HandlerFunc(syncHandler.PrecacheAudio)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/sync/audio/:conversation_id/urls", Handler: protected(http.HandlerFunc(syncHandler.AudioURLs)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/sync/audio/:conversation_id/:audio_file_id", Handler: protected(http.HandlerFunc(syncHandler.DownloadAudio)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/sync-local-files", Handler: protected(http.HandlerFunc(syncHandler.Start)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/sync-local-files", Handler: protected(http.HandlerFunc(syncHandler.Start)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/sync-local-files/:job_id", Handler: protected(http.HandlerFunc(syncHandler.Status)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/messages", Handler: protected(http.HandlerFunc(chatHandler.Messages)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/messages", Handler: protected(http.HandlerFunc(chatHandler.Messages)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v2/messages", Handler: protected(http.HandlerFunc(chatHandler.Messages)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/messages/:message_id/report", Handler: protected(http.HandlerFunc(chatHandler.Report)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/messages/share", Handler: protected(http.HandlerFunc(chatHandler.Share)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/messages/shared/:token", Handler: http.HandlerFunc(chatHandler.Shared).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v2/messages/:message_id/rating", Handler: protected(http.HandlerFunc(chatHandler.Rating)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/initial-message", Handler: protected(http.HandlerFunc(chatHandler.Initial)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/initial-message", Handler: protected(http.HandlerFunc(chatHandler.Initial)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat/generate-reply", Handler: protected(http.HandlerFunc(chatHandler.Generate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat/completions", Handler: protected(http.HandlerFunc(chatHandler.Completions)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat-sessions", Handler: protected(http.HandlerFunc(sessionHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/chat-sessions", Handler: protected(http.HandlerFunc(sessionHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/chat-sessions/:session_id", Handler: protected(http.HandlerFunc(sessionHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v2/chat-sessions/:session_id", Handler: protected(http.HandlerFunc(sessionHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v2/chat-sessions/:session_id", Handler: protected(http.HandlerFunc(sessionHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/users/stats/chat-messages", Handler: protected(http.HandlerFunc(sessionHandler.Count)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat/initial-message", Handler: protected(http.HandlerFunc(sessionHandler.Initial)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/chat/generate-title", Handler: protected(http.HandlerFunc(sessionHandler.Title)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v2/desktop/messages", Handler: protected(http.HandlerFunc(desktopHandler.Messages)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/desktop/messages", Handler: protected(http.HandlerFunc(desktopHandler.Messages)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v2/desktop/messages/reconcile", Handler: protected(http.HandlerFunc(desktopHandler.Reconcile)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v2/desktop/messages", Handler: protected(http.HandlerFunc(desktopHandler.Messages)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v2/desktop/messages/:message_id/rating", Handler: protected(http.HandlerFunc(desktopHandler.Rating)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/memories", Handler: protected(http.HandlerFunc(memoryHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories", Handler: protected(http.HandlerFunc(memoryHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories/batch", Handler: protected(http.HandlerFunc(memoryHandler.BatchCreate)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/memories/extract", Handler: protected(http.HandlerFunc(memoryHandler.Extract)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memory-imports/batch", Handler: protected(http.HandlerFunc(memoryHandler.ImportBatch)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories/:memory_id/use", Handler: protected(http.HandlerFunc(memoryHandler.Use)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories/:memory_id/revert", Handler: protected(http.HandlerFunc(memoryHandler.Revert)).ServeHTTP},
		{Method: http.MethodGet, Path: "/memory/search", Handler: protected(http.HandlerFunc(memoryHandler.ProductSearch)).ServeHTTP},
		{Method: http.MethodGet, Path: "/memory/vector/search", Handler: protected(http.HandlerFunc(memoryHandler.VectorSearch)).ServeHTTP},
		{Method: http.MethodGet, Path: "/memory/archive/search", Handler: protected(http.HandlerFunc(memoryHandler.ArchiveSearch)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v3/memories", Handler: protected(http.HandlerFunc(memoryHandler.DeleteAll)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v3/memories/batch", Handler: protected(http.HandlerFunc(memoryHandler.Batch)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/memories/:memory_id", Handler: protected(http.HandlerFunc(memoryHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v3/memories/:memory_id", Handler: protected(http.HandlerFunc(memoryHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v3/memories/:memory_id/visibility", Handler: protected(http.HandlerFunc(memoryHandler.Visibility)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v3/memories/:memory_id/read", Handler: protected(http.HandlerFunc(memoryHandler.Read)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v3/memories/:memory_id", Handler: protected(http.HandlerFunc(memoryHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/memories/ledger-history", Handler: protected(http.HandlerFunc(memoryHandler.LedgerHistory)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/memories/review-queue", Handler: protected(http.HandlerFunc(memoryHandler.ReviewQueue)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/memories/review-queue/:review_id", Handler: protected(http.HandlerFunc(memoryHandler.ReviewQueue)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories/review-queue/:review_id/resolve", Handler: protected(http.HandlerFunc(memoryHandler.ReviewQueue)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/memories/:memory_id/review", Handler: protected(http.HandlerFunc(memoryHandler.Review)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v3/memories/:memory_id/baseline", Handler: protected(http.HandlerFunc(memoryHandler.Baseline)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items", Handler: protected(http.HandlerFunc(actionHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items/pending-sync", Handler: protected(http.HandlerFunc(actionHandler.PendingSync)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items/ids", Handler: protected(http.HandlerFunc(actionHandler.IDs)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items/search", Handler: protected(http.HandlerFunc(actionHandler.Search)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/action-items/share", Handler: protected(http.HandlerFunc(actionHandler.Share)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items/shared/:token", Handler: http.HandlerFunc(actionHandler.Shared).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/action-items/accept", Handler: protected(http.HandlerFunc(actionHandler.AcceptShare)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/action-items", Handler: protected(http.HandlerFunc(actionHandler.Collection)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/speech-profile", Handler: protected(http.HandlerFunc(speechProfileHandler.Profile)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/speech-profile/stt-availability", Handler: protected(http.HandlerFunc(speechProfileHandler.Availability)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v4/speech-profile", Handler: protected(http.HandlerFunc(speechProfileHandler.Audio)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/speech-profile/status", Handler: protected(http.HandlerFunc(speechProfileHandler.Status)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v3/upload-audio", Handler: protected(http.HandlerFunc(speechProfileHandler.Upload)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v3/speech-profile/expand", Handler: protected(http.HandlerFunc(speechProfileHandler.Expand)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v3/speech-profile/expand", Handler: protected(http.HandlerFunc(speechProfileHandler.Expand)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v4/speech-profile/audio", Handler: protected(http.HandlerFunc(speechProfileHandler.ServeAudio)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/staged-tasks", Handler: protected(http.HandlerFunc(stagedHandler.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/staged-tasks", Handler: protected(http.HandlerFunc(stagedHandler.List)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/staged-tasks", Handler: protected(http.HandlerFunc(stagedHandler.Delete)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/staged-tasks/batch-scores", Handler: protected(http.HandlerFunc(stagedHandler.Scores)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/staged-tasks/promote", Handler: protected(http.HandlerFunc(stagedHandler.Promote)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/staged-tasks/migrate", Handler: protected(http.HandlerFunc(stagedHandler.Compatibility)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/staged-tasks/migrate-conversation-items", Handler: protected(http.HandlerFunc(stagedHandler.Compatibility)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/staged-tasks/:task_id", Handler: protected(http.HandlerFunc(stagedHandler.Delete)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/staged-tasks/:task_id/promote", Handler: protected(http.HandlerFunc(stagedHandler.Promote)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/work-intents", Handler: protected(http.HandlerFunc(workstreamHandler.Intent)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/workstreams", Handler: protected(http.HandlerFunc(workstreamHandler.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/workstreams/:workstream_id", Handler: protected(http.HandlerFunc(workstreamHandler.Detail)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/workstreams/:workstream_id", Handler: protected(http.HandlerFunc(workstreamHandler.Update)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/workstreams/:workstream_id/events", Handler: protected(http.HandlerFunc(workstreamHandler.Events)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/workstreams/:workstream_id/events", Handler: protected(http.HandlerFunc(workstreamHandler.Events)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/workstreams/:workstream_id/artifacts", Handler: protected(http.HandlerFunc(workstreamHandler.Artifacts)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/workstreams/:workstream_id/artifacts", Handler: protected(http.HandlerFunc(workstreamHandler.Artifacts)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/workstreams/:workstream_id/artifacts/:artifact_id/status", Handler: protected(http.HandlerFunc(workstreamHandler.ArtifactStatus)).ServeHTTP},
		{Method: http.MethodPut, Path: "/v1/workstreams/:workstream_id/checkpoints/:runtime_id", Handler: protected(http.HandlerFunc(workstreamHandler.Checkpoints)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/workstreams/:workstream_id/checkpoints", Handler: protected(http.HandlerFunc(workstreamHandler.Checkpoints)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/candidates", Handler: protected(http.HandlerFunc(candidateHandler.Create)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/candidates", Handler: protected(http.HandlerFunc(candidateHandler.List)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/candidates/migrate-staged", Handler: protected(http.HandlerFunc(candidateHandler.Migrate)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/candidates/control", Handler: protected(http.HandlerFunc(candidateHandler.Control)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/candidates/integrations/drain", Handler: protected(http.HandlerFunc(candidateHandler.Drain)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/candidates/:candidate_id", Handler: protected(http.HandlerFunc(candidateHandler.Detail)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/candidates/:candidate_id/:resolution", Handler: protected(http.HandlerFunc(candidateHandler.Resolve)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/action-items/batch", Handler: protected(http.HandlerFunc(actionHandler.Batch)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/action-items/batch", Handler: protected(http.HandlerFunc(actionHandler.Batch)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/action-items/sync-batch", Handler: protected(http.HandlerFunc(actionHandler.Batch)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/action-items/batch-delete", Handler: protected(http.HandlerFunc(actionHandler.Batch)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/action-items/:action_item_id", Handler: protected(http.HandlerFunc(actionHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/action-items/:action_item_id", Handler: protected(http.HandlerFunc(actionHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/action-items/:action_item_id/completed", Handler: protected(http.HandlerFunc(actionHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/action-items/:action_item_id", Handler: protected(http.HandlerFunc(actionHandler.Item)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations", Handler: protected(http.HandlerFunc(conversationHandler.Collection)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/joan/:memory_id/followup-question", Handler: protected(http.HandlerFunc(conversationHandler.Followup)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/count", Handler: protected(http.HandlerFunc(conversationHandler.Count)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations", Handler: protected(http.HandlerFunc(conversationHandler.Collection)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/search", Handler: protected(http.HandlerFunc(conversationHandler.Search)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/topic", Handler: protected(http.HandlerFunc(conversationHandler.Topic)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/from-segments", Handler: protected(http.HandlerFunc(conversationHandler.FromSegments)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/merge", Handler: protected(http.HandlerFunc(conversationHandler.Merge)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/analytics", Handler: protected(http.HandlerFunc(conversationHandler.Analytics)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/:conversation_id/test-prompt", Handler: protected(http.HandlerFunc(conversationHandler.TestPrompt)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/dev/user/conversations/from-segments", Handler: protected(http.HandlerFunc(conversationHandler.FromSegments)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id", Handler: protected(http.HandlerFunc(conversationHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id", Handler: protected(http.HandlerFunc(conversationHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/title", Handler: protected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { conversationHandler.Field(w, r, "title") })).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/summary", Handler: protected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { conversationHandler.Field(w, r, "summary") })).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/visibility", Handler: protected(http.HandlerFunc(conversationHandler.Item)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/starred", Handler: protected(http.HandlerFunc(conversationHandler.Item)).ServeHTTP},
		{Method: http.MethodDelete, Path: "/v1/conversations/:conversation_id", Handler: protected(http.HandlerFunc(conversationHandler.Item)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/:conversation_id/finalize", Handler: protected(http.HandlerFunc(conversationHandler.Finalize)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/finalization", Handler: protected(http.HandlerFunc(conversationHandler.Finalization)).ServeHTTP},
		{Method: http.MethodGet, Path: "/v1/conversations/:conversation_id/transcripts", Handler: protected(http.HandlerFunc(transcriptHandler.List)).ServeHTTP},
		{Method: http.MethodPost, Path: "/v1/conversations/:conversation_id/transcript-segments", Handler: protected(http.HandlerFunc(transcriptHandler.Create)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/segments/text", Handler: protected(http.HandlerFunc(transcriptHandler.UpdateText)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/segments/:segment_idx/assign", Handler: protected(http.HandlerFunc(transcriptHandler.AssignSegment)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/assign-speaker/:speaker_id", Handler: protected(http.HandlerFunc(transcriptHandler.AssignSpeaker)).ServeHTTP},
		{Method: http.MethodPatch, Path: "/v1/conversations/:conversation_id/segments/assign-bulk", Handler: protected(http.HandlerFunc(transcriptHandler.AssignBulk)).ServeHTTP},
	})
	return server
}

func retiredAgentVM(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusGone, "The cloud Agent VM has been retired and can no longer be provisioned.")
}

func openAIAppsChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(os.Getenv("OPENAI_APPS_CHALLENGE_TOKEN")))
}

func deprecatedEndpoint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":     "gone",
		"message":   fmt.Sprintf("This endpoint (%s %s) is deprecated and no longer served by the desktop backend. See https://api.omi.me for supported endpoints.", r.Method, r.URL.Path),
		"migration": "https://api.omi.me",
	})
}

func legacyMemorySummaryAnalytics(w http.ResponseWriter, r *http.Request) {
	// Python intentionally made the write side a no-op because this legacy UI
	// has been unreachable since 2025-04-11. Preserve that 200 contract for
	// old clients while keeping the read response shape stable.
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"has_rating": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}

func graphProvider(cfg config.Config) chat.Provider {
	if cfg.LLMEndpoint == "" {
		return chat.NullProvider{}
	}
	return chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
}

func desktopHealthHandler(ready bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ready {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[bool]string{true: "ready", false: "healthy"}[ready], "service": "omi-desktop-backend", "version": "0.1.0", "chat_contract_version": "1", "runtime_implementation": "go"})
	}
}
func apiKeysHandler(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for k, env := range map[string]string{"firebase_api_key": "FIREBASE_API_KEY", "google_calendar_api_key": "GOOGLE_CALENDAR_API_KEY", "anthropic_api_key": "DESKTOP_LEGACY_ANTHROPIC_KEY"} {
		if v := os.Getenv(env); v != "" {
			out[k] = v
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func hostFromAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "0.0.0.0"
	}
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			if i == 0 {
				return "0.0.0.0"
			}
			return addr[:i]
		}
	}
	return "0.0.0.0"
}

func portFromAddr(addr string) int {
	parts := strings.Split(addr, ":")
	value, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || value == 0 {
		return 8080
	}
	return value
}
