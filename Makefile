BIN := bin/tmux-menu
PREFIX ?= $(HOME)/.local

.PHONY: build test race vet validate validate-config coverage install sample-config config run palette agents tools projects links bookmarks status status-new clean

build:
	go build -o $(BIN) ./cmd/tmux-menu

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

validate: test race vet build sample-config validate-config

validate-config:
	go run ./cmd/tmux-menu validate-config examples/config.toml

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/tmux-menu

sample-config:
	go run ./cmd/tmux-menu sample-config

config:
	test -f $(HOME)/.tmux-menu.conf || go run ./cmd/tmux-menu sample-config > $(HOME)/.tmux-menu.conf

run: palette

palette:
	go run ./cmd/tmux-menu popup palette

agents:
	go run ./cmd/tmux-menu popup agents

tools:
	go run ./cmd/tmux-menu popup tools

projects:
	go run ./cmd/tmux-menu popup projects

links:
	go run ./cmd/tmux-menu popup links

bookmarks:
	go run ./cmd/tmux-menu popup bookmarks

status:
	go run ./cmd/tmux-menu popup status

status-new:
	sh scripts/status-new-mock.sh

clean:
	rm -rf bin
