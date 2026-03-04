MAKEFLAGS += --always-make

default:
	go build
	go test

install-dev:
	ln -s $$PWD/portmap ~/.local/bin/portmap 

major-release:
	gobump major -w
	jj commit -m major
minor-release:
	gobump minor -w
	jj commit -m minor
patch-release:
	gobump patch -w
	jj commit -m patch
