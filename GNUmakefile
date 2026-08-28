default: build

build:
	go build -v .

install: build
	go install .

test:
	go test -v -cover ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

.PHONY: build install test testacc vet fmt
