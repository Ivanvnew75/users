# Makefile как единая точка входа в проект.
#
# Смысл не в экономии символов, а в том, что команды перестают быть
# знанием в голове конкретного человека. Новый разработчик читает
# `make help` вместо того, чтобы искать нужный набор флагов в чужой
# истории команд. Это Фактор 10 со стороны «разрыва в людях».

.DEFAULT_GOAL := help
COMPOSE := docker compose
TEST_DB_URL := postgres://moodbot:devpassword@localhost:15432/moodbot?sslmode=disable

.PHONY: help
help: ## показать эту справку
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: up
up: ## поднять локальное окружение (postgres + миграции + сервис)
	$(COMPOSE) up -d --build
	@echo "users на http://localhost:18080"

.PHONY: down
down: ## погасить окружение вместе с данными
	$(COMPOSE) down -v

.PHONY: logs
logs: ## логи сервиса
	$(COMPOSE) logs -f users

.PHONY: psql
psql: ## psql в локальную базу
	$(COMPOSE) exec postgres psql -U moodbot -d moodbot

.PHONY: migrate
migrate: ## накатить миграции заново
	$(COMPOSE) run --rm migrate

.PHONY: test
test: ## тесты против локальной базы (те же, что в CI)
	TEST_DATABASE_URL="$(TEST_DB_URL)" go test -race -count=1 ./...

.PHONY: lint
lint: ## то же, что проверяет CI
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "не отформатированы:"; echo "$$unformatted"; exit 1; fi
	go vet ./...

.PHONY: parity
parity: ## проверить, что версии образов не разъехались
	./scripts/check-parity.sh

.PHONY: smoke
smoke: ## быстрый прогон API по локальному окружению
	@curl -fsS localhost:18080/health && echo
	@curl -fsS localhost:18080/ready && echo
	@curl -fsS -XPOST localhost:18080/users -H 'Content-Type: application/json' \
		-d '{"name":"local","email":"local@example.com"}' && echo
	@curl -fsS localhost:18080/users && echo
