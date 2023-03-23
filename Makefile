build: build-arm64 build-amd64

build-arm64:
	GOOS=linux GOARCH=arm64 go build -o spotdrain-arm64 spotdrain

build-amd64:
	GOOS=linux GOARCH=amd64 go build -o spotdrain-amd64 spotdrain
