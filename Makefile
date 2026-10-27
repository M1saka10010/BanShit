.PHONY: test race vet build check demo

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -o banshit .

check: test race vet build

demo: build
	BANSHIT_DEMO=1 ./banshit
