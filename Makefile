PROJECT=waves-lease-canceller
SOURCE=$(shell find . -name '*.go' | grep -v vendor/)
VERSION=$(shell git describe --tags --always --dirty)

.PHONY: vendor vetcheck fmtcheck clean

all: vendor vetcheck fmtcheck mod-clean dist

ver:
	@echo Building version: $(VERSION)

fmtcheck:
	@gofmt -l -s $(SOURCE) | grep ".*\.go"; if [ "$$?" = "0" ]; then exit 1; fi

mod-clean:
	go mod tidy

clean:
	@rm -rf build
	go mod tidy

vendor:
	go mod vendor

vetcheck:
	go vet ./...
	golangci-lint run

build-linux:
	@CGO_ENABLE=0 GOOS=linux GOARCH=amd64 go build -o build/bin/linux-amd64/waves-lease-canceller -ldflags="-X main.version=$(VERSION)" $(SOURCE)
build-darwin:
	@CGO_ENABLE=0 GOOS=darwin GOARCH=amd64 go build -o build/bin/darwin-amd64/waves-lease-canceller -ldflags="-X main.version=$(VERSION)" $(SOURCE)
build-windows:
	@CGO_ENABLE=0 GOOS=windows GOARCH=amd64 go build -o build/bin/windows-amd64/waves-lease-canceller.exe -ldflags="-X main.version=$(VERSION)" $(SOURCE)

release: ver build-linux build-darwin build-windows

dist: release
	@mkdir -p build/dist
	@cd ./build/; zip -j ./dist/waves-lease-canceller_$(VERSION)_Windows-64bit.zip ./bin/windows-amd64/waves-lease-canceller*
	@cd ./build/bin/linux-amd64/; tar pzcvf ../../dist/waves-lease-canceller_$(VERSION)_Linux-64bit.tar.gz ./waves-lease-canceller*
	@cd ./build/bin/darwin-amd64/; tar pzcvf ../../dist/waves-lease-canceller_$(VERSION)_macOS-64bit.tar.gz ./waves-lease-canceller*
