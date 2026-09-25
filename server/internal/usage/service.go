package usage

import (
	"context"
	"database/sql"
	"time"
)

type Service struct{ DB *sql.DB }

func (s Service) RecordChat(ctx context.Context, uid string, costMicroUSD int64) error {
	return s.RecordChatDetailed(ctx, uid, 0, 0, costMicroUSD)
}

func (s Service) RecordChatDetailed(ctx context.Context, uid string, inputTokens, outputTokens, costMicroUSD int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO llm_usage (user_external_uid,allocation,feature,input_tokens,output_tokens,questions,cost_micro_usd,created_at) VALUES (?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, uid, "chat", "chat", inputTokens, outputTokens, 1, costMicroUSD)
	return err
}

type Snapshot struct {
	Questions int
	CostUSD   float64
	ResetAt   int64
}

func (s Service) MonthlyChat(ctx context.Context, uid string, now time.Time) (Snapshot, error) {
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	var questions int
	var costMicro int64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(questions),0),COALESCE(SUM(cost_micro_usd),0) FROM llm_usage WHERE user_external_uid=? AND allocation='chat' AND created_at>=?`, uid, start).Scan(&questions, &costMicro)
	if err != nil {
		return Snapshot{}, err
	}
	next := start.AddDate(0, 1, 0).Unix()
	return Snapshot{Questions: questions, CostUSD: float64(costMicro) / 1_000_000, ResetAt: next}, nil
}
