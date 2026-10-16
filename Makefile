# Each target is what CI runs; `make check` runs them all locally.
GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: check fmt vet lint vuln test build snapshot release-check mod

check: fmt vet lint vuln test release-check mod

fmt:
	@out="$$(gofmt -l cmd internal)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint:
	$(GOBIN)/golangci-lint run ./...

vuln:
	$(GOBIN)/govulncheck ./...

test:
	go test -race ./...

build:
	go build -trimpath -o bin/wakewire ./cmd/wakewire

release-check:
	$(GOBIN)/goreleaser check

snapshot:
	$(GOBIN)/goreleaser release --snapshot --clean

mod:
	tsc -p mod
	claude plugin validate .
	claude plugin test mod

