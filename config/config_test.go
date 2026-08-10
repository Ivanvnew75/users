package config

import (
	"testing"
	"time"
)

// Тесты на конфигурацию — самые дешёвые и одни из самых полезных.
// Ошибка в разборе переменных окружения проявляется только на деплое
// в конкретное окружение, то есть в худший момент.
func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(*testing.T, Config)
	}{
		{
			name:    "DATABASE_URL обязателен",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "дефолты применяются",
			env:  map[string]string{"DATABASE_URL": "postgres://u:p@h:5432/d"},
			check: func(t *testing.T, c Config) {
				if c.Port != "8080" {
					t.Errorf("Port = %q, ожидался 8080", c.Port)
				}
				if c.DBMaxConns != 5 {
					t.Errorf("DBMaxConns = %d, ожидалось 5", c.DBMaxConns)
				}
				if c.ShutdownTimeout != 15*time.Second {
					t.Errorf("ShutdownTimeout = %v, ожидалось 15s", c.ShutdownTimeout)
				}
			},
		},
		{
			name: "переменные окружения перекрывают дефолты",
			env: map[string]string{
				"DATABASE_URL":     "postgres://u:p@h:5432/d",
				"SERVER_PORT":      "9090",
				"DB_MAX_CONNS":     "20",
				"SHUTDOWN_TIMEOUT": "5s",
			},
			check: func(t *testing.T, c Config) {
				if c.Port != "9090" || c.DBMaxConns != 20 || c.ShutdownTimeout != 5*time.Second {
					t.Errorf("конфиг из окружения не применился: %+v", c)
				}
			},
		},
		{
			// Отрицательный тест важнее положительного: он проверяет,
			// что сервис упадёт на старте, а не будет работать
			// с молча подставленным дефолтом.
			name: "мусор в DB_MAX_CONNS — ошибка, а не тихий дефолт",
			env: map[string]string{
				"DATABASE_URL": "postgres://u:p@h:5432/d",
				"DB_MAX_CONNS": "много",
			},
			wantErr: true,
		},
		{
			name: "мусор в SHUTDOWN_TIMEOUT — ошибка",
			env: map[string]string{
				"DATABASE_URL":     "postgres://u:p@h:5432/d",
				"SHUTDOWN_TIMEOUT": "15",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv сам восстанавливает окружение после теста
			// и запрещает t.Parallel — иначе тесты на env гонялись бы
			// между собой. Ровно то, что нужно.
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			// Переменные, которых нет в кейсе, надо явно погасить:
			// иначе окружение CI-раннера протечёт в тест.
			for _, k := range []string{"DATABASE_URL", "SERVER_PORT", "DB_MAX_CONNS", "SHUTDOWN_TIMEOUT", "LOG_LEVEL", "LOG_FORMAT"} {
				if _, ok := tt.env[k]; !ok {
					t.Setenv(k, "")
				}
			}

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка, получен конфиг %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
