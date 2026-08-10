package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// Административные команды (Фактор 12).
//
// ЗАЧЕМ ОНИ ЖИВУТ В ТОМ ЖЕ БИНАРНИКЕ
//
// Соблазн — написать разовый скрипт на bash/python и запускать его
// с ноутбука. Так делают, и это ровно то, что Фактор 12 запрещает:
//
//   * скрипт использует ДРУГОЙ доступ к базе (часто из-под суперпользователя),
//     другую версию драйвера и другие представления о схеме;
//   * он не версионируется вместе с кодом и отстаёт от него;
//   * он выполняется на машине человека, а не в окружении приложения,
//     то есть с другими переменными, сетью и правами.
//
// Команда внутри того же бинарника получает ту же конфигурацию из тех же
// переменных окружения, тот же пул, те же миграции и ту же версию.
// Запускается она через `kubectl exec` в работающий под или отдельным Job
// из того же образа — в обоих случаях окружение идентично боевому.

func runAdmin(ctx context.Context, dsn string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("укажите команду: stats | answers")
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	switch args[0] {
	case "stats":
		return adminStats(ctx, conn)
	case "answers":
		return adminAnswers(ctx, conn)
	default:
		return fmt.Errorf("неизвестная команда %q", args[0])
	}
}

// adminStats — сводка по данным.
//
// Вывод в JSON, а не таблицей: административную команду часто запускают
// из скрипта или из CI, и разбирать её вывод должно быть можно машиной.
// Человек прочитает и JSON, а вот jq по таблице не сделаешь.
func adminStats(ctx context.Context, conn *pgx.Conn) error {
	var stats struct {
		Users           int64      `json:"users"`
		UsersWithTG     int64      `json:"users_with_telegram"`
		Answers         int64      `json:"answers"`
		LastAnswerAt    *time.Time `json:"last_answer_at"`
		SchemaVersion   *int64     `json:"schema_version"`
		SchemaAppliedAt *time.Time `json:"schema_applied_at"`
	}

	const q = `
		SELECT
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM users WHERE telegram_id IS NOT NULL),
			(SELECT count(*) FROM mood_answers),
			(SELECT max(created_at) FROM mood_answers),
			(SELECT max(version) FROM schema_migrations),
			(SELECT max(applied_at) FROM schema_migrations)`

	if err := conn.QueryRow(ctx, q).Scan(
		&stats.Users, &stats.UsersWithTG, &stats.Answers,
		&stats.LastAnswerAt, &stats.SchemaVersion, &stats.SchemaAppliedAt,
	); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(stats)
}

// adminAnswers — последние ответы. Пример «посмотреть данные глазами»
// без выдачи кому-либо доступа к самой базе.
func adminAnswers(ctx context.Context, conn *pgx.Conn) error {
	const q = `
		SELECT u.name, a.answer, a.created_at
		FROM mood_answers a JOIN users u ON u.id = a.user_id
		ORDER BY a.created_at DESC LIMIT 20`

	rows, err := conn.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		Name      string    `json:"name"`
		Answer    string    `json:"answer"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Name, &r.Answer, &r.CreatedAt); err != nil {
			return err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
