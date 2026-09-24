.PHONY: test run generate-ent migrate docker-up

test:
	go test ./...

run:
	go run ./cmd/api

generate-ent:
	GOARCH=amd64 go run -mod=mod entgo.io/ent/cmd/ent generate ./ent/schema

migrate:
	@echo "Apply migrations/001_init.sql with your MySQL migration runner"

docker-up:
	docker compose up --build
