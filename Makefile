BINARY := dependabot
PKGS := ./...

.PHONY: all build test cover lint fmt tidy clean

all: lint test build

build:
	go build -o $(BINARY) ./cmd/dependabot

test:
	go test $(PKGS)

cover:
	go test -coverprofile=coverage.out $(PKGS)
	go tool cover -func=coverage.out | tail -1

# gofmt -l prints the files that need formatting; make that a failure.
lint:
	@unformatted=$$(gofmt -l cmd internal pkg); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	go vet $(PKGS)

fmt:
	gofmt -w cmd internal pkg

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) coverage.out
