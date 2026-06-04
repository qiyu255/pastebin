.PHONY: all build pastebin passwd run clean test

BINDIR ?= bin

all: build

build: pastebin passwd

pastebin:
	go build -o $(BINDIR)/pastebin ./cmd/pastebin

passwd:
	go build -o $(BINDIR)/passwd ./cmd/passwd

run: build
	./pastebin -config config.json

clean:
	rm -f pastebin passwd

test:
	go test ./...
