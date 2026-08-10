package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Интеграционный тест против НАСТОЯЩЕГО PostgreSQL.
//
// Почему не мок базы: мок проверяет, что мы вызвали метод, который сами же
// и придумали. Он не поймает ни опечатку в SQL, ни неверный тип колонки,
// ни сработавший уникальный индекс — то есть ровно те ошибки, ради которых
// этот код и тестируют.
//
// Фактор 10 (Dev/prod parity) прямо об этом: в тестах должна быть та же
// версия того же движка, что в проде. В CI базу поднимает service container
// (см. .github/workflows/ci.yml), локально — docker compose.
//
// Если TEST_DATABASE_URL не задан, тест пропускается, а не падает:
// `go test ./...` на машине без Docker должен оставаться зелёным.
func testStore(t *testing.T) *Store {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — пропускаю интеграционные тесты")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := New(ctx, dsn, 4)
	if err != nil {
		t.Fatalf("подключение к тестовой базе: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping тестовой базы: %v", err)
	}
	t.Cleanup(s.Close)

	// Чистим таблицы перед каждым тестом.
	// TRUNCATE ... RESTART IDENTITY CASCADE: сбрасывает счётчик id
	// (иначе тесты зависят от порядка запуска) и обходит внешний ключ
	// mood_answers → users.
	if _, err := s.pool.Exec(ctx, `TRUNCATE users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	tgID := int64(4242)
	created, err := s.Create(ctx, User{TelegramID: &tgID, Name: "Ivan", Email: "i@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("Create вернул пользователя без id")
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at не заполнен — значит RETURNING отдал не то, что ожидалось")
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Ivan" {
		t.Errorf("Get вернул name=%q, ожидалось Ivan", got.Name)
	}

	byTG, err := s.GetByTelegramID(ctx, tgID)
	if err != nil {
		t.Fatalf("GetByTelegramID: %v", err)
	}
	if byTG.ID != created.ID {
		t.Errorf("GetByTelegramID вернул id=%d, ожидался %d", byTG.ID, created.ID)
	}
}

// Проверяем именно перевод SQLSTATE 23505 в доменную ошибку.
// Это то место, которое ломается при смене драйвера или версии Postgres,
// и без теста ломается молча: 409 превращается в 500.
func TestDuplicateTelegramIDGivesConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	tgID := int64(777)
	if _, err := s.Create(ctx, User{TelegramID: &tgID, Name: "first"}); err != nil {
		t.Fatalf("первый Create: %v", err)
	}
	_, err := s.Create(ctx, User{TelegramID: &tgID, Name: "second"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ожидался ErrConflict, получено: %v", err)
	}
}

func TestGetMissingGivesNotFound(t *testing.T) {
	s := testStore(t)
	if _, err := s.Get(context.Background(), 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено: %v", err)
	}
}

// DELETE несуществующей строки не возвращает ошибку на уровне SQL —
// проверяем, что мы её различаем по RowsAffected.
func TestDeleteMissingGivesNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.Delete(context.Background(), 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено: %v", err)
	}
}

func TestListPagination(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := s.Create(ctx, User{Name: "u"}); err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}

	page, err := s.List(ctx, 2, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("limit=2 вернул %d записей", len(page))
	}

	second, err := s.List(ctx, 2, 2)
	if err != nil {
		t.Fatalf("List offset=2: %v", err)
	}
	if len(second) != 2 || second[0].ID == page[0].ID {
		t.Errorf("offset не сработал: первая страница %v, вторая %v", page[0].ID, second[0].ID)
	}
}
