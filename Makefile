PLUGIN_ID := com.bednarz.scheduler
PLUGIN_VERSION := 1.2.0
BUNDLE_NAME := $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz

GO ?= go
NPM ?= npm

.PHONY: all
all: dist

.PHONY: server
server:
	mkdir -p server/dist
	cd server && env GOOS=linux  GOARCH=amd64 $(GO) build -trimpath -o dist/plugin-linux-amd64 .
	cd server && env GOOS=linux  GOARCH=arm64 $(GO) build -trimpath -o dist/plugin-linux-arm64 .
	cd server && env GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -o dist/plugin-darwin-amd64 .
	cd server && env GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -o dist/plugin-darwin-arm64 .
	cd server && env GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o dist/plugin-windows-amd64.exe .

.PHONY: webapp
webapp:
	cd webapp && $(NPM) install
	cd webapp && $(NPM) run build

.PHONY: bundle
bundle:
	rm -rf dist
	mkdir -p dist/$(PLUGIN_ID)/server/dist
	mkdir -p dist/$(PLUGIN_ID)/webapp/dist
	cp plugin.json dist/$(PLUGIN_ID)/
	cp -r server/dist/. dist/$(PLUGIN_ID)/server/dist/
	cp -r webapp/dist/. dist/$(PLUGIN_ID)/webapp/dist/
	cd dist && tar -czf $(BUNDLE_NAME) $(PLUGIN_ID)

.PHONY: dist
dist: server webapp bundle

.PHONY: test
test:
	cd server && $(GO) test ./... -race -count=1

.PHONY: vet
vet:
	cd server && $(GO) vet ./...

.PHONY: clean
clean:
	rm -rf dist server/dist webapp/dist webapp/node_modules
