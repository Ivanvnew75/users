// Package config — вся конфигурация сервиса, собранная в одном месте.
//
// Фактор 3 (Config) на практике: конфигурация читается ТОЛЬКО из переменных
// окружения и ТОЛЬКО здесь. Никаких os.Getenv в хендлерах — иначе через полгода
// никто не сможет ответить на вопрос «а какие вообще переменные нужны сервису».
// Один тип Config — это заодно и живая документация контракта с окружением.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Port — порт HTTP-сервера. Фактор 7 (Port binding): сервис сам
	// биндится на порт, ему не нужен внешний веб-сервер.
	Port string

	// DatabaseURL — адрес backing service (Фактор 4). Целиком одной строкой,
	// чтобы подмена in-cluster Postgres на managed-инстанс была правкой
	// одной переменной, а не пяти.
	DatabaseURL string

	// DBMaxConns — размер пула соединений. Вынесен в конфиг не для красоты:
	// у Postgres жёсткий лимит max_connections (по умолчанию 100), и при
	// горизонтальном масштабировании (Фактор 8) реплики его делят.
	// 10 реплик × пул 20 = 200 соединений = отказ базы.
	DBMaxConns int32

	// ShutdownTimeout — сколько ждать завершения текущих запросов при SIGTERM.
	// Фактор 9 (Disposability).
	ShutdownTimeout time.Duration

	// LogLevel/LogFormat — Фактор 11 (Logs).
	LogLevel  string
	LogFormat string
}

// Load читает конфигурацию из окружения и падает, если обязательного нет.
//
// Почему падаем сразу, а не «работаем как получится»: приложение,
// стартовавшее с неполным конфигом, ломается позже и в неожиданном месте.
// Fail fast на старте — это то, что превращает ошибку конфигурации
// в понятный CrashLoopBackOff вместо пятисоток в проде.
func Load() (Config, error) {
	c := Config{
		Port:            getenv("SERVER_PORT", "8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ShutdownTimeout: 15 * time.Second,
		LogLevel:        getenv("LOG_LEVEL", "info"),
		LogFormat:       getenv("LOG_FORMAT", "json"),
	}

	// DATABASE_URL — без разумного дефолта осознанно.
	//
	// Соблазн: «если переменной нет — работаем на in-memory хранилище».
	// Это прямое нарушение Фактора 10 (Dev/prod parity): разработчик гоняет
	// код на map, прод — на Postgres, и все различия (транзакции, гонки,
	// уникальные индексы, NULL) вылезают только в проде.
	// Локально базу поднимает docker-compose — см. compose.yaml.
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}

	maxConns, err := strconv.Atoi(getenv("DB_MAX_CONNS", "5"))
	if err != nil || maxConns <= 0 {
		return c, fmt.Errorf("DB_MAX_CONNS must be a positive integer, got %q", os.Getenv("DB_MAX_CONNS"))
	}
	c.DBMaxConns = int32(maxConns)

	if v := os.Getenv("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("SHUTDOWN_TIMEOUT must be a Go duration (e.g. 15s), got %q", v)
		}
		c.ShutdownTimeout = d
	}

	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
