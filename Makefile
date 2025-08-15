.PHONY: build dev

APP_BIN := ./cache
GRAFTERM := grafterm
GRAFTERM_ARGS := --config ./dashboard.json --datasource.prometheus.url http://localhost:9090

build:
	go build -o cache ./cmd/main.go

dev: build
	APP_BIN=$(APP_BIN) \
	GRAFTERM=$(GRAFTERM) \
	GRAFTERM_ARGS='$(GRAFTERM_ARGS)' \
	bash supervisor.sh
