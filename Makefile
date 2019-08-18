all: cdbg app

cdbg: *.go
	go build .

app: app/app
app/app: app/app.c
	cd app && gcc -g -o app app.c

test: all clean
	docker pull ubuntu:bionic
	cid=$$(docker run --rm -d --name cdbg_app -v $(PWD)/app:/app ubuntu:bionic /app/app /app/state); \
		sudo ./cdbg -ro=false $$cid
	-docker rm -f cdbg_app

clean:
	-docker rm -f cdbg_app
