package csat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"remi/server/ent"
	"remi/server/ent/csatrating"
	"remi/server/ent/user"
)

var platforms = map[string]bool{"macos": true, "windows": true, "ios": true, "android": true}

type Config struct {
	Enabled           bool   `json:"enabled"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	ThankYouText      string `json:"thank_you_text"`
	ReferCTAText      string `json:"refer_cta_text"`
	QuestionThreshold int    `json:"question_threshold"`
	CommentMaxScore   int    `json:"comment_max_score"`
	Revision          int    `json:"revision"`
}

var defaultConfig = Config{Enabled: true, Title: "How would you rate Omi Desktop?", ThankYouText: "Thank you!", ReferCTAText: "Enjoying Omi? Give a friend a free month.", QuestionThreshold: 3, CommentMaxScore: 3, Revision: 0}

func normalizeConfig(raw map[string]any) Config {
	c := defaultConfig
	if v, ok := raw["enabled"].(bool); ok {
		c.Enabled = v
	}
	if v, ok := raw["title"].(string); ok && strings.TrimSpace(v) != "" {
		c.Title = strings.TrimSpace(v)
	}
	if v, ok := raw["body"].(string); ok {
		c.Body = strings.TrimSpace(v)
	}
	if v, ok := raw["thank_you_text"].(string); ok && strings.TrimSpace(v) != "" {
		c.ThankYouText = strings.TrimSpace(v)
	}
	if v, ok := raw["refer_cta_text"].(string); ok && strings.TrimSpace(v) != "" {
		c.ReferCTAText = strings.TrimSpace(v)
	}
	if v, ok := raw["question_threshold"].(int); ok {
		c.QuestionThreshold = clamp(v, 1, 50, 3)
	}
	if v, ok := raw["comment_max_score"].(int); ok {
		c.CommentMaxScore = clamp(v, 1, 5, 3)
	}
	if v, ok := raw["revision"].(int); ok && v >= 0 {
		c.Revision = v
	}
	return c
}
func clamp(v, low, high, def int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
func validateRating(platform string, score, revision int) error {
	if !platforms[platform] {
		return errors.New("invalid platform")
	}
	if score < 1 || score > 5 {
		return errors.New("score must be between 1 and 5")
	}
	if revision < 0 {
		return errors.New("revision must be >= 0")
	}
	return nil
}

type Service struct{ Client *ent.Client }

func (s Service) Config(context.Context) Config { return defaultConfig }
func (s Service) Submit(ctx context.Context, uid, platform, version, comment string, score, revision int) (string, bool, error) {
	if err := validateRating(platform, score, revision); err != nil {
		return "", false, err
	}
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		u, err = s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256([]byte(uid + "\x00" + platform))
	id := hex.EncodeToString(sum[:])
	exists, e := s.Client.CsatRating.Query().Where(csatrating.ExternalIDEQ(id)).Exist(ctx)
	if e != nil {
		return "", false, e
	}
	exists2, e := s.Client.CsatRating.Query().Where(csatrating.HasUserWith(user.IDEQ(u.ID)), csatrating.PlatformEQ(platform)).Exist(ctx)
	if e != nil {
		return "", false, e
	}
	if exists || exists2 {
		return id, false, nil
	}
	if score > defaultConfig.CommentMaxScore {
		comment = ""
	}
	if len(version) > 32 {
		version = version[:32]
	}
	if len(comment) > 500 {
		comment = comment[:500]
	}
	_, err = s.Client.CsatRating.Create().SetExternalID(id).SetPlatform(platform).SetAppVersion(version).SetScore(score).SetComment(comment).SetRevision(maxInt(revision, 0)).SetUserID(u.ID).Save(ctx)
	if ent.IsConstraintError(err) {
		return id, false, nil
	}
	return id, true, err
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
