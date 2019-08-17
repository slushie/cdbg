all:
	go build ./...

test: all
	sudo ./cdbg $$cid
