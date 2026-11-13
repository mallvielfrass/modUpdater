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

.PHONY: server client-linux client-windows clients all upload release bump upgrade bump-forge forge upload-forge appimage deb linux-bundles

FORGE_PROPS := forge-mod/gradle.properties
FORGE_JAVA := $(shell if [ -x /usr/lib/jvm/java-21-openjdk/bin/javac ]; then echo /usr/lib/jvm/java-21-openjdk; elif [ -n "$$JAVA_HOME" ] && [ -x "$$JAVA_HOME/bin/javac" ]; then echo "$$JAVA_HOME"; fi)

server:
	python3 scripts/bump-server-version.py
	cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o modupdater ./cmd/modupdater

bump:
	python3 scripts/bump-client-version.py

client-linux: bump
	cd client && npx @tauri-apps/cli build --no-bundle

# appimage — linuxdeploy кладёт webkit и gtk в образ. deb — тот же набор в пакете, Depends только libc6. linux-bundles — оба.
appimage:
	cd client && CI=true npx @tauri-apps/cli build --bundles appimage

deb: appimage
	command -v dpkg-deb >/dev/null || { echo "нет dpkg-deb" >&2; exit 1; }
	set -euo pipefail; \
	ver=$$(python3 -c 'import json; print(json.load(open("client/src-tauri/tauri.conf.json"))["version"])'); \
	debarch=$$(uname -m); \
	if [ "$$debarch" = x86_64 ]; then debarch=amd64; fi; \
	bundle=client/src-tauri/target/release/bundle/appimage; \
	img="$$bundle/modupdater_$${ver}_$${debarch}.AppImage"; \
	if [ ! -f "$$img" ]; then img=$$(find "$$bundle" -maxdepth 1 -type f -name '*.AppImage' -print -quit); fi; \
	test -f "$$img"; \
	img=$$(readlink -f "$$img"); \
	work=client/src-tauri/target/release/bundle/deb/.work; \
	rm -rf "$$work"; \
	mkdir -p "$$work/extract" "$$work/root/DEBIAN" "$$work/root/opt/modupdater" "$$work/root/usr/bin"; \
	( cd "$$work/extract" && "$$img" --appimage-extract ); \
	cp -a "$$work/extract/squashfs-root/." "$$work/root/opt/modupdater/"; \
	test -x "$$work/root/opt/modupdater/AppRun"; \
	test -d "$$work/root/opt/modupdater/usr/lib"; \
	printf '%s\n' '#!/bin/sh' 'export APPDIR=/opt/modupdater' 'exec "$$APPDIR/AppRun" "$$@"' > "$$work/root/usr/bin/modupdater"; \
	chmod 755 "$$work/root/usr/bin/modupdater"; \
	{ \
		echo "Package: modupdater"; \
		echo "Version: $$ver"; \
		echo "Architecture: $$debarch"; \
		echo "Maintainer: modupdater"; \
		echo "Depends: libc6"; \
		echo "Description: Синхронизация модов Minecraft"; \
	} > "$$work/root/DEBIAN/control"; \
	mkdir -p client/src-tauri/target/release/bundle/deb; \
	dpkg-deb --root-owner-group --build "$$work/root" "client/src-tauri/target/release/bundle/deb/modupdater_$${ver}_$${debarch}.deb"; \
	rm -rf "$$work"

linux-bundles: deb

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
