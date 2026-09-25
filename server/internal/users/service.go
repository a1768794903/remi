package users

import (
	"context"
	"errors"
	"strings"

	"remi/server/ent"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("user not found")

type Profile struct {
	UID      string `json:"uid"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Language string `json:"language"`
}
type UpdateInput struct {
	Name     *string `json:"name"`
	Language *string `json:"language"`
}
type Service struct{ Client *ent.Client }

type OnboardingState struct {
	Completed                 bool   `json:"completed"`
	AcquisitionSource         string `json:"acquisition_source"`
	DeviceOnboardingCompleted bool   `json:"device_onboarding_completed"`
}

type PrivateCloudSyncState struct {
	Enabled bool `json:"private_cloud_sync_enabled"`
}

type ScreenFrameSettings struct {
	Enabled bool `json:"meeting_note_screenshots_enabled"`
}

type StoreRecordingPermission struct {
	Enabled bool `json:"store_recording_permission"`
}

type NotificationSettings struct {
	Enabled   bool `json:"enabled"`
	Frequency int  `json:"frequency"`
}

type AIProfile struct {
	ProfileText     *string `json:"profile_text"`
	GeneratedAt     *string `json:"generated_at"`
	DataSourcesUsed *int    `json:"data_sources_used"`
}

type TranscriptionPreferences struct {
	SingleLanguageMode bool     `json:"single_language_mode"`
	Vocabulary         []string `json:"vocabulary"`
	Language           string   `json:"language"`
	UsesCustomSTT      bool     `json:"uses_custom_stt"`
}

func (s Service) Get(ctx context.Context, uid string) (Profile, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	return toProfile(node), nil
}
func (s Service) Ensure(ctx context.Context, uid string) (Profile, error) {
	profile, err := s.Get(ctx, uid)
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Profile{}, err
	}
	node, err := s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	if err != nil {
		return Profile{}, err
	}
	return toProfile(node), nil
}
func (s Service) Update(ctx context.Context, uid string, input UpdateInput) (Profile, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	b := s.Client.User.UpdateOneID(node.ID)
	if input.Name != nil {
		b.SetName(strings.TrimSpace(*input.Name))
	}
	if input.Language != nil {
		language := strings.TrimSpace(*input.Language)
		if language == "" || len(language) > 16 {
			return Profile{}, errors.New("invalid language")
		}
		b.SetLanguage(language)
	}
	updated, err := b.Save(ctx)
	if err != nil {
		return Profile{}, err
	}
	return toProfile(updated), nil
}

func (s Service) TranscriptionPreferences(ctx context.Context, uid string) (TranscriptionPreferences, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return TranscriptionPreferences{}, ErrNotFound
	}
	if err != nil {
		return TranscriptionPreferences{}, err
	}
	p := TranscriptionPreferences{Vocabulary: []string{}, Language: node.Language}
	if node.Integrations != nil {
		if raw, ok := node.Integrations["transcription_preferences"].(map[string]any); ok {
			if v, ok := raw["single_language_mode"].(bool); ok {
				p.SingleLanguageMode = v
			}
			if v, ok := raw["vocabulary"].([]any); ok {
				for _, x := range v {
					if s, ok := x.(string); ok {
						p.Vocabulary = append(p.Vocabulary, s)
					}
				}
			}
		}
	}
	return p, nil
}

func (s Service) UpdateTranscriptionPreferences(ctx context.Context, uid string, single *bool, vocabulary *[]string) error {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	p, err := s.TranscriptionPreferences(ctx, uid)
	if err != nil {
		return err
	}
	if single != nil {
		p.SingleLanguageMode = *single
	}
	if vocabulary != nil {
		if len(*vocabulary) > 100 {
			return errors.New("vocabulary may contain at most 100 items")
		}
		p.Vocabulary = *vocabulary
	}
	merged := cloneMap(node.Integrations)
	merged["transcription_preferences"] = map[string]any{"single_language_mode": p.SingleLanguageMode, "vocabulary": p.Vocabulary}
	_, err = s.Client.User.UpdateOneID(node.ID).SetIntegrations(merged).Save(ctx)
	return err
}

func (s Service) NotificationSettings(ctx context.Context, uid string) (NotificationSettings, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return NotificationSettings{}, ErrNotFound
	}
	if err != nil {
		return NotificationSettings{}, err
	}
	return notificationSettings(node), nil
}

func (s Service) UpdateNotificationSettings(ctx context.Context, uid string, enabled *bool, frequency *int) (NotificationSettings, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return NotificationSettings{}, ErrNotFound
	}
	if err != nil {
		return NotificationSettings{}, err
	}
	settings := notificationSettings(node)
	if enabled != nil {
		settings.Enabled = *enabled
	}
	if frequency != nil {
		if *frequency < 0 || *frequency > 5 {
			return NotificationSettings{}, errors.New("frequency must be between 0 and 5")
		}
		settings.Frequency = *frequency
	}
	updated, err := s.Client.User.UpdateOneID(node.ID).SetNotificationSettings(map[string]any{"enabled": settings.Enabled, "frequency": settings.Frequency}).Save(ctx)
	if err != nil {
		return NotificationSettings{}, err
	}
	return notificationSettings(updated), nil
}

func (s Service) AssistantSettings(ctx context.Context, uid string) (map[string]any, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if node.AssistantSettings == nil {
		return map[string]any{}, nil
	}
	return node.AssistantSettings, nil
}

func (s Service) UpdateAssistantSettings(ctx context.Context, uid string, patch map[string]any) (map[string]any, error) {
	current, err := s.AssistantSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	merged := cloneMap(current)
	for key, value := range patch {
		if key == "update_channel" {
			if text, ok := value.(string); !ok || len(text) > 50 {
				return nil, errors.New("invalid update_channel")
			}
		}
		merged[key] = value
	}
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return nil, err
	}
	updated, err := s.Client.User.UpdateOneID(node.ID).SetAssistantSettings(merged).Save(ctx)
	if err != nil {
		return nil, err
	}
	return updated.AssistantSettings, nil
}

func (s Service) AIProfile(ctx context.Context, uid string) (AIProfile, bool, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return AIProfile{}, false, ErrNotFound
	}
	if err != nil {
		return AIProfile{}, false, err
	}
	return decodeAIProfile(node.AiProfile), node.AiProfile != nil, nil
}

func (s Service) Onboarding(ctx context.Context, uid string) (OnboardingState, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return OnboardingState{}, ErrNotFound
	}
	if err != nil {
		return OnboardingState{}, err
	}
	state := OnboardingState{}
	state = decodeOnboarding(node.Onboarding)
	return state, nil
}

func decodeOnboarding(value map[string]any) OnboardingState {
	state := OnboardingState{}
	if value == nil {
		return state
	}
	if completed, ok := value["completed"].(bool); ok {
		state.Completed = completed
	}
	if source, ok := value["acquisition_source"].(string); ok {
		state.AcquisitionSource = source
	}
	if completed, ok := value["device_onboarding_completed"].(bool); ok {
		state.DeviceOnboardingCompleted = completed
	}
	return state
}

func (s Service) UpdateOnboarding(ctx context.Context, uid string, input map[string]any) error {
	state, err := s.Onboarding(ctx, uid)
	if err != nil {
		return err
	}
	if value, ok := input["completed"].(bool); ok {
		state.Completed = value
	}
	if value, ok := input["acquisition_source"].(string); ok {
		state.AcquisitionSource = value
	}
	if value, ok := input["device_onboarding_completed"].(bool); ok {
		state.DeviceOnboardingCompleted = value
	}
	_, err = s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return err
	}
	_, err = s.Client.User.Update().Where(user.ExternalUIDEQ(uid)).SetOnboarding(map[string]any{
		"completed":                   state.Completed,
		"acquisition_source":          state.AcquisitionSource,
		"device_onboarding_completed": state.DeviceOnboardingCompleted,
	}).Save(ctx)
	return err
}

func (s Service) PrivateCloudSync(ctx context.Context, uid string) (PrivateCloudSyncState, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return PrivateCloudSyncState{}, ErrNotFound
	}
	if err != nil {
		return PrivateCloudSyncState{}, err
	}
	return PrivateCloudSyncState{Enabled: node.PrivateCloudSyncEnabled}, nil
}

func (s Service) SetPrivateCloudSync(ctx context.Context, uid string, enabled bool) error {
	_, err := s.Client.User.Update().Where(user.ExternalUIDEQ(uid)).SetPrivateCloudSyncEnabled(enabled).Save(ctx)
	return err
}

func (s Service) ScreenFrameSettings(ctx context.Context, uid string) (ScreenFrameSettings, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return ScreenFrameSettings{}, ErrNotFound
	}
	if err != nil {
		return ScreenFrameSettings{}, err
	}
	return ScreenFrameSettings{Enabled: node.MeetingNoteScreenshotsEnabled}, nil
}

func (s Service) SetScreenFrameSettings(ctx context.Context, uid string, enabled bool) (ScreenFrameSettings, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return ScreenFrameSettings{}, ErrNotFound
	}
	if err != nil {
		return ScreenFrameSettings{}, err
	}
	updated, err := s.Client.User.UpdateOneID(node.ID).SetMeetingNoteScreenshotsEnabled(enabled).Save(ctx)
	if err != nil {
		return ScreenFrameSettings{}, err
	}
	return ScreenFrameSettings{Enabled: updated.MeetingNoteScreenshotsEnabled}, nil
}

func (s Service) StoreRecordingPermission(ctx context.Context, uid string) (StoreRecordingPermission, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return StoreRecordingPermission{}, ErrNotFound
	}
	if err != nil {
		return StoreRecordingPermission{}, err
	}
	return StoreRecordingPermission{Enabled: node.StoreRecordingPermission}, nil
}

func (s Service) SetStoreRecordingPermission(ctx context.Context, uid string, enabled bool) error {
	_, err := s.Client.User.Update().Where(user.ExternalUIDEQ(uid)).SetStoreRecordingPermission(enabled).Save(ctx)
	return err
}

func (s Service) UpdateAIProfile(ctx context.Context, uid string, profile AIProfile) (AIProfile, error) {
	if profile.ProfileText != nil && len(*profile.ProfileText) > 50000 {
		return AIProfile{}, errors.New("profile_text is too long")
	}
	if profile.DataSourcesUsed != nil && *profile.DataSourcesUsed < 0 {
		return AIProfile{}, errors.New("data_sources_used must be non-negative")
	}
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return AIProfile{}, err
	}
	value := map[string]any{}
	if profile.ProfileText != nil {
		value["profile_text"] = *profile.ProfileText
	}
	if profile.GeneratedAt != nil {
		value["generated_at"] = *profile.GeneratedAt
	}
	if profile.DataSourcesUsed != nil {
		value["data_sources_used"] = *profile.DataSourcesUsed
	}
	updated, err := s.Client.User.UpdateOneID(node.ID).SetAiProfile(value).Save(ctx)
	if err != nil {
		return AIProfile{}, err
	}
	return decodeAIProfile(updated.AiProfile), nil
}

func cloneMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func decodeAIProfile(value map[string]any) AIProfile {
	var result AIProfile
	if value == nil {
		return result
	}
	if text, ok := value["profile_text"].(string); ok {
		result.ProfileText = &text
	}
	if text, ok := value["generated_at"].(string); ok {
		result.GeneratedAt = &text
	}
	if number, ok := value["data_sources_used"].(float64); ok {
		n := int(number)
		result.DataSourcesUsed = &n
	}
	if number, ok := value["data_sources_used"].(int); ok {
		result.DataSourcesUsed = &number
	}
	return result
}

func notificationSettings(node *ent.User) NotificationSettings {
	settings := NotificationSettings{Enabled: true}
	if node.NotificationSettings != nil {
		if value, ok := node.NotificationSettings["enabled"].(bool); ok {
			settings.Enabled = value
		}
		if value, ok := node.NotificationSettings["frequency"].(float64); ok {
			settings.Frequency = int(value)
		}
		if value, ok := node.NotificationSettings["frequency"].(int); ok {
			settings.Frequency = value
		}
	}
	return settings
}
func toProfile(node *ent.User) Profile {
	return Profile{UID: node.ExternalUID, Email: node.Email, Name: node.Name, Language: node.Language}
}
