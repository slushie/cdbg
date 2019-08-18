all: cdbg app

cdbg: *.go
	go build .

app: app/app
app/app:
	cd app && gcc -o app app.c

test: all
	docker pull ubuntu:bionic
	-docker rm -f cdbg_app
	cid=$$(docker run --rm -d --name cdbg_app -v $(PWD)/app:/app ubuntu:bionic /app/app /app/state); \
		sudo ./cdbg $$cid
