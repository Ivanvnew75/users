package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Миграции ВСТРОЕНЫ в бинарник через embed.
//
// ФАКТОР 12 (Admin processes) требует, чтобы разовая задача выполнялась
// «в идентичном окружении и на том же релизе», что и обычные процессы.
// embed делает это буквально: SQL-файлы становятся частью того же образа,
// который катится в прод. Отсюда невозможна классическая беда —
// «код выкатили новый, а миграции забыли/накатили не те».
//
// Альтернатива (положить .sql в ConfigMap или тянуть из git в Job)
// разрывает эту связь: версия схемы начинает жить отдельно от версии кода.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// advisoryLockID — произвольное, но ЗАФИКСИРОВАННОЕ число.
//
// pg_advisory_lock — блокировка на уровне сессии Postgres, не связанная
// ни с какой таблицей. Здесь она решает конкретную проблему: если Job
// по какой-то причине запустится дважды (ретрай, гонка при выкатке,
// человек руками), два процесса не должны накатывать миграции
// одновременно. Второй просто подождёт первого и увидит, что всё уже
// применено.
//
// Почему именно advisory lock, а не «блокировка таблицы»: он берётся
// ДО того, как таблицы вообще существуют, и не мешает обычной работе.
const advisoryLockID = 4812001

// runMigrations накатывает все непринятые миграции.
func runMigrations(ctx context.Context, dsn string, log *slog.Logger) error {
	// Отдельное ОДИНОЧНОЕ соединение, а не пул.
	//
	// Это принципиально: advisory lock и временные настройки живут
	// в пределах СЕССИИ. Через пул следующий запрос может уйти
	// в другое соединение, и блокировка окажется взята не там,
	// где выполняется миграция.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	log.Info("беру advisory lock", slog.Int("lock_id", advisoryLockID))
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		// Освобождать явно, хотя закрытие соединения снимет блокировку само.
		// Явное снятие делает поведение очевидным при чтении кода.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()

	// Таблица учёта. Без неё «накатить только новое» невозможно,
	// и приходится полагаться на IF NOT EXISTS в каждой миграции —
	// что не работает для ALTER, UPDATE и вообще для всего интересного.
	const createTable = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     BIGINT PRIMARY KEY,
			name        TEXT        NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`
	if _, err := conn.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	files, err := migrationFiles()
	if err != nil {
		return err
	}

	pending := 0
	for _, m := range files {
		if applied[m.version] {
			continue
		}
		pending++

		body, err := fs.ReadFile(migrationFS, m.path)
		if err != nil {
			return fmt.Errorf("read %s: %w", m.path, err)
		}

		// Ключ migration_version, а не version: у логгера уже есть поле
		// version (версия сборки). Дублирующиеся ключи в JSON-логе —
		// это не «просто некрасиво»: спецификация JSON не запрещает дубли,
		// но парсеры разрешают их по-разному (кто берёт первый, кто последний),
		// и поле в хранилище оказывается не тем, что ожидали.
		log.Info("применяю миграцию",
			slog.Int64("migration_version", m.version), slog.String("name", m.name))

		// Каждая миграция — В ОДНОЙ ТРАНЗАКЦИИ вместе с записью в
		// schema_migrations. Иначе возможен разрыв: SQL применился,
		// процесс упал до отметки — и при следующем запуске миграция
		// накатится повторно.
		//
		// Оговорка: в PostgreSQL DDL транзакционен (в отличие от MySQL),
		// поэтому такой подход здесь работает целиком. На MySQL пришлось бы
		// городить компенсации.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations(version, name) VALUES ($1, $2)",
			m.version, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", m.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", m.name, err)
		}
	}

	if pending == 0 {
		log.Info("новых миграций нет", slog.Int("applied_total", len(applied)))
	} else {
		log.Info("миграции применены", slog.Int("count", pending))
	}
	return nil
}

type migration struct {
	version int64
	name    string
	path    string
}

// migrationFiles разбирает имена вида 0001_init.up.sql.
func migrationFiles() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}

	var out []migration
	for _, e := range entries {
		n := e.Name()
		// down-миграции здесь не применяются: откат схемы в проде —
		// осознанное ручное действие, а не побочный эффект деплоя.
		if !strings.HasSuffix(n, ".up.sql") {
			continue
		}
		parts := strings.SplitN(n, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("некорректное имя миграции %q: ожидается <версия>_<имя>.up.sql", n)
		}
		v, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("некорректная версия в %q: %w", n, err)
		}
		out = append(out, migration{version: v, name: n, path: "migrations/" + n})
	}

	// Сортировка по числовой версии, а не по имени файла.
	// Лексикографически "10" < "9", и на десятой миграции порядок
	// молча сломался бы.
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[int64]bool, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int64]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
