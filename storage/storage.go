// Package storage — работа с PostgreSQL.
//
// Фактор 4 (Backing services): база — подключаемый ресурс. Код здесь знает
// только строку подключения; кто именно на том конце — контейнер в kind,
// managed-инстанс Яндекс.Облака или CloudSQL — ему безразлично.
package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — доменная ошибка «нет такой записи».
//
// Почему не отдаём наружу pgx.ErrNoRows: слой HTTP не должен знать,
// что под ним Postgres. Иначе замена базы (тот самый Фактор 4) потянет
// правки в хендлерах.
var ErrNotFound = errors.New("not found")

// ErrConflict — нарушение уникальности (например, дубль telegram_id).
var ErrConflict = errors.New("conflict")

type User struct {
	ID         int64     `json:"id"`
	TelegramID *int64    `json:"telegram_id,omitempty"`
	Name       string    `json:"name"`
	Email      string    `json:"email,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

// New открывает пул соединений.
//
// Важно: pgxpool.New НЕ подключается к базе немедленно — соединение ленивое.
// Это ровно то поведение, которое нужно в Kubernetes: под должен подняться,
// даже если Postgres в этот момент ещё не готов, и стать ready позже,
// когда база появится. Обратный вариант (падать на старте, если база
// недоступна) даёт CrashLoopBackOff всего приложения из-за перезапуска базы.
func New(ctx context.Context, dsn string, maxConns int32) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	// MaxConnLifetime: соединения периодически пересоздаются. Зачем — чтобы
	// после failover'а базы или смены DNS у Service клиент не держал вечно
	// соединение к исчезнувшему адресу.
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Ping — для readiness-пробы. Отдельный метод, чтобы HTTP-слой не лез в пул.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

const userCols = `id, telegram_id, name, email, created_at`

func (s *Store) Create(ctx context.Context, u User) (User, error) {
	// $1/$2/... — параметризованный запрос, а не конкатенация строк.
	// Это единственная реальная защита от SQL-инъекций: значение уходит
	// отдельно от текста запроса и никогда не парсится как SQL.
	const q = `INSERT INTO users (telegram_id, name, email)
	           VALUES ($1, $2, $3)
	           RETURNING ` + userCols

	row := s.pool.QueryRow(ctx, q, u.TelegramID, u.Name, u.Email)
	out, err := scanUser(row)
	if err != nil {
		return User{}, wrapPGError(err)
	}
	return out, nil
}

func (s *Store) List(ctx context.Context, limit, offset int) ([]User, error) {
	// LIMIT/OFFSET, а не «SELECT * FROM users». Ручка без пагинации —
	// это мина: она работает на 10 строках в тесте и кладёт сервис
	// на 10 миллионах в проде.
	const q = `SELECT ` + userCols + ` FROM users ORDER BY id LIMIT $1 OFFSET $2`

	rows, err := s.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, wrapPGError(err)
	}
	defer rows.Close()

	out := make([]User, 0, limit)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, wrapPGError(err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (User, error) {
	const q = `SELECT ` + userCols + ` FROM users WHERE id = $1`
	u, err := scanUser(s.pool.QueryRow(ctx, q, id))
	if err != nil {
		return User{}, wrapPGError(err)
	}
	return u, nil
}

// GetByTelegramID понадобится сервису telegram-api: он получает апдейт
// от Telegram, где есть chat_id, и должен найти по нему пользователя.
func (s *Store) GetByTelegramID(ctx context.Context, tgID int64) (User, error) {
	const q = `SELECT ` + userCols + ` FROM users WHERE telegram_id = $1`
	u, err := scanUser(s.pool.QueryRow(ctx, q, tgID))
	if err != nil {
		return User{}, wrapPGError(err)
	}
	return u, nil
}

func (s *Store) Update(ctx context.Context, id int64, u User) (User, error) {
	const q = `UPDATE users
	           SET name = $2, email = $3, telegram_id = COALESCE($4, telegram_id)
	           WHERE id = $1
	           RETURNING ` + userCols
	out, err := scanUser(s.pool.QueryRow(ctx, q, id, u.Name, u.Email, u.TelegramID))
	if err != nil {
		return User{}, wrapPGError(err)
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	const q = `DELETE FROM users WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return wrapPGError(err)
	}
	// DELETE не возвращает ошибку, если ничего не удалил, — поэтому
	// различие «удалили» / «не было такого» приходится делать по RowsAffected.
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanUser работает и с pgx.Row, и с pgx.Rows — у обоих есть Scan.
type scannable interface {
	Scan(dest ...any) error
}

func scanUser(r scannable) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.TelegramID, &u.Name, &u.Email, &u.CreatedAt)
	return u, err
}

// wrapPGError переводит ошибки драйвера в доменные.
func wrapPGError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 23505 = unique_violation. Коды SQLSTATE стабильны и переносимы
		// между версиями Postgres — в отличие от текста сообщения,
		// который зависит от локали сервера.
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.ConstraintName)
		case "23503": // foreign_key_violation — ссылка на несуществующего юзера
			return fmt.Errorf("%w: %s", ErrNotFound, pgErr.ConstraintName)
		}
	}
	return err
}
