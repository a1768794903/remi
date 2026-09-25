package main

import (
	"context"
	"log"
	"os"
	"strings"

	"remi/server/internal/account"
	"remi/server/internal/actionitems"
	"remi/server/internal/api"
	"remi/server/internal/audio"
	"remi/server/internal/audioplayback"
	"remi/server/internal/automodel"
	"remi/server/internal/calendarmeetings"
	"remi/server/internal/candidates"
	"remi/server/internal/chat"
	"remi/server/internal/chatfiles"
	"remi/server/internal/config"
	"remi/server/internal/conversations"
	"remi/server/internal/csat"
	"remi/server/internal/dailysummaries"
	"remi/server/internal/desktopusage"
	"remi/server/internal/focussessions"
	"remi/server/internal/folders"
	"remi/server/internal/framerequests"
	"remi/server/internal/goals"
	"remi/server/internal/integrations"
	"remi/server/internal/memories"
	"remi/server/internal/mobilefeedback"
	"remi/server/internal/notifications"
	"remi/server/internal/people"
	"remi/server/internal/realtime"
	"remi/server/internal/referrals"
	"remi/server/internal/scores"
	"remi/server/internal/screenactivity"
	"remi/server/internal/speechprofile"
	"remi/server/internal/stagedtasks"
	"remi/server/internal/staticmap"
	"remi/server/internal/storage"
	"remi/server/internal/stt"
	"remi/server/internal/syncjobs"
	"remi/server/internal/transcripts"
	"remi/server/internal/trends"
	"remi/server/internal/tts"
	"remi/server/internal/usage"
	"remi/server/internal/users"
	ws "remi/server/internal/websocket"
	"remi/server/internal/workstreams"
)

func main() {
	cfg := config.FromEnv()
	connections, err := storage.Open(cfg.MySQLDSN, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer connections.Close()
	tracker := audio.NewTracker()
	conversationService := conversations.Service{Client: connections.Ent, DB: connections.MySQL}
	audioHandler := ws.NewHandler(tracker, conversationService)
	audioHandler.SegmentStore = transcripts.Service{Client: connections.Ent}
	if cfg.STTEndpoint != "" {
		audioHandler.STT = stt.HTTPProvider{Endpoint: strings.TrimRight(cfg.STTEndpoint, "/") + "/v2/transcribe", APIKey: cfg.STTAPIKey}
	} else {
		audioHandler.STT = stt.NullProvider{}
	}
	provider := stt.Provider(stt.NullProvider{})
	if cfg.STTEndpoint != "" {
		provider = stt.HTTPProvider{Endpoint: strings.TrimRight(cfg.STTEndpoint, "/") + "/v2/transcribe", APIKey: cfg.STTAPIKey}
	}
	// Dev keeps the local header contract. Production-like modes validate
	// Firebase Secure Token JWTs and derive the business UID from `sub`.
	chatProvider := chat.Provider(chat.NullProvider{})
	if cfg.LLMEndpoint != "" {
		chatProvider = chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
	}
	chatService := chat.Service{Client: connections.Ent, DB: connections.MySQL, Provider: chatProvider, Usage: usage.Service{DB: connections.MySQL}}
	syncQueue := syncjobs.Queue{Client: connections.Redis, Jobs: connections.Ent}
	autoService := &automodel.Service{}
	var thumbnailStore chatfiles.ThumbnailStore
	var closeThumbnailStore func() error
	if bucket := os.Getenv("BUCKET_CHAT_FILES"); bucket != "" {
		var storeErr error
		thumbnailStore, closeThumbnailStore, storeErr = chatfiles.NewGCSStore(context.Background(), bucket)
		if storeErr != nil {
			log.Printf("GCS chat thumbnail store unavailable: %v", storeErr)
		}
	}
	if closeThumbnailStore != nil {
		defer closeThumbnailStore()
	}
	frameStore, frameStoreErr := framerequests.NewGCSStore(context.Background(), os.Getenv("BUCKET_FRAME_REQUESTS"))
	if frameStoreErr != nil {
		log.Printf("GCS frame-request store unavailable; using local storage: %v", frameStoreErr)
	}
	if frameStore != nil {
		defer frameStore.Close()
	}
	server := api.BuildServer(cfg, connections.MySQL, connections.Redis, audioHandler, actionitems.Handler{Service: actionitems.Service{Client: connections.Ent, Redis: connections.Redis}}, conversations.Handler{Service: conversationService, Queue: conversations.RedisQueue{Client: connections.Redis}, Transcripts: transcripts.Service{Client: connections.Ent}, Provider: chatProvider}, transcripts.Handler{Service: transcripts.Service{Client: connections.Ent}}, stt.Handler{Provider: provider}, memories.Handler{Service: memories.Service{Client: connections.Ent}, DB: connections.MySQL}, users.Handler{Service: users.Service{Client: connections.Ent}, Redis: connections.Redis, DB: connections.MySQL}, chat.Handler{Service: chatService}, chat.SessionsHandler{Service: chat.SessionService{Client: connections.Ent, Provider: chatProvider}}, chat.DesktopHandler{Service: chatService}, notifications.Handler{Service: notifications.Service{Client: connections.Ent}}, folders.Handler{Service: folders.Service{Client: connections.Ent}}, goals.Handler{Service: goals.Service{Client: connections.Ent}}, integrations.Handler{Service: integrations.Service{Client: connections.Ent, Redis: connections.Redis}}, account.Handler{Service: account.Service{Client: connections.Ent}, Wipe: account.WipeService{DB: connections.MySQL, Client: connections.Ent}}, syncjobs.Handler{Queue: syncQueue, Conversations: conversationService, Audio: audioplayback.Service{Conversations: conversationService}}, scores.Handler{Service: scores.Service{Client: connections.Ent}}, calendarmeetings.Handler{Service: calendarmeetings.Service{Client: connections.Ent}}, automodel.Handler{Service: autoService}, staticmap.Handler{Service: staticmap.Service{Redis: connections.Redis}}, csat.Handler{Service: csat.Service{Client: connections.Ent}}, chatfiles.Handler{Service: chatfiles.Service{Client: connections.Ent, ThumbnailStore: thumbnailStore}}, tts.Handler{APIKey: cfg.OpenAITTSKey, Endpoint: cfg.OpenAITTSEndpoint}, realtime.Handler{DB: connections.MySQL, OpenAIKey: os.Getenv("OPENAI_API_KEY"), GeminiKey: os.Getenv("GEMINI_API_KEY")}, trends.Handler{Service: trends.Service{DB: connections.MySQL}}, dailysummaries.Handler{Service: dailysummaries.Service{DB: connections.MySQL, Redis: connections.Redis}, Provider: chatProvider}, focussessions.Handler{Service: focussessions.Service{DB: connections.MySQL}}, screenactivity.Handler{Service: screenactivity.Service{DB: connections.MySQL}}, people.Handler{Service: people.Service{DB: connections.MySQL}}, desktopusage.Handler{Service: desktopusage.Service{DB: connections.MySQL}}, thumbnailStore, stagedtasks.Handler{DB: connections.MySQL, Actions: actionitems.Service{Client: connections.Ent, Redis: connections.Redis}}, workstreams.Handler{DB: connections.MySQL}, candidates.Handler{DB: connections.MySQL, Actions: actionitems.Service{Client: connections.Ent, Redis: connections.Redis}}, speechprofile.Handler{DB: connections.MySQL, Embed: speechprofile.HTTPEmbedder(os.Getenv("HOSTED_SPEAKER_EMBEDDING_API_URL"), os.Getenv("HOSTED_SPEAKER_EMBEDDING_API_KEY"))}, referrals.Handler{DB: connections.MySQL}, mobilefeedback.Handler{DB: connections.MySQL}, framerequests.Handler{DB: connections.MySQL, Store: frameStore})
	defer server.Stop()
	log.Printf("Remi API listening on %s", cfg.HTTPAddr)
	server.Start()
}
