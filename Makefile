MAKEFLAGS += --always-make

default:
	go build
	go test

install-dev:
	ln -s $$PWD/portmap ~/.local/bin/portmap 
