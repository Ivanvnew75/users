package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Ivanvnew75/libs/common"

	"github.com/Ivanvnew75/users/actions"
	"github.com/Ivanvnew75/users/config"
	"github.com/Ivanvnew75/users/storage"
)

// Заполняются линкером при сборке (-ldflags -X).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// До инициализации логгера писать больше нечем. Важно, что это
		// stderr, а не файл: поток подхватит рантайм (Фактор 11).
		slog.Error("config error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Один логгер на весь сервис. service и version добавляются в каждую
	// запись — без них логи трёх сервисов в общем хранилище неразличимы.
	logger := common.NewLogger("users", version, cfg.LogFormat, cfg.LogLevel)

	// Имя пода — из Downward API. В логе оно отвечает на вопрос
	// «это одна реплика сходит с ума или все».
	if pod := os.Getenv("POD_NAME"); pod != "" {
		logger = logger.With(slog.String("pod", pod))
	}

	// slog.SetDefault нужен, чтобы чужой код, пишущий через log.Printf
	// или slog по умолчанию, тоже попадал в наш JSON, а не в plain text.
	// Иначе в контейнере снова окажется два формата.
	slog.SetDefault(logger)

	logger.Info("starting", slog.String("commit", commit))

	ctx := context.Background()

	store, err := storage.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		logger.Error("storage init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer store.Close()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Порядок middleware имеет значение и читается сверху вниз:
	//   RequestID          — сначала выдать/принять идентификатор,
	//   PropagateRequestID — положить его в context для исходящих вызовов,
	//   RequestLogger      — залогировать запрос уже с этим идентификатором,
	//   Recover            — поймать панику ВНУТРИ логируемого участка,
	//                        иначе паника не попадёт в лог запроса.
	e.Use(common.RequestID())
	e.Use(common.PropagateRequestID())
	e.Use(common.RequestLogger(logger))
	e.Use(middleware.Recover())

	actions.New(store, logger).Register(e)

	go func() {
		addr := ":" + cfg.Port
		logger.Info("http server listening", slog.String("addr", addr))
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// Фактор 9: корректное завершение по SIGTERM.
	sigCtx, stop := common.SignalContext()
	defer stop()
	<-sigCtx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := common.ShutdownContext(cfg.ShutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}
	logger.Info("stopped")
}
