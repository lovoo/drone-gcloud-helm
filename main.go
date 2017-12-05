package main

import (
	"log"
)

func main() {
	p, err := newPlugin()
	if err != nil {
		log.Fatalf("could not initialize helm plugin: %v", err)
	}

	if err := p.Exec(); err != nil {
		log.Fatalf("failed to execute helm plugin: %v", err)
	}
}
