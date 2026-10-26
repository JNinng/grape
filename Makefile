GOHOSTOS:=$(shell go env GOHOSTOS)
GOPATH:=$(shell go env GOPATH)
VERSION=$(shell git describe --tags --always)

.PHONY: init
# init env
init:
	go install github.com/google/wire/cmd/wire@latest
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

.PHONY: config
# generate internal proto
config:
	buf generate --template buf.gen.config.yaml

.PHONY: config-init
# write initial config.yaml to a directory (from cmd/configinit template)
config-init:
	go run ./cmd/configinit

.PHONY: api
# generate api proto
api:
	buf generate --template buf.gen.yaml

.PHONY: sqlc
# generate storage code by sqlc
sqlc:
	cd internal/data/pg_sqlc/sqlc && sqlc generate -f sqlc.yaml

.PHONY: dbdeploy
# deploy database from scratch (admin DSN via --admin-dsn or GRAPE_DEPLOY_ADMIN_DSN)
dbdeploy:
	go run ./cmd/dbdeploy $(ARGS)

.PHONY: build
# build
build:
	mkdir -p bin/ && go build -ldflags "-X main.Version=$(VERSION)" -o ./bin/ ./...

.PHONY: web
# build web frontend into web/dist (must run BEFORE go build: go:embed collects dist at compile time)
# npm ci requires network; offline: run `npm install` in web/ manually first.
web:
	cd web && npm ci && npm run build

.PHONY: generate
# generate
generate:
	go generate ./...
	go mod tidy

.PHONY: all
# generate all
all:
	make api
	make config
	make sqlc
	make generate

# show help
help:
	@echo ''
	@echo 'Usage:'
	@echo ' make [target]'
	@echo ''
	@echo 'Targets:'
	@awk '/^[a-zA-Z0-9_-]+:/ { \
	helpMessage = match(lastLine, /^# (.*)/); \
		if (helpMessage) { \
			helpCommand = substr($$1, 0, index($$1, ":")); \
			helpMessage = substr(lastLine, RSTART + 2, RLENGTH); \
			printf "\033[36m%-22s\033[0m %s\n", helpCommand,helpMessage; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help
