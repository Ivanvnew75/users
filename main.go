package main

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Ivanvnew75/users/actions"
)

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

	e.Logger.Fatal(e.Start(":8080"))
}
