package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Ivanvnew75/users/actions"
	"github.com/Ivanvnew75/users/config"
	"github.com/Ivanvnew75/users/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Фактор 11 (Logs): пишем в stdout/stderr, никаких файлов и ротации.
		// Сбором занимается окружение, а не приложение.
		log.Fatalf("config error: %v", err)
	}

	ctx := context.Background()

	store, err := storage.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}
	defer store.Close()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	actions.New(store).Register(e)

	// Фактор 7 (Port binding): сервис сам открывает порт и является
	// самодостаточным. Ему не нужен ни Apache с mod_php, ни nginx-обёртка —
	// снаружи он выглядит как обычный HTTP-бэкенд, и это позволяет
	// одинаково запускать его локально, в Docker и в Kubernetes.
	go func() {
		addr := ":" + cfg.Port
		log.Printf("users service listening on %s", addr)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Фактор 9 (Disposability): корректное завершение по сигналу.
	//
	// Kubernetes при удалении пода шлёт SIGTERM и ждёт
	// terminationGracePeriodSeconds, потом SIGKILL. Если приложение
	// игнорирует SIGTERM, каждый деплой рвёт запросы «на лету» —
	// клиенты видят 502. Здесь мы перестаём принимать новые соединения
	// и даём текущим запросам доработать.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("stopped")
}
