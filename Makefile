# make release — собирает linux-сервер и оба клиента, заливает клиенты как dev-релиз без активации.

SHELL := /usr/bin/bash

LINUX_BIN := client/src-tauri/target/release/modupdater-client
WINDOWS_BIN := client/src-tauri/target/x86_64-pc-windows-msvc/release/modupdater-client.exe
ifneq (,$(wildcard .env))
include .env
export
endif

SERVER_URL ?= $(if $(MODUPDATER_SERVER_URL),$(MODUPDATER_SERVER_URL),http://127.0.0.1:8080)
DEPLOY_PATH ?= $(if $(MODUPDATER_DEPLOY_PATH),$(MODUPDATER_DEPLOY_PATH),/opt/modupdater)

.PHONY: server client-linux client-windows clients all upload release bump upgrade bump-forge forge upload-forge

FORGE_PROPS := forge-mod/gradle.properties
FORGE_JAVA := $(shell if [ -x /usr/lib/jvm/java-21-openjdk/bin/javac ]; then echo /usr/lib/jvm/java-21-openjdk; elif [ -n "$$JAVA_HOME" ] && [ -x "$$JAVA_HOME/bin/javac" ]; then echo "$$JAVA_HOME"; fi)

server:
	python3 scripts/bump-server-version.py
	cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o modupdater ./cmd/modupdater

bump:
	python3 scripts/bump-client-version.py

client-linux: bump
	cd client && npx @tauri-apps/cli build --no-bundle

client-windows: bump
	cd client && PATH="$(HOME)/.cargo/bin:$$PATH" npx @tauri-apps/cli build --no-bundle --runner cargo-xwin --target x86_64-pc-windows-msvc

clients: client-linux client-windows

all: server clients

upload: $(LINUX_BIN) $(WINDOWS_BIN)
	cd server && go run ./cmd/upload -server "$(SERVER_URL)"

bump-forge:
	python3 scripts/bump-forge-version.py

forge: bump-forge
	cd forge-mod && JAVA_HOME="$(FORGE_JAVA)" PATH="$(FORGE_JAVA)/bin:$$PATH" ./gradlew jar --no-daemon

upload-forge: forge
	python3 scripts/upload-forge.py --server "$(SERVER_URL)" --jar "forge-mod/build/libs/modupdater-$$(sed -n 's/^mod_version=//p' $(FORGE_PROPS)).jar"

release: all upload

upgrade: server
	@test -n "$(MODUPDATER_DEPLOY_HOST)" || { echo "задайте MODUPDATER_DEPLOY_HOST" >&2; exit 1; }
	rsync --progress ./server/modupdater "$(MODUPDATER_DEPLOY_HOST):/tmp/modupdater"
	ssh -t "$(MODUPDATER_DEPLOY_HOST)" 'sudo mv /tmp/modupdater $(DEPLOY_PATH)/modupdater && sudo systemctl restart modupdater && sudo systemctl status modupdater'
