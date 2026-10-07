.PHONY: build test check
# A stable signing identity keeps macOS privacy grants (Full Disk Access for
# Tart storage on external volumes) valid across rebuilds; ad-hoc signatures
# change with every build.
SIGN_IDENTITY ?= Apple Development: mallexxx@gmail.com (D46EWG5VWN)

build:
	go build -trimpath -o bin/ ./cmd/...
	@if [ "$$(uname -s)" = Darwin ]; then sh deploy/build-apple-browser.sh; fi
	@if [ "$$(uname -s)" = Darwin ]; then sh deploy/sign-binaries.sh "$(SIGN_IDENTITY)"; fi
test:
	go test -race ./...
check:
	python3 tools/check-docs.py
	python3 -m unittest discover -s internal/images -p '*_test.py'
	python3 -m unittest discover -s deploy -p '*_test.py'
	python3 -m unittest discover -s tools -p '*_test.py'
	@test -z "$$(gofmt -l cmd internal docs)" || (gofmt -l cmd internal docs; exit 1)
	go vet ./...
	go test -race ./...
