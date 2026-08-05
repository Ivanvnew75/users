package main

import (
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Ivanvnew75/users/actions"
)

// defaultPort используется, если переменная окружения SERVER_PORT не задана.
const defaultPort = "8080"

// serverPort — Фактор 3 (Config): конфигурация берётся из окружения,
// а не хардкодится в коде. Значение по умолчанию оставляем, чтобы сервис
// поднимался локально без дополнительных настроек.
func serverPort() string {
	if p := os.Getenv("SERVER_PORT"); p != "" {
		return p
	}
	return defaultPort
}

func main() {
	e := echo.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/health", actions.Health)

	e.POST("/users", actions.CreateUser)
	e.GET("/users", actions.GetUsers)
	e.GET("/users/:id", actions.GetUser)
	e.PUT("/users/:id", actions.UpdateUser)
	e.DELETE("/users/:id", actions.DeleteUser)

	e.Logger.Fatal(e.Start(":" + serverPort()))
}
