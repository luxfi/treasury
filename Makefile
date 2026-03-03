.PHONY: build run test docker

build:
	go build -o treasuryd ./cmd/treasuryd/

run: build
	./treasuryd

test:
	go test ./...

docker:
	docker build --platform linux/amd64 -t ghcr.io/luxfi/treasury:latest .

clean:
	rm -f treasuryd
