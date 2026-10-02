// Command server hosts a shell build for browser runtime tests.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/djangbahevans/goerp/internal/engine/shellassets"
)

func main() {
	handler, err := shellassets.New(os.DirFS(os.Args[1]))
	if err != nil {
		panic(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println("http://" + listener.Addr().String())
	if err := http.Serve(listener, handler.Wrap(http.NotFoundHandler())); err != nil {
		panic(err)
	}
}
