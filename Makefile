MAKEFLAGS += --always-make

default:
	go test
	go build

install-dev:
	rm -f ~/.local/bin/portmap
	ln -s $$PWD/portmap  ~/.local/bin/portmap

# mixing jj and git is fun
update-main:
	jj bookmark set main -r @-
	git checkout main

push-with-tags: update-main
	git push
	git push --tags

# uv tool install bump-my-version
major-release: pre-release
	bump-my-version bump major
	make push-with-tags

minor-release: pre-release
	bump-my-version bump minor
	make push-with-tags

patch-release: pre-release
	bump-my-version bump patch
	make push-with-tags
