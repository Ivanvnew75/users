// Package actions contains the HTTP handlers for the users microservice.
//
// Хранилище здесь намеренно сделано заглушкой (in-memory map): по условию
// задания реальных операций с базой данных пока нет — PgSQL появится позже.
package actions

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/labstack/echo/v4"
)

// User — модель пользователя микросервиса.
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Заглушка вместо базы данных: map под мьютексом, т.к. Echo обрабатывает
// запросы в отдельных горутинах.
var (
	mu     sync.RWMutex
	users  = map[int]User{}
	lastID int
)

// CreateUser — POST /users
func CreateUser(c echo.Context) error {
	u := new(User)
	if err := c.Bind(u); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	mu.Lock()
	lastID++
	u.ID = lastID
	users[u.ID] = *u
	mu.Unlock()

	return c.JSON(http.StatusCreated, u)
}

// GetUsers — GET /users
func GetUsers(c echo.Context) error {
	mu.RLock()
	list := make([]User, 0, len(users))
	for _, u := range users {
		list = append(list, u)
	}
	mu.RUnlock()

	return c.JSON(http.StatusOK, list)
}

// GetUser — GET /users/:id
func GetUser(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "id must be an integer"})
	}

	mu.RLock()
	u, ok := users[id]
	mu.RUnlock()

	if !ok {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "user not found"})
	}
	return c.JSON(http.StatusOK, u)
}

// UpdateUser — PUT /users/:id
func UpdateUser(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "id must be an integer"})
	}

	in := new(User)
	if err := c.Bind(in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := users[id]; !ok {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "user not found"})
	}

	in.ID = id
	users[id] = *in

	return c.JSON(http.StatusOK, in)
}

// DeleteUser — DELETE /users/:id
func DeleteUser(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "id must be an integer"})
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := users[id]; !ok {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "user not found"})
	}
	delete(users, id)

	return c.NoContent(http.StatusNoContent)
}

// Health — GET /health, понадобится для проб Kubernetes.
func Health(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}
