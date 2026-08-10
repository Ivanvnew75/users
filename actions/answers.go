package actions

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type answerInput struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// CreateAnswer — POST /users/:id/answers
func (h *Handler) CreateAnswer(c echo.Context) error {
	id, err := pathID(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	var in answerInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}
	if in.Answer == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "answer is required"})
	}

	a, err := h.Store.AddAnswer(c.Request().Context(), id, in.Question, in.Answer)
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusCreated, a)
}

// ListAnswers — GET /users/:id/answers?limit=N
func (h *Handler) ListAnswers(c echo.Context) error {
	id, err := pathID(c, "id")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	limit := intQuery(c, "limit", 20)
	if limit <= 0 || limit > 200 {
		limit = 20
	}

	list, err := h.Store.ListAnswers(c.Request().Context(), id, limit)
	if err != nil {
		return h.mapError(c, err)
	}
	return c.JSON(http.StatusOK, list)
}
