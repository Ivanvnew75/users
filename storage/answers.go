package storage

import (
	"context"
	"time"
)

// MoodAnswer — ответ пользователя на вопрос бота.
type MoodAnswer struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	CreatedAt time.Time `json:"created_at"`
}

// AddAnswer сохраняет ответ.
//
// Внешний ключ на users уже проверит существование пользователя:
// если user_id не существует, Postgres вернёт SQLSTATE 23503
// (foreign_key_violation). Отдельный SELECT «а есть ли такой юзер»
// перед вставкой был бы и лишним запросом, и гонкой — между проверкой
// и вставкой пользователя могут удалить.
func (s *Store) AddAnswer(ctx context.Context, userID int64, question, answer string) (MoodAnswer, error) {
	const q = `INSERT INTO mood_answers (user_id, question, answer)
	           VALUES ($1, $2, $3)
	           RETURNING id, user_id, question, answer, created_at`

	var a MoodAnswer
	err := s.pool.QueryRow(ctx, q, userID, question, answer).
		Scan(&a.ID, &a.UserID, &a.Question, &a.Answer, &a.CreatedAt)
	if err != nil {
		return MoodAnswer{}, wrapPGError(err)
	}
	return a, nil
}

// ListAnswers отдаёт последние ответы пользователя.
// Порядок и фильтр совпадают с индексом mood_answers_user_created_idx
// (user_id, created_at DESC) — запрос идёт по индексу, без сортировки в памяти.
func (s *Store) ListAnswers(ctx context.Context, userID int64, limit int) ([]MoodAnswer, error) {
	const q = `SELECT id, user_id, question, answer, created_at
	           FROM mood_answers
	           WHERE user_id = $1
	           ORDER BY created_at DESC
	           LIMIT $2`

	rows, err := s.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, wrapPGError(err)
	}
	defer rows.Close()

	out := make([]MoodAnswer, 0, limit)
	for rows.Next() {
		var a MoodAnswer
		if err := rows.Scan(&a.ID, &a.UserID, &a.Question, &a.Answer, &a.CreatedAt); err != nil {
			return nil, wrapPGError(err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
