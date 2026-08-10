// Package actions — HTTP-хендлеры микросервиса users.
//
// Хендлеры получают *storage.Store через структуру Handler, а не через
// глобальную переменную. Это не вкусовщина: глобальное состояние делает
// сервис непроверяемым (в тесте не подсунуть другую базу) и незаметно
// нарушает Фактор 6 (Processes) — глобалы соблазняют держать в них кэш,
// а значит состояние, которое теряется при рестарте пода.
package actions

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/Ivanvnew75/libs/common"
	"github.com/Ivanvnew75/users/storage"
)

type Handler struct {
	Store *storage.Store
	Log   *slog.Logger
}

func New(s *storage.Store, log *slog.Logger) *Handler {
	return &Handler{Store: s, Log: log}
}

// Register вешает маршруты. Собрано в одном месте, чтобы список ручек
// сервиса читался за пять секунд.
func (h *Handler) Register(e *echo.Echo) {
	e.GET("/health", h.Health)
	e.GET("/ready", h.Ready)

	e.POST("/users", h.CreateUser)
	e.GET("/users", h.GetUsers)
	e.GET("/users/:id", h.GetUser)
	e.PUT("/users/:id", h.UpdateUser)
	e.DELETE("/users/:id", h.DeleteUser)

	e.GET("/users/by-telegram/:tgid", h.GetUserByTelegram)

	// Ответы пользователя вложены в его ресурс — /users/:id/answers.
	// Это не украшательство URL: вложенность отражает владение. Ответ
	// не существует без пользователя (внешний ключ с CASCADE), и адрес
	// это показывает.
	e.POST("/users/:id/answers", h.CreateAnswer)
	e.GET("/users/:id/answers", h.ListAnswers)
}

// Health — liveness. Отвечает «процесс жив», НЕ ходит в базу.
//
// Это принципиально. Если liveness-проба проверяет базу, то короткая
// недоступность Postgres превращается в перезапуск ВСЕХ подов приложения:
// kubelet видит фейл пробы и убивает контейнеры. В результате к аварии базы
// добавляется холодный старт всего приложения — авария усиливается вместо
// того чтобы локализоваться. Правило: liveness проверяет только сам процесс.
func (h *Handler) Health(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}

// Ready — readiness. Вот здесь база проверяется.
//
// Смысл readiness другой: «можно ли слать мне трафик». Если база недоступна,
// под honestly отвечает 503, Service убирает его из endpoints, балансировщик
// перестаёт слать запросы — но под НЕ перезапускается и сам вернётся в строй,
// когда база поднимется.
func (h *Handler) Ready(c echo.Context) error {
	if err := h.Store.Ping(c.Request().Context()); err != nil {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{
			"status": "unavailable",
			"reason": "database is not reachable",
		})
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "ready"})
}

type userInput struct {
	TelegramID *int64 `json:"telegram_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
}

func (h *Handler) CreateUser(c echo.Context) error {
	var in userInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}
	if in.Name == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "name is required"})
	}

	// c.Request().Context() — контекст запроса, а не context.Background().
	// Если клиент отвалился, запрос к базе будет отменён и не будет
	// занимать соединение из пула впустую.
	u, err := h.Store.Create(c.Request().Context(), storage.User{
		TelegramID: in.TelegramID, Name: in.Name, Email: in.Email,
	})
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusCreated, u)
}

func (h *Handler) GetUsers(c echo.Context) error {
	limit := intQuery(c, "limit", 50)
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := intQuery(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	list, err := h.Store.List(c.Request().Context(), limit, offset)
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusOK, list)
}

func (h *Handler) GetUser(c echo.Context) error {
	id, err := pathID(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	u, err := h.Store.Get(c.Request().Context(), id)
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusOK, u)
}

func (h *Handler) GetUserByTelegram(c echo.Context) error {
	id, err := pathID(c, "tgid")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	u, err := h.Store.GetByTelegramID(c.Request().Context(), id)
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusOK, u)
}

func (h *Handler) UpdateUser(c echo.Context) error {
	id, err := pathID(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	var in userInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	u, err := h.Store.Update(c.Request().Context(), id, storage.User{
		TelegramID: in.TelegramID, Name: in.Name, Email: in.Email,
	})
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusOK, u)
}

func (h *Handler) DeleteUser(c echo.Context) error {
	id, err := pathID(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	if err := h.Store.Delete(c.Request().Context(), id); err != nil {
		return h.mapError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// mapError — единственное место, где доменные ошибки превращаются в HTTP-коды.
//
// Отдельно: наружу НЕ отдаётся текст ошибки базы. Сообщения Postgres содержат
// имена таблиц, колонок и иногда значения — это подсказка для атакующего
// и утечка внутреннего устройства. Детали уходят в лог, клиент получает 500.
func (h *Handler) mapError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return c.JSON(http.StatusNotFound, echo.Map{"error": "user not found"})
	case errors.Is(err, storage.ErrConflict):
		return c.JSON(http.StatusConflict, echo.Map{"error": "user already exists"})
	default:
		// Детали ошибки — в лог, наружу только общее сообщение.
		// request_id связывает 500-ку у клиента с этой строкой в логе:
		// пользователь присылает идентификатор, инженер находит причину
		// одним запросом, не имея ничего кроме него.
		h.Log.ErrorContext(c.Request().Context(), "storage error",
			slog.String("error", err.Error()),
			slog.String("request_id", common.RequestIDFromContext(c.Request().Context())),
			slog.String("uri", c.Request().URL.Path),
		)
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "internal error"})
	}
}

func pathID(c echo.Context, param string) (int64, error) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil {
		return 0, errors.New(param + " must be an integer")
	}
	return id, nil
}

func intQuery(c echo.Context, name string, def int) int {
	v := c.QueryParam(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
