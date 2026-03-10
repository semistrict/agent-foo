.PHONY: build test install clean

build:
	go build -o bin/af ./cmd/af

test: build
	go test -v -count=1 ./...

install:
	go install ./cmd/af

clean:
	rm -rf bin/
