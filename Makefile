SHELL := /bin/zsh

.PHONY: setup network test

setup:
	npm --prefix contracts ci
	@test -f .env || cp .env.example .env

# Keep this foreground command visible. Stop it with Ctrl C.
network: setup
	go run ./cmd/issuer-demo network

test:
	go test ./...
	npm --prefix contracts test
